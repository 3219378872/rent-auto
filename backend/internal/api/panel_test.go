package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPanelReadsFailClosedAndRequireAuth(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"execution/status", "price-actions", "templates/catalog", "inventory/categories"} {
		if r := do(t, s.Routes(), "GET", "/api/v1/"+path, "", ""); r.Code != 401 {
			t.Fatalf("%s without session: %d", path, r.Code)
		}
	}
	r := httptest.NewRecorder()
	s.handleExecutionStatus(r, httptest.NewRequest("GET", "/", nil))
	if r.Code != 503 || strings.Contains(r.Body.String(), `"dry_run":false`) {
		t.Fatalf("unknown mode must be unavailable: %d %s", r.Code, r.Body.String())
	}
	r = httptest.NewRecorder()
	s.handleJobsList(r, httptest.NewRequest("GET", "/", nil))
	if r.Code != 503 {
		t.Fatalf("nil scheduler: %d", r.Code)
	}
}

func TestPanelTimeWindowAndHistoricalRedaction(t *testing.T) {
	for _, query := range []string{"since=bad", "until=bad", "since=2026-09-20T00:00:00Z&until=2026-09-19T00:00:00Z", "since=2026-09-19T00:00:00Z&until=2026-09-19T00:00:00Z"} {
		r := httptest.NewRecorder()
		_, _, ok := timeWindow(r, httptest.NewRequest("GET", "/?"+query, nil))
		if ok || r.Code != 400 {
			t.Fatalf("invalid time accepted: %s", query)
		}
	}
	input := map[string]any{"factor": 1.02, "nested": []any{map[string]any{"token": "sensitive-token", "password": "fake-password", "raw": "internal", "note": "Bearer sensitive-token"}}, "key_fp": "abcd1234abcd", "secret_fp": "abcd1234abcd", "token_tail": "Abcd1234", "error": "private detail"}
	b, err := json.Marshal(publicDetail(input))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"sensitive-token", "fake-password", "private detail"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("exposed %s", secret)
		}
	}
	for _, expected := range []string{"1.02", "abcd1234abcd", "Abcd1234"} {
		if !strings.Contains(string(b), expected) {
			t.Fatalf("lost safe value %s", expected)
		}
	}
	if input["error"] != "private detail" {
		t.Fatal("redaction mutated persisted object")
	}
}

type panelJobs struct{ statuses []JobStatus }

func (p panelJobs) StatusList() []JobStatus               { return p.statuses }
func (p panelJobs) Trigger(context.Context, string) error { return nil }

func TestPanelJobErrorsAreCopiedAndRedacted(t *testing.T) {
	s := newTestServer(t)
	jobs := []JobStatus{{Name: "reprice", LastError: "upstream-secret"}}
	s.Jobs = panelJobs{jobs}
	r := httptest.NewRecorder()
	s.handleJobsList(r, httptest.NewRequest("GET", "/", nil))
	if strings.Contains(r.Body.String(), "upstream-secret") || jobs[0].LastError != "upstream-secret" {
		t.Fatal("leaked or mutated task error")
	}
	s.Jobs = panelJobs{}
	r = httptest.NewRecorder()
	s.handleJobsList(r, httptest.NewRequest("GET", "/", nil))
	if strings.TrimSpace(r.Body.String()) != "[]" {
		t.Fatalf("empty task list: %s", r.Body.String())
	}
}
