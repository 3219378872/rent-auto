//go:build integration

package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

func TestSessionEpochAtomicAndPasswordTransaction(t *testing.T) {
	st := openReviewStore(t)
	ctx := context.Background()
	if v, err := st.SessionEpoch(ctx); err != nil || v != 0 {
		t.Fatalf("absent epoch: %d %v", v, err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for range 24 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := st.BumpSessionEpoch(ctx); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if v, err := st.SessionEpoch(ctx); err != nil || v != 24 {
		t.Fatalf("atomic increment: %d %v", v, err)
	}
	if err := st.UpsertSettingEnc(ctx, "admin_password_hash", []byte("old-hash")); err != nil {
		t.Fatal(err)
	}
	if err := st.ChangeAdminPassword(ctx, "old-hash", "new-hash"); err != nil {
		t.Fatal(err)
	}
	if v, err := st.SessionEpoch(ctx); err != nil || v != 25 {
		t.Fatalf("password did not revoke: %d %v", v, err)
	}
	if err := st.ChangeAdminPassword(ctx, "old-hash", "wrong-hash"); !errors.Is(err, ErrPasswordChanged) {
		t.Fatalf("CAS: %v", err)
	}
	for _, invalid := range []string{"null", `"0"`, "-1", "1.5", "{}"} {
		if err := st.UpsertSettingPlain(ctx, sessionEpochKey, json.RawMessage(invalid)); err != nil {
			t.Fatal(err)
		}
		if _, err := st.SessionEpoch(ctx); err == nil {
			t.Fatalf("accepted epoch %s", invalid)
		}
		if err := st.ChangeAdminPassword(ctx, "new-hash", "must-rollback"); err == nil {
			t.Fatalf("changed password with invalid epoch %s", invalid)
		}
		if hash, err := st.AdminPasswordHash(ctx); err != nil || hash != "new-hash" {
			t.Fatalf("rollback hash=%s %v", hash, err)
		}
	}
}

func TestInstanceLockDetectsOriginalConnectionLoss(t *testing.T) {
	st := openReviewStore(t)
	ctx := context.Background()
	guard, err := AcquireInstanceLock(ctx, st.Pool)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	if err := guard.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if second, err := AcquireInstanceLock(ctx, st.Pool); err == nil {
		second.Close()
		t.Fatal("second lock acquired while original held")
	}
	pid := guard.conn.Conn().PgConn().PID()
	var killed bool
	if err := st.Pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&killed); err != nil || !killed {
		t.Fatalf("terminate own test lock: %v %v", killed, err)
	}
	if err := guard.Check(ctx); !errors.Is(err, ErrInstanceLockLost) {
		t.Fatalf("lock loss hidden: %v", err)
	}
	replacement, err := AcquireInstanceLock(ctx, st.Pool)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if err := guard.Check(ctx); !errors.Is(err, ErrInstanceLockLost) {
		t.Fatalf("mistook replacement lock for own: %v", err)
	}
	if err := st.Pool.Ping(ctx); err != nil {
		t.Fatalf("other connections must still work: %v", err)
	}
}
