package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type failedEpochStore struct{ readErr, writeErr error }

func (f failedEpochStore) SessionEpoch(context.Context) (int64, error)     { return 0, f.readErr }
func (f failedEpochStore) BumpSessionEpoch(context.Context) (int64, error) { return 0, f.writeErr }

func TestSessionBackendFailuresAreClosed(t *testing.T) {
	s := newTestServer(t)
	token, _, err := s.JWT.Sign("admin", 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	h := s.Routes()
	for _, epochs := range []EpochStore{nil, failedEpochStore{readErr: errors.New("database offline")}} {
		s.Epochs = epochs
		if r := do(t, h, "GET", "/api/v1/auth/me", token, ""); r.Code != 401 {
			t.Fatalf("unreadable store authorized: %d", r.Code)
		}
		if r := do(t, h, "POST", "/api/v1/auth/login", "", `{"username":"admin","password":"hunter2"}`); r.Code != 500 {
			t.Fatalf("unreadable store signed: %d", r.Code)
		}
	}
	s.Epochs = failedEpochStore{writeErr: errors.New("database read only")}
	if r := do(t, h, "POST", "/api/v1/auth/logout", token, ""); r.Code != 500 {
		t.Fatalf("failed revocation reported success: %d", r.Code)
	}
}

func TestLoginCannotEscapeConcurrentPasswordRevocation(t *testing.T) {
	s := newTestServer(t)
	load := s.PasswordHash
	s.PasswordHash = func(ctx context.Context) (string, error) {
		old, err := load(ctx)
		if _, bumpErr := s.Epochs.BumpSessionEpoch(ctx); bumpErr != nil {
			return "", bumpErr
		}
		return old, err
	}
	h := s.Routes()
	r := do(t, h, "POST", "/api/v1/auth/login", "", `{"username":"admin","password":"hunter2"}`)
	if r.Code != 200 {
		t.Fatalf("login: %d %s", r.Code, r.Body.String())
	}
	var response loginResponse
	if err := json.Unmarshal(r.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if r := do(t, h, "GET", "/api/v1/auth/me", response.Token, ""); r.Code != 401 {
		t.Fatalf("old password obtained fresh epoch: %d", r.Code)
	}
}
