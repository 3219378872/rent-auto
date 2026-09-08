//go:build integration

package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/auth"
	"github.com/3219378872/rent-auto/backend/internal/config"
	"github.com/3219378872/rent-auto/backend/internal/store"
	"github.com/3219378872/rent-auto/backend/internal/testutil"
)

func TestGuardedJobCancelsDetachedManualRun(t *testing.T) {
	ctx := context.Background()
	pool, err := store.Open(ctx, testutil.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	guard, err := store.AcquireInstanceLock(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	root, cancel := context.WithCancel(ctx)
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	job := guardedJob(root, guard, func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() })
	go func() { done <- job(context.Background()) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("manual job escaped root cancellation")
	}
	called := false
	if err := guardedJob(root, guard, func(context.Context) error { called = true; return nil })(ctx); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("called after shutdown: %v %v", called, err)
	}
	guard.Close()
	if err := guardedJob(ctx, guard, func(context.Context) error { called = true; return nil })(ctx); !errors.Is(err, store.ErrInstanceLockLost) || called {
		t.Fatalf("called after lock loss: %v %v", called, err)
	}
}

func TestBootstrapPasswordNeverEntersLogs(t *testing.T) {
	ctx := context.Background()
	pool, err := store.Open(ctx, testutil.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := store.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE app_settings,audit_log`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := pool.Exec(ctx, `TRUNCATE app_settings,audit_log`); err != nil {
			t.Error(err)
		}
	}()
	st := store.New(pool)
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	cfg := &config.Config{AdminBootstrapFile: filepath.Join(t.TempDir(), "password")}
	hash, err := resolveAdminPassword(ctx, st, cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfg.AdminBootstrapFile)
	if err != nil {
		t.Fatal(err)
	}
	password := strings.TrimSpace(string(data))
	if !auth.CheckPassword(hash, password) {
		t.Fatal("file does not contain usable initial password")
	}
	if strings.Contains(logs.String(), password) || strings.Contains(logs.String(), hash) {
		t.Fatal("bootstrap secret entered logs")
	}
	again, err := resolveAdminPassword(ctx, st, cfg, log)
	if err != nil || again != hash {
		t.Fatalf("restart attempted bootstrap again: %v", err)
	}
	// Simulate a crash between publishing the restricted file and persisting
	// its hash. Recovery must use the same password without replacing the file.
	if _, err := pool.Exec(ctx, `DELETE FROM app_settings WHERE key='admin_password_hash'`); err != nil {
		t.Fatal(err)
	}
	recovered, err := resolveAdminPassword(ctx, st, cfg, log)
	if err != nil || !auth.CheckPassword(recovered, password) {
		t.Fatalf("bootstrap handoff recovery: %v", err)
	}
}
