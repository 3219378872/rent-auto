package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/api"
	"github.com/3219378872/rent-auto/backend/internal/auth"
	"github.com/3219378872/rent-auto/backend/internal/channels"
	"github.com/3219378872/rent-auto/backend/internal/config"
	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/logging"
	"github.com/3219378872/rent-auto/backend/internal/platform"
	"github.com/3219378872/rent-auto/backend/internal/platform/eco"
	"github.com/3219378872/rent-auto/backend/internal/pricing"
	"github.com/3219378872/rent-auto/backend/internal/ratelimit"
	"github.com/3219378872/rent-auto/backend/internal/recon"
	"github.com/3219378872/rent-auto/backend/internal/scheduler"
	"github.com/3219378872/rent-auto/backend/internal/secrets"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

const version = "0.5.0"

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logging.New(cfg.LogLevel)

	ctx := context.Background()
	pool, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	unlock, ok, err := store.TryAdvisoryLock(ctx, pool)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("another instance is already running (advisory lock held)")
	}
	defer unlock()

	applied, err := store.MigrateUp(ctx, pool)
	if err != nil {
		return err
	}
	for _, v := range applied {
		log.Info("migration applied", "version", v)
	}

	st := store.New(pool)
	hash, err := resolveAdminPassword(ctx, st, cfg, log)
	if err != nil {
		return err
	}

	var box *secrets.Box
	if cfg.MasterKey != nil {
		box, err = secrets.NewBox(cfg.MasterKey)
		if err != nil {
			return err
		}
	} else {
		log.Warn("APP_MASTER_KEY not set: channel credentials cannot be stored")
	}

	registry := channels.NewRegistry(st, box, log)
	registry.SetLimiter(domain.ChannelUU, newLimiter(3))
	registry.SetLimiter(domain.ChannelECO, newLimiter(2))
	if err := registry.Refresh(ctx); err != nil {
		log.Warn("channel adapters refresh", "err", err)
	}

	strategyID, _, err := st.EnsureGlobalStrategy(ctx, "{}")
	if err != nil {
		return err
	}
	log.Info("global strategy ready", "id", strategyID)

	deps := scheduler.Deps{Store: st, Log: log, DryRun: cfg.DryRunDefault}
	sch := scheduler.New(log)

	// Reconcile pipeline: plan desired-vs-actual shelf state, then execute.
	planner := &recon.Planner{Store: st, Log: log, Health: registry.Health}
	executor := &recon.Executor{DryRun: cfg.DryRunDefault, Log: log,
		Audit: func(ctx context.Context, e domain.AuditEntry) { _ = st.InsertAudit(ctx, e) }}
	steamSess := channels.NewSteamSession(st, box, log)
	if err := steamSess.Restore(ctx); err != nil {
		log.Warn("steam session restore", "err", err)
	}
	uuDeliveryFn := func(ctx context.Context) error {
		sent, gifts, err := registry.DeliverPendingRentals(ctx)
		log.Info("uu delivery", "sent", len(sent), "gifts_skipped", gifts, "err", err)
		return err
	}
	steamOffersFn := func(ctx context.Context) error {
		accepted, skipped, err := steamSess.AcceptZeroCostOffers(ctx, log)
		if err != nil {
			log.Warn("steam offers", "err", err)
		}
		log.Info("steam offers", "accepted", accepted, "skipped_costly", skipped)
		return err
	}

	channels.AuditFn = func(ctx context.Context, e domain.AuditEntry) {
		_ = st.InsertAudit(ctx, e)
	}
	ecoDeps := &scheduler.EcoDeliveryDeps{
		Eco:   liveECOClient{r: registry},
		Steam: steamSess,
		Audit: func(ctx context.Context, e domain.AuditEntry) { _ = st.InsertAudit(ctx, e) },
		Log:   log,
	}
	ecoDeliveryFn := func(ctx context.Context) error {
		err := ecoDeps.RunECODelivery(ctx)
		if err != nil {
			log.Warn("eco delivery", "err", err)
			return err
		}
		// 平台批量兜底（归还方向等），失败不阻塞主链路
		if err := registry.EcoOneClickResolve(ctx); err != nil && err != platform.ErrUnsupported {
			log.Warn("eco oneclick resolve", "err", err)
		}
		return nil
	}

	reconcileFn := func(ctx context.Context) error {
		ads := map[domain.Channel]platform.Adapter{}
		for _, a := range registry.All() {
			ads[a.Channel()] = a
		}
		executor.Adapters = ads
		plan, err := planner.Plan(ctx)
		if err != nil {
			return err
		}
		applied, failed := executor.Execute(ctx, plan)
		log.Info("reconcile done", "plan", len(plan), "applied", applied, "failed", failed)
		return nil
	}

	for _, job := range scheduler.Jobs(&deps, registry.All, uuQuotesFn(registry), ecoDumpFn(registry), registry.ClearZeroCD, reconcileFn, uuDeliveryFn, steamOffersFn, ecoDeliveryFn, log) {
		if err := sch.Register(job); err != nil {
			return err
		}
	}

	srv := api.NewServer(st, auth.NewJWT(cfg.JWTSecret), cfg.AdminUser, version, log)
	srv.PasswordHash = func(context.Context) (string, error) { return hash, nil }
	srv.Jobs = schedulerAdapter{sch}
	srv.Channels = registry
	srv.Steam = steamSess
	srv.Wallets = func(ctx context.Context) map[domain.Channel]float64 {
		out := map[domain.Channel]float64{}
		for _, a := range registry.All() {
			if v, err := a.Wallet(ctx); err == nil {
				out[a.Channel()] = v
			}
		}
		return out
	}

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	rootCtx, stopCtx := context.WithCancel(context.Background())
	defer stopCtx()
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	go func() { sch.Start(rootCtx) }()
	log.Info("server listening", "addr", cfg.Addr, "version", version, "dry_run_default", cfg.DryRunDefault)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		log.Info("shutting down", "signal", sig.String())
		stopCtx()
		sch.Stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}

func newLimiter(rps float64) platform.Limiter { return ratelimit.New(rps) }

// liveECOClient forwards ECO order calls to whatever client is currently
// configured in the registry, so panel credential updates take effect on the
// next delivery cycle without a restart.
type liveECOClient struct{ r *channels.Registry }

func (l liveECOClient) client() (interface {
	SellerOrderList(ctx context.Context, start, end time.Time, detailsState *int, steamID string) ([]eco.SellerOrder, error)
	SendOffer(ctx context.Context, orderNum string) (*eco.SendOfferResult, error)
	Detail(ctx context.Context, orderNum string) (*eco.SellerOrderDetail, error)
}, error) {
	if c := l.r.EcoOrderClient(); c != nil {
		return c, nil
	}
	return nil, platform.ErrUnsupported
}

func (l liveECOClient) SellerOrderList(ctx context.Context, start, end time.Time, detailsState *int, steamID string) ([]eco.SellerOrder, error) {
	c, err := l.client()
	if err != nil {
		return nil, err
	}
	return c.SellerOrderList(ctx, start, end, detailsState, steamID)
}

func (l liveECOClient) SendOffer(ctx context.Context, orderNum string) (*eco.SendOfferResult, error) {
	c, err := l.client()
	if err != nil {
		return nil, err
	}
	return c.SendOffer(ctx, orderNum)
}

func (l liveECOClient) Detail(ctx context.Context, orderNum string) (*eco.SellerOrderDetail, error) {
	c, err := l.client()
	if err != nil {
		return nil, err
	}
	return c.Detail(ctx, orderNum)
}

func uuQuotesFn(r *channels.Registry) func(context.Context, int64, float64, float64) ([]pricing.Quote, error) {
	return func(ctx context.Context, tplID int64, minP, maxP float64) ([]pricing.Quote, error) {
		items, err := r.UUMarketQuotes(ctx, tplID, minP, maxP)
		if err != nil {
			return nil, err
		}
		out := make([]pricing.Quote, 0, len(items))
		for _, it := range items {
			out = append(out, pricingQuoteAlias{
				Name:    it.CommodityName,
				Short:   deref(it.LeaseUnitPrice),
				Long:    deref(it.LongLeaseUnitPrice),
				Deposit: it.Deposit(),
			})
		}
		return out, nil
	}
}

func ecoDumpFn(r *channels.Registry) func(context.Context) (map[string]float64, error) {
	return r.EcoRefPrices
}

type pricingQuoteAlias = struct {
	Name    string
	Short   float64
	Long    float64
	Deposit float64
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

// schedulerAdapter bridges scheduler.Status to the api JobStatus shape.
type schedulerAdapter struct{ s *scheduler.Scheduler }

func (a schedulerAdapter) StatusList() []api.JobStatus {
	src := a.s.StatusList()
	out := make([]api.JobStatus, len(src))
	for i, s := range src {
		out[i] = api.JobStatus(s)
	}
	return out
}

func (a schedulerAdapter) Trigger(ctx context.Context, name string) error {
	return a.s.Trigger(ctx, name)
}

// resolveAdminPassword returns the bcrypt hash used by login:
// env override wins; otherwise bootstrap a random password once and persist it.
func resolveAdminPassword(ctx context.Context, st *store.Store, cfg *config.Config, log *slog.Logger) (string, error) {
	if cfg.AdminPassHash != "" {
		return cfg.AdminPassHash, nil
	}
	setting, err := st.GetSetting(ctx, "admin_password_hash")
	switch {
	case err == nil && setting.ValueEnc != nil:
		return string(setting.ValueEnc), nil
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return "", err
	}
	pw, err := config.BootstrapPassword()
	if err != nil {
		return "", err
	}
	h, err := auth.HashPassword(pw)
	if err != nil {
		return "", err
	}
	if err := st.UpsertSettingEnc(ctx, "admin_password_hash", []byte(h)); err != nil {
		return "", err
	}
	log.Warn("BOOTSTRAP admin password generated ONCE — change it after first login", "password", pw)
	_ = st.InsertAudit(ctx, domain.AuditEntry{Time: time.Now().UTC(), Actor: "system", Action: "bootstrap.admin_password"})
	return h, nil
}
