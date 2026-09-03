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

const version = "0.7.0"

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
		log.Error("APP_MASTER_KEY not set: channel credentials cannot be stored or loaded; set it to enable UU/ECO/Steam channels")
	}

	registry := channels.NewRegistry(st, box, log)
	registry.SetLimiter(domain.ChannelUU, newLimiter(3))
	registry.SetLimiter(domain.ChannelECO, newLimiter(2))
	registry.SetAuditFn(func(ctx context.Context, e domain.AuditEntry) {
		if err := st.InsertAudit(ctx, e); err != nil {
			log.Warn("audit insert failed", "action", e.Action, "err", err)
		}
	})
	if err := registry.Refresh(ctx); err != nil {
		log.Warn("channel adapters refresh", "err", err)
	}

	strategyID, _, err := st.EnsureGlobalStrategy(ctx, "{}")
	if err != nil {
		return err
	}
	log.Info("global strategy ready", "id", strategyID)

	deps := scheduler.Deps{Store: st, Log: log, DryRun: cfg.DryRunDefault,
		Audit: func(ctx context.Context, e domain.AuditEntry) {
			if err := st.InsertAudit(ctx, e); err != nil {
				log.Warn("audit insert failed", "action", e.Action, "err", err)
			}
		}}
	sch := scheduler.New(log)

	// Reconcile pipeline: plan desired-vs-actual shelf state, then execute.
	planner := &recon.Planner{Store: st, Log: log, Health: registry.Health}
	executor := &recon.Executor{Log: log,
		Audit: func(ctx context.Context, e domain.AuditEntry) {
			if err := st.InsertAudit(ctx, e); err != nil {
				log.Warn("audit insert failed", "action", e.Action, "err", err)
			}
		},
		Store:    st,
		Penalize: deps.NoteChannelError,
	}
	steamSess := channels.NewSteamSession(st, box, log)
	steamSess.SetAuditFn(func(ctx context.Context, e domain.AuditEntry) {
		if err := st.InsertAudit(ctx, e); err != nil {
			log.Warn("audit insert failed", "action", e.Action, "err", err)
		}
	})
	if err := steamSess.Restore(ctx); err != nil {
		log.Warn("steam session restore", "err", err)
	}
	uuDeliveryFn := func(ctx context.Context) error {
		if cfg.DryRunDefault || !globalRealEnabled(ctx, st, log) {
			log.Info("uu delivery skipped: dry-run")
			if err := st.InsertAudit(ctx, domain.AuditEntry{Time: time.Now().UTC(), Actor: "system",
				Action: "uu_delivery.dry_run_skip", Detail: map[string]any{"dry_run": true}}); err != nil {
				log.Warn("audit insert failed", "action", "uu_delivery.dry_run_skip", "err", err)
			}
			return nil
		}
		sent, gifts, err := registry.DeliverPendingRentals(ctx)
		log.Info("uu delivery", "sent", len(sent), "gifts_skipped", gifts, "err", err)
		return err
	}
	steamOffersFn := func(ctx context.Context) error {
		if cfg.DryRunDefault || !globalRealEnabled(ctx, st, log) {
			log.Info("steam offers skipped: dry-run")
			if err := st.InsertAudit(ctx, domain.AuditEntry{Time: time.Now().UTC(), Actor: "system",
				Action: "steam_offers.dry_run_skip", Detail: map[string]any{"dry_run": true}}); err != nil {
				log.Warn("audit insert failed", "action", "steam_offers.dry_run_skip", "err", err)
			}
			return nil
		}
		accepted, skipped, err := steamSess.AcceptZeroCostOffers(ctx, log)
		if err != nil {
			log.Warn("steam offers", "err", err)
		}
		log.Info("steam offers", "accepted", accepted, "skipped_costly", skipped)
		return err
	}

	ecoDeps := &scheduler.EcoDeliveryDeps{
		Eco:   liveECOClient{r: registry},
		Steam: steamSess,
		Audit: func(ctx context.Context, e domain.AuditEntry) {
			if err := st.InsertAudit(ctx, e); err != nil {
				log.Warn("audit insert failed", "action", e.Action, "err", err)
			}
		},
		Log: log,
	}
	ecoDeliveryFn := func(ctx context.Context) error {
		if cfg.DryRunDefault || !globalRealEnabled(ctx, st, log) {
			log.Info("eco delivery skipped: dry-run")
			if err := st.InsertAudit(ctx, domain.AuditEntry{Time: time.Now().UTC(), Actor: "system",
				Action: "eco_delivery.dry_run_skip", Detail: map[string]any{"dry_run": true}}); err != nil {
				log.Warn("audit insert failed", "action", "eco_delivery.dry_run_skip", "err", err)
			}
			return nil
		}
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
	zeroCDFn := func(ctx context.Context) error {
		if cfg.DryRunDefault || !globalRealEnabled(ctx, st, log) {
			log.Info("zero_cd skipped: dry-run")
			if err := st.InsertAudit(ctx, domain.AuditEntry{Time: time.Now().UTC(), Actor: "system",
				Action: "zero_cd.dry_run_skip", Detail: map[string]any{"dry_run": true}}); err != nil {
				log.Warn("audit insert failed", "action", "zero_cd.dry_run_skip", "err", err)
			}
			return nil
		}
		return registry.ClearZeroCD(ctx)
	}

	reconcileFn := func(ctx context.Context) error {
		adapters := map[domain.Channel]platform.Adapter{}
		caps := map[domain.Channel]platform.Capabilities{}
		for _, a := range registry.All() {
			adapters[a.Channel()] = a
			caps[a.Channel()] = a.Caps()
		}
		executor.Adapters = adapters
		planner.Caps = caps
		// Strategy-level dry-run gate (AC-T1), mirroring the reprice job:
		// effective dry-run = global default OR strategy not real-enabled;
		// unknown strategy state fails closed.
		executor.DryRun = cfg.DryRunDefault || !globalRealEnabled(ctx, st, log)
		plan, err := planner.Plan(ctx)
		if err != nil {
			return err
		}
		kept := plan[:0]
		for _, a := range plan {
			if deps.ChannelReady(a.Channel) { // risk-control cooldown discipline
				kept = append(kept, a)
			}
		}
		cooldownSkipped := len(plan) - len(kept)
		if executor.DryRun {
			// Global dry-run is the floor: everything records without platform calls.
			applied, failed := executor.Execute(ctx, kept)
			log.Info("reconcile done", "plan", len(kept), "applied", applied, "failed", failed,
				"cooldown_skipped", cooldownSkipped, "dry_run", true)
			return nil
		}
		// Template-level dry-run: each publish/delist is gated by its hash's
		// effective strategy; RealEnabled=false forces a dry-run record.
		// Strategy lookup failure fails closed (skip + error log, no platform call).
		var dryPlan, livePlan []recon.Action
		strategySkipped := 0
		for _, a := range kept {
			es, err := st.GetEffectiveStrategy(ctx, a.HashName)
			if err != nil {
				log.Error("reconcile strategy lookup failed; skipping action (fail-closed)",
					"hash", a.HashName, "kind", a.Kind, "err", err)
				strategySkipped++
				continue
			}
			if !es.RealEnabled {
				dryPlan = append(dryPlan, a)
			} else {
				livePlan = append(livePlan, a)
			}
		}
		applied, failed := 0, 0
		if len(dryPlan) > 0 {
			executor.DryRun = true
			da, df := executor.Execute(ctx, dryPlan)
			applied += da
			failed += df
		}
		if len(livePlan) > 0 {
			executor.DryRun = false
			la, lf := executor.Execute(ctx, livePlan)
			applied += la
			failed += lf
		}
		executor.DryRun = false
		log.Info("reconcile done", "plan", len(kept), "applied", applied, "failed", failed,
			"cooldown_skipped", cooldownSkipped, "strategy_skipped", strategySkipped,
			"dry_plan", len(dryPlan), "live_plan", len(livePlan), "dry_run", false)
		return nil
	}

	for _, job := range scheduler.Jobs(&deps, registry.All, uuQuotesFn(registry), ecoDumpFn(registry), zeroCDFn, reconcileFn, uuDeliveryFn, steamOffersFn, ecoDeliveryFn, log) {
		if err := sch.Register(job); err != nil {
			return err
		}
	}

	srv := api.NewServerWithTTL(st, auth.NewJWT(cfg.JWTSecret), cfg.AdminUser, version, cfg.JWTTTL, log)
	srv.SetTrustProxies(cfg.TrustProxies)
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
		ReadTimeout:       30 * time.Second,
		// Trigger runs long jobs synchronously (up to the 10-minute job
		// budget); the panel should poll GET /jobs for completion instead
		// of holding the trigger request open.
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	rootCtx, stopCtx := context.WithCancel(context.Background())
	defer stopCtx()
	// Advisory-lock heartbeat: re-probe every 5min. A re-acquired lock means
	// the original holder connection died — never auto-preempt, only alert.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("advisory heartbeat panic recovered", "panic", r)
			}
		}()
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-rootCtx.Done():
				return
			case <-t.C:
				probeUnlock, probeOK, probeErr := store.TryAdvisoryLock(rootCtx, pool)
				if probeErr == nil && probeOK {
					// Lock was free: the original guard is gone. Release the
					// probe immediately (no preemption) and alert loudly.
					probeUnlock()
					log.Error("advisory lock lost: another probe acquired it; not preempting, operator action required")
					if aerr := st.InsertAudit(rootCtx, domain.AuditEntry{Time: time.Now().UTC(),
						Actor: "system", Action: "system.advisory_lock_lost",
						Detail: map[string]any{"error": "lock was free on re-probe"}}); aerr != nil {
						log.Warn("audit insert failed", "action", "system.advisory_lock_lost", "err", aerr)
					}
					continue
				}
				if probeUnlock != nil {
					probeUnlock()
				}
				if probeErr != nil && (probeOK || probeUnlock != nil) {
					log.Warn("advisory lock re-probe failed", "err", probeErr)
				}
			}
		}
	}()
	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("http serve panic recovered", "panic", r)
				errCh <- errors.New("http server panic")
			}
		}()
		errCh <- httpSrv.ListenAndServe()
	}()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("scheduler panic recovered", "panic", r)
			}
		}()
		sch.Start(rootCtx)
	}()
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

// globalRealEnabled reports the global strategy's real_execution_enabled flag.
// Lookup failure fails closed (dry-run) — reconcile must never go live on an
// unreadable gate.
func globalRealEnabled(ctx context.Context, st *store.Store, log *slog.Logger) bool {
	es, err := st.GetEffectiveStrategy(ctx, "")
	if err != nil {
		log.Warn("reconcile strategy gate lookup failed; forcing dry-run", "err", err)
		return false
	}
	return es.RealEnabled
}

// liveECOClient forwards ECO order calls to whatever client is currently
// configured in the registry, so panel credential updates take effect on the
// next delivery cycle without a restart.
type liveECOClient struct{ r *channels.Registry }

type ecoOrderClient interface {
	SellerOrderList(ctx context.Context, start, end time.Time, detailsState *int, steamID string) ([]eco.SellerOrder, error)
	SendOffer(ctx context.Context, orderNum string) (*eco.SendOfferResult, error)
	Detail(ctx context.Context, orderNum string) (*eco.SellerOrderDetail, error)
	SellerRentOrderList(ctx context.Context, start, end time.Time, status []int) ([]eco.SellerRentOrder, error)
	SellerRentOrderDetail(ctx context.Context, orderNum string) (*eco.SellerRentOrderDetailResult, error)
}

func (l liveECOClient) client() (ecoOrderClient, error) {
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

func (l liveECOClient) SellerRentOrderList(ctx context.Context, start, end time.Time, status []int) ([]eco.SellerRentOrder, error) {
	c, err := l.client()
	if err != nil {
		return nil, err
	}
	return c.SellerRentOrderList(ctx, start, end, status)
}

func (l liveECOClient) SellerRentOrderDetail(ctx context.Context, orderNum string) (*eco.SellerRentOrderDetailResult, error) {
	c, err := l.client()
	if err != nil {
		return nil, err
	}
	return c.SellerRentOrderDetail(ctx, orderNum)
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
				Short:   it.UnitPrice(),
				Long:    it.LongUnitPrice(),
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
	if err := st.InsertAudit(ctx, domain.AuditEntry{Time: time.Now().UTC(), Actor: "system", Action: "bootstrap.admin_password"}); err != nil {
		log.Warn("audit insert failed", "action", "bootstrap.admin_password", "err", err)
	}
	return h, nil
}
