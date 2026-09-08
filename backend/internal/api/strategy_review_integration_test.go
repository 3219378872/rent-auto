//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/pricing"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

func TestTemplatesOptionalPaginationFullCatalog(t *testing.T) {
	st, cleanup := openAPIDB(t)
	defer cleanup()
	defer truncateTables(t, st, "templates CASCADE")
	_, err := st.Pool.Exec(context.Background(), `INSERT INTO templates(hash_name)
		SELECT 'API-T-' || lpad(i::text, 3, '0') FROM generate_series(1,205) AS i`)
	if err != nil {
		t.Fatal(err)
	}
	routes := newAuthedServer(t, st).Routes()
	token := tokenFor(t, routes)
	for _, tc := range []struct {
		query string
		count int
		first string
	}{
		{"", 205, "API-T-001"},
		{"?page=2", 205, "API-T-001"},
		{"?page_size=0", 205, "API-T-001"},
		{"?page_size=-1", 205, "API-T-001"},
		{"?page=2&page_size=50", 50, "API-T-051"},
		{"?page=2&page_size=500", 5, "API-T-201"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			rec := do(t, routes, http.MethodGet, "/api/v1/templates"+tc.query, token, "")
			var items []store.Template
			if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil || rec.Code != http.StatusOK {
				t.Fatalf("templates: %d %s (%v)", rec.Code, rec.Body.String(), err)
			}
			if len(items) != tc.count {
				t.Fatalf("items=%d, want %d", len(items), tc.count)
			}
			if items[0].HashName != tc.first {
				t.Fatalf("first=%s, want %s", items[0].HashName, tc.first)
			}
		})
	}
}

func TestConcurrentGlobalAndTemplateValidationCannotCommitInvalidPair(t *testing.T) {
	st, cleanup := openAPIDB(t)
	defer cleanup()
	defer truncateTables(t, st, "strategies, templates CASCADE")
	ctx := context.Background()
	if err := st.UpsertTemplate(ctx, store.Template{HashName: "API-CONCURRENT"}); err != nil {
		t.Fatal(err)
	}
	id, _, err := st.EnsureGlobalStrategy(ctx, `{"factor":{"max":2}}`)
	if err != nil {
		t.Fatal(err)
	}
	routes := newAuthedServer(t, st).Routes()
	token := tokenFor(t, routes)
	for attempt := 0; attempt < 10; attempt++ {
		if _, err := st.Pool.Exec(ctx, `DELETE FROM strategies WHERE scope='template'`); err != nil {
			t.Fatal(err)
		}
		if err := st.UpdateGlobalStrategy(ctx, id, store.StrategyGlobalPatch{Params: []byte(`{"factor":{"max":2}}`)}); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan int, 2)
		for _, req := range []struct{ method, path, body string }{
			{http.MethodPut, "/strategies/global", `{"params":{"factor":{"max":1.25}}}`},
			{http.MethodPost, "/strategies/template", `{"hash_name":"API-CONCURRENT","channel_route":"eco_only","params":{"factor":{"min":1.5}}}`},
		} {
			go func() {
				<-start
				request := httptest.NewRequest(req.method, "/api/v1"+req.path, strings.NewReader(req.body))
				request.Header.Set("Authorization", "Bearer "+token)
				recorder := httptest.NewRecorder()
				routes.ServeHTTP(recorder, request)
				results <- recorder.Code
			}()
		}
		close(start)
		codes := map[int]int{}
		codes[<-results]++
		codes[<-results]++
		if codes[http.StatusOK] != 1 || codes[http.StatusBadRequest] != 1 {
			t.Fatalf("competing edits must commit exactly one valid pair: %v", codes)
		}
		effective, err := st.GetEffectiveStrategy(ctx, "API-CONCURRENT")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pricing.ParseParams(effective.GlobalParams, effective.Params); err != nil {
			t.Fatalf("concurrent writes committed an invalid effective strategy: %v", err)
		}
	}
}

func TestAuditPaginationNormalizesBeforeOffset(t *testing.T) {
	st, cleanup := openAPIDB(t)
	defer cleanup()
	defer truncateTables(t, st, "audit_log")
	_, err := st.Pool.Exec(context.Background(), `INSERT INTO audit_log(ts,actor,action,target)
		SELECT timestamptz '2026-09-08 00:00:00Z' + i * interval '1 second',
		       'system', 'review.pagination', i::text FROM generate_series(1,450) AS i`)
	if err != nil {
		t.Fatal(err)
	}
	routes := newAuthedServer(t, st).Routes()
	token := tokenFor(t, routes)
	for _, tc := range []struct {
		query string
		count int
		first string
		last  string
	}{
		{"page=2", 50, "400", "351"},
		{"page=2&page_size=0", 50, "400", "351"},
		{"page=2&page_size=-1", 50, "400", "351"},
		{"page=2&page_size=500", 200, "250", "51"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			rec := do(t, routes, http.MethodGet, "/api/v1/audit?action=review.pagination&"+tc.query, token, "")
			var page struct {
				Items []domain.AuditEntry `json:"items"`
				Total int                 `json:"total"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || rec.Code != http.StatusOK {
				t.Fatalf("audit: %d %s (%v)", rec.Code, rec.Body.String(), err)
			}
			if page.Total != 450 || len(page.Items) != tc.count || page.Items[0].Target != tc.first || page.Items[len(page.Items)-1].Target != tc.last {
				t.Fatalf("unexpected page: %+v", page)
			}
		})
	}
}

func TestStrategyValidationUsesEffectiveParamsAndRejectsAtomically(t *testing.T) {
	st, cleanup := openAPIDB(t)
	defer cleanup()
	defer truncateTables(t, st, "strategies, templates CASCADE")
	ctx := context.Background()
	if err := st.UpsertTemplate(ctx, store.Template{HashName: "API-STRATEGY"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.EnsureGlobalStrategy(ctx, `{"factor":{"max":2}}`); err != nil {
		t.Fatal(err)
	}
	routes := newAuthedServer(t, st).Routes()
	token := tokenFor(t, routes)
	request := func(method, path, body string, want int) {
		t.Helper()
		rec := do(t, routes, method, "/api/v1"+path, token, body)
		if rec.Code != want {
			t.Fatalf("%s %s: %d %s, want %d", method, path, rec.Code, rec.Body.String(), want)
		}
	}
	request(http.MethodPost, "/strategies/template", `{"hash_name":"API-STRATEGY","channel_route":"eco_only","params":{"factor":{"min":1.5}},"priority":7}`, http.StatusOK)
	es, err := st.GetEffectiveStrategy(ctx, "API-STRATEGY")
	if err != nil {
		t.Fatal(err)
	}
	params, err := pricing.ParseParams(es.GlobalParams, es.Params)
	if err != nil || params.Ctrl.FMin != 1.5 || params.Ctrl.FMax != 2 {
		t.Fatalf("valid inherited range rejected: %+v %v", params.Ctrl, err)
	}
	oldGlobal, oldTemplate := string(es.GlobalParams), string(es.Params)

	for _, body := range []string{
		`{"params":{"baseline":{"topn":"many"}},"channel_route":"uu_only","real_execution_enabled":true}`,
		`{"params":{"factor":{"min":2,"max":1}}}`,
		`{"params":[]}`,
		`{"params":{"factor":{"max":1.25}},"channel_route":"uu_only","real_execution_enabled":true}`,
	} {
		request(http.MethodPut, "/strategies/global", body, http.StatusBadRequest)
	}
	request(http.MethodPost, "/strategies/template", `{"hash_name":"API-STRATEGY","channel_route":"uu_only","params":{"factor":{"min":2.5}},"priority":0,"real_execution_enabled":true}`, http.StatusBadRequest)
	es, err = st.GetEffectiveStrategy(ctx, "API-STRATEGY")
	if err != nil {
		t.Fatal(err)
	}
	var globalRoute string
	var globalReal bool
	var priority int
	if err := st.Pool.QueryRow(ctx, `SELECT channel_route,real_execution_enabled FROM strategies WHERE scope='global'`).Scan(&globalRoute, &globalReal); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT priority FROM strategies WHERE scope='template' AND hash_name='API-STRATEGY'`).Scan(&priority); err != nil {
		t.Fatal(err)
	}
	if string(es.GlobalParams) != oldGlobal || string(es.Params) != oldTemplate || es.Route != "eco_only" || es.RealEnabled || globalRoute != "both" || globalReal || priority != 7 {
		t.Fatalf("invalid write changed strategy: %+v global=%s/%v priority=%d", es, globalRoute, globalReal, priority)
	}
	request(http.MethodPut, "/strategies/global", `{"params":{"factor":{"max":2.2}}}`, http.StatusOK)
	es, err = st.GetEffectiveStrategy(ctx, "API-STRATEGY")
	if err != nil {
		t.Fatal(err)
	}
	params, err = pricing.ParseParams(es.GlobalParams, es.Params)
	if err != nil || params.Ctrl.FMin != 1.5 || params.Ctrl.FMax != 2.2 {
		t.Fatalf("valid global update: %+v %v", params.Ctrl, err)
	}
}
