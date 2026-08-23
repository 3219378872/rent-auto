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
	"github.com/3219378872/rent-auto/backend/internal/config"
	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/logging"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

const version = "0.1.0"

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

	srv := api.NewServer(st, auth.NewJWT(cfg.JWTSecret), cfg.AdminUser, version, log)
	srv.PasswordHash = func(context.Context) (string, error) { return hash, nil }

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	log.Info("server listening", "addr", cfg.Addr, "version", version)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		log.Info("shutting down", "signal", sig.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
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
	_ = st.InsertAudit(ctx, newSystemAudit("bootstrap.admin_password"))
	return h, nil
}

func newSystemAudit(action string) domain.AuditEntry {
	return domain.AuditEntry{Time: time.Now().UTC(), Actor: "system", Action: action}
}
