package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/3219378872/rent-auto/backend/internal/secrets"
)

// ---- stubs ----

type errJobs struct{ msg string }

func (e *errJobs) StatusList() []JobStatus { return nil }
func (e *errJobs) Trigger(context.Context, string) error {
	return errors.New(e.msg)
}

type okJobs struct{}

func (okJobs) StatusList() []JobStatus { return nil }
func (okJobs) Trigger(context.Context, string) error {
	return nil
}

type masterKeyFailChannels struct {
	recordingChannels
}

func (f *masterKeyFailChannels) SetUUToken(context.Context, string) error {
	return errors.New("APP_MASTER_KEY not configured: cannot store credentials safely")
}

type uuErrChannels struct {
	recordingChannels
	msg string
}

func (f *uuErrChannels) VerifyUUSms(context.Context, string, string, string, string) (string, error) {
	return "", errors.New(f.msg)
}

// ---- tests ----

// Direct :8080 connections bypass Caddy: the API must emit baseline headers
// itself (HSTS/CSP stay Caddy-only by design).
func TestSecurityHeadersPresent(t *testing.T) {
	s := newTestServer(t)
	rec := do(t, s.Routes(), "GET", "/api/v1/health", "", "")
	if rec.Code != 200 {
		t.Fatalf("health: %d", rec.Code)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Fatalf("Referrer-Policy = %q", got)
	}
}

// Upstream trigger failures must not echo internals: code stays
// machine-readable, message is generic, detail lives in server logs.
func TestJobTriggerHidesUpstreamText(t *testing.T) {
	jobRunBucket.reset()
	defer jobRunBucket.reset()
	s := newTestServer(t)
	s.Jobs = &errJobs{msg: "pg-connection-refused-db-internal-xyz"}
	h := s.Routes()
	token := loginToken(t, h)

	rec := do(t, h, "POST", "/api/v1/jobs/orders_sync/run", token, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("trigger: want 400, got %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"trigger_failed"`) {
		t.Fatalf("code must stay trigger_failed: %s", body)
	}
	if strings.Contains(body, "pg-connection-refused-db-internal-xyz") {
		t.Fatalf("upstream text leaked to client: %s", body)
	}
}

// Credential writes without APP_MASTER_KEY answer the single unified 500.
func TestMasterKeyMissingUnifiedMessage(t *testing.T) {
	channelWriteBucket.reset()
	defer channelWriteBucket.reset()
	s := newTestServer(t)
	s.Channels = &masterKeyFailChannels{}
	h := s.Routes()
	token := loginToken(t, h)

	rec := do(t, h, "PUT", "/api/v1/channels/uu", token, `{"token":"jwt-abc.def.ghi"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"master_key_missing"`) {
		t.Fatalf("code must be master_key_missing: %s", body)
	}
	if !strings.Contains(body, masterKeyMissingMessage) {
		t.Fatalf("unified message missing: %s", body)
	}
}

func TestIsMasterKeyError(t *testing.T) {
	if !isMasterKeyError(secrets.ErrNoMasterKey) {
		t.Fatal("secrets sentinel must match")
	}
	if !isMasterKeyError(errors.New("APP_MASTER_KEY not configured: cannot store credentials safely")) {
		t.Fatal("registry wording must match")
	}
	if isMasterKeyError(errors.New("connection refused")) {
		t.Fatal("ordinary errors must not match")
	}
	if isMasterKeyError(nil) {
		t.Fatal("nil must not match")
	}
}

// POST /jobs/*/run: 30/10min per IP, 31st is 429.
func TestJobRunRateLimit(t *testing.T) {
	jobRunBucket.reset()
	defer jobRunBucket.reset()
	s := newTestServer(t)
	s.Jobs = okJobs{}
	h := s.Routes()
	token := loginToken(t, h)

	var last int
	for i := 0; i < 31; i++ {
		rec := do(t, h, "POST", "/api/v1/jobs/orders_sync/run", token, "")
		last = rec.Code
		if i < 30 && rec.Code != http.StatusOK {
			t.Fatalf("attempt %d: want 200, got %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("31st attempt: want 429, got %d", last)
	}
}

// PUT /channels/*: 30/10min per IP, 31st is 429.
func TestChannelWriteRateLimit(t *testing.T) {
	channelWriteBucket.reset()
	defer channelWriteBucket.reset()
	s := newTestServer(t)
	rc := &recordingChannels{}
	s.Channels = rc
	h := s.Routes()
	token := loginToken(t, h)

	var last int
	for i := 0; i < 31; i++ {
		rec := do(t, h, "PUT", "/api/v1/channels/uu", token, `{"token":"jwt-abc.def.ghi"}`)
		last = rec.Code
		if i < 30 && rec.Code != http.StatusOK {
			t.Fatalf("attempt %d: want 200, got %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("31st attempt: want 429, got %d", last)
	}
}

// sms-verify consumes the same 10/10min sms budget: 11th attempt is 429.
func TestUUSmsVerifyRateLimited(t *testing.T) {
	s := newTestServer(t)
	s.Channels = &recordingChannels{}
	h := s.Routes()
	token := loginToken(t, h)

	var last int
	for i := 0; i < 11; i++ {
		rec := do(t, h, "POST", "/api/v1/channels/uu/sms-verify", token,
			`{"phone":"13800000000","code":"123456","session_id":"sess-A"}`)
		last = rec.Code
		if i < 10 && rec.Code != http.StatusOK {
			t.Fatalf("attempt %d: want 200, got %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("11th verify: want 429, got %d", last)
	}
}

// sms-verify upstream failures are generic client-side.
func TestUUSmsVerifyHidesUpstreamText(t *testing.T) {
	s := newTestServer(t)
	s.Channels = &uuErrChannels{msg: "upstream-secret-500-detail"}
	h := s.Routes()
	token := loginToken(t, h)

	rec := do(t, h, "POST", "/api/v1/channels/uu/sms-verify", token,
		`{"phone":"13800000000","code":"000000","session_id":"sess-A"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "upstream-secret-500-detail") {
		t.Fatalf("upstream text leaked: %s", rec.Body.String())
	}
}

func TestTruncateAudit(t *testing.T) {
	if got := truncateAudit("abc", 256); got != "abc" {
		t.Fatalf("short text must pass through: %q", got)
	}
	long := strings.Repeat("v", 600)
	if got := truncateAudit(long, 256); len(got) != 256 {
		t.Fatalf("long text must cap at 256, got %d", len(got))
	}
}
