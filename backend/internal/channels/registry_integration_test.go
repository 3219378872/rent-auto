//go:build integration

package channels

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/3219378872/rent-auto/backend/internal/secrets"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

// roundTripperTo redirects every request to the mock server base URL
// (same technique as the uu package unit tests).
type redirectTransport struct{ base string }

func (rt redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Host = strings.TrimPrefix(rt.base, "http://")
	r.URL.Scheme = "http"
	return http.DefaultTransport.RoundTrip(r)
}

func openRegistryDB(t *testing.T) (*store.Store, func()) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := store.Open(context.Background(), url)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	st := store.New(pool)
	if _, err := store.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `TRUNCATE app_settings`)
		pool.Close()
	}
	return st, cleanup
}

var masterKey = []byte("0123456789abcdef0123456789abcdef")

func newTestBox(t *testing.T) *secrets.Box {
	t.Helper()
	box, err := secrets.NewBox(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	return box
}

// uuMockServer answers getUserInfo with an OK envelope.
func uuMockServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "getUserInfo") {
			_, _ = w.Write([]byte(`{"Code":0,"Data":{"NickName":"tester","UserId":7}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv, &http.Client{Transport: redirectTransport{base: srv.URL}}
}

// SetUUToken must persist the token encrypted: value_enc set, value_plain NULL.
func TestSetUUTokenStoresEncrypted(t *testing.T) {
	st, done := openRegistryDB(t)
	defer done()
	ctx := context.Background()

	reg := NewRegistry(st, newTestBox(t), slog.Default())
	_, mockHTTP := uuMockServer(t)
	reg.SetUUHTTPClient(mockHTTP)

	if err := reg.SetUUToken(ctx, "token123"); err != nil {
		t.Fatalf("SetUUToken: %v", err)
	}

	s, err := st.GetSetting(ctx, keyUUToken)
	if err != nil {
		t.Fatal(err)
	}
	if s.ValuePlain != nil {
		t.Fatalf("token stored in plaintext: %s", *s.ValuePlain)
	}
	if s.ValueEnc == nil {
		t.Fatal("value_enc missing")
	}
	box := newTestBox(t)
	plain, err := box.Open(string(s.ValueEnc))
	if err != nil {
		t.Fatalf("seal roundtrip: %v", err)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(plain, &payload); err != nil || payload.Token != "token123" {
		t.Fatalf("payload=%s err=%v", plain, err)
	}
	if _, ok := reg.Get("uu"); !ok {
		t.Fatal("uu adapter not registered")
	}
}

// SetUUToken without a master key must refuse to store the credential.
func TestSetUUTokenRefusesWithoutMasterKey(t *testing.T) {
	st, done := openRegistryDB(t)
	defer done()
	ctx := context.Background()

	reg := NewRegistry(st, nil, slog.Default())
	_, mockHTTP := uuMockServer(t)
	reg.SetUUHTTPClient(mockHTTP)

	if err := reg.SetUUToken(ctx, "token123"); err == nil {
		t.Fatal("expected refusal without APP_MASTER_KEY")
	}
	if _, err := st.GetSetting(ctx, keyUUToken); err != store.ErrNotFound {
		t.Fatalf("nothing must be persisted, got %v", err)
	}
}

// Legacy plaintext rows must be migrated to encrypted storage on Refresh.
func TestRefreshMigratesLegacyPlainToken(t *testing.T) {
	st, done := openRegistryDB(t)
	defer done()
	ctx := context.Background()

	if err := st.UpsertSettingPlain(ctx, keyUUToken,
		json.RawMessage(`{"token":"legacytok"}`)); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry(st, newTestBox(t), slog.Default())
	_, mockHTTP := uuMockServer(t)
	reg.SetUUHTTPClient(mockHTTP)

	if err := reg.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if _, ok := reg.Get("uu"); !ok {
		t.Fatal("legacy token did not produce an adapter")
	}
	s, err := st.GetSetting(ctx, keyUUToken)
	if err != nil {
		t.Fatal(err)
	}
	if s.ValuePlain != nil {
		t.Fatalf("legacy plaintext still present: %s", *s.ValuePlain)
	}
	if s.ValueEnc == nil {
		t.Fatal("migrated value_enc missing")
	}
	plain, err := newTestBox(t).Open(string(s.ValueEnc))
	if err != nil || !strings.Contains(string(plain), "legacytok") {
		t.Fatalf("migrated payload mismatch: %s %v", plain, err)
	}
}

// Encrypted rows take precedence over any stale plaintext twin.
func TestRefreshPrefersEncryptedOverLegacy(t *testing.T) {
	st, done := openRegistryDB(t)
	defer done()
	ctx := context.Background()

	box := newTestBox(t)
	b, _ := json.Marshal(map[string]string{"token": "enctok"})
	enc, err := box.Seal(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSettingEnc(ctx, keyUUToken, []byte(enc)); err != nil {
		t.Fatal(err)
	}
	_, _ = st.Pool.Exec(ctx, `UPDATE app_settings SET value_plain=$2 WHERE key=$1`, keyUUToken, `{"token":"plaintok"}`)

	reg := NewRegistry(st, box, slog.Default())
	_, mockHTTP := uuMockServer(t)
	reg.SetUUHTTPClient(mockHTTP)
	if err := reg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if reg.uuClient == nil || reg.uuClient.Token() != "enctok" {
		t.Fatalf("encrypted row must win (got %+v)", reg.uuClient)
	}
}
