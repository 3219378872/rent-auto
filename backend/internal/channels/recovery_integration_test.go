//go:build integration

package channels

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
)

func TestRegistryRecoversOnlyAfterValidatedRetry(t *testing.T) {
	st, done := openRegistryDB(t)
	defer done()
	ctx := context.Background()
	box := newTestBox(t)
	enc, err := box.Seal([]byte(`{"token":"stored-token"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSettingEnc(ctx, keyUUToken, []byte(enc)); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(st, box, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Now()
	r.now = func() time.Time { return now }
	var available atomic.Bool
	var revoked atomic.Bool
	var calls atomic.Int64
	r.SetUUHTTPClient(&http.Client{Transport: reliabilityTransport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if !available.Load() {
			return nil, context.DeadlineExceeded
		}
		body := `{"Code":0,"Data":{"UserId":7,"NickName":"mock"}}`
		if revoked.Load() {
			body = `{"Code":84101}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})})
	if err := r.Refresh(ctx); err == nil {
		t.Fatal("expected initialization failure")
	}
	if _, ok := r.Get(domain.ChannelUU); ok {
		t.Fatal("unvalidated adapter installed")
	}
	if r.Health(ctx)["uu"] == "not_configured" {
		t.Fatal("stored configuration must remain observable")
	}
	available.Store(true)
	if err := r.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("recovery ignored backoff")
	}
	now = now.Add(time.Minute)
	if err := r.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	old, ok := r.Get(domain.ChannelUU)
	if !ok || calls.Load() != 2 {
		t.Fatal("validated retry did not install adapter")
	}
	available.Store(false)
	if err := r.Refresh(ctx); err == nil {
		t.Fatal("expected transient validation failure")
	}
	if current, ok := r.Get(domain.ChannelUU); !ok || current != old {
		t.Fatal("transient failure discarded verified current adapter")
	}
	if err := r.SetUUToken(ctx, "new-unvalidated-token"); err == nil {
		t.Fatal("unvalidated credential accepted")
	}
	if current, _ := r.Get(domain.ChannelUU); current != old {
		t.Fatal("failed replacement displaced old adapter")
	}
	available.Store(true)
	revoked.Store(true)
	if err := r.Refresh(ctx); !errors.Is(err, platform.ErrAuthExpired) {
		t.Fatalf("revocation must retain the auth sentinel: %v", err)
	}
	if _, ok := r.Get(domain.ChannelUU); ok {
		t.Fatal("revoked credential still available")
	}
	revoked.Store(false)
	if err := r.SetUUToken(ctx, "verified-again"); err != nil {
		t.Fatal(err)
	}
	changed, err := box.Seal([]byte(`{"token":"changed-outside-registry"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSettingEnc(ctx, keyUUToken, []byte(changed)); err != nil {
		t.Fatal(err)
	}
	available.Store(false)
	if err := r.Refresh(ctx); err == nil {
		t.Fatal("unvalidated changed credential accepted")
	}
	if _, ok := r.Get(domain.ChannelUU); ok {
		t.Fatal("old account remained active after stored credential changed")
	}
}

func TestECOCredentialUpdateDoesNotRevalidateUU(t *testing.T) {
	st, done := openRegistryDB(t)
	defer done()
	r := NewRegistry(st, newTestBox(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, hc := uuMockServer(t)
	r.SetUUHTTPClient(hc)
	ctx := context.Background()
	if err := r.SetUUToken(ctx, "uu-verified"); err != nil {
		t.Fatal(err)
	}
	old, _ := r.Get(domain.ChannelUU)
	var calls atomic.Int64
	r.SetUUHTTPClient(&http.Client{Transport: reliabilityTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, context.DeadlineExceeded
	})})
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := r.SetECOCreds(ctx, "partner", string(keyPEM), "steam-id"); err != nil {
		t.Fatal(err)
	}
	if current, ok := r.Get(domain.ChannelUU); !ok || current != old || calls.Load() != 0 {
		t.Fatalf("ECO change affected UU: current=%v calls=%d", ok, calls.Load())
	}
	if _, ok := r.Get(domain.ChannelECO); !ok {
		t.Fatal("ECO credential was not installed")
	}
}

func TestRegistryConcurrentRefreshAndCredentialUpdate(t *testing.T) {
	st, done := openRegistryDB(t)
	defer done()
	r := NewRegistry(st, newTestBox(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, hc := uuMockServer(t)
	r.SetUUHTTPClient(hc)
	ctx := context.Background()
	if err := r.SetUUToken(ctx, "initial"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if err := r.Refresh(ctx); err != nil {
					t.Error(err)
				}
				if err := r.SetUUToken(ctx, "updated"); err != nil {
					t.Error(err)
				}
				if err := r.Recover(ctx); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	setting, err := st.GetSetting(ctx, keyUUToken)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := r.box.Open(string(setting.ValueEnc))
	if err != nil {
		t.Fatal(err)
	}
	var persisted struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(plain, &persisted); err != nil {
		t.Fatal(err)
	}
	if r.uuClient.Token() != persisted.Token {
		t.Fatal("stale construction replaced newest credentials")
	}
}
