package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/store"
)

func (s *Server) handleExecutionStatus(w http.ResponseWriter, r *http.Request) {
	if s.EnvironmentDryRun == nil || s.Store == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "执行模式暂不可用")
		return
	}
	hash := r.URL.Query().Get("hash_name")
	es, err := s.Store.GetEffectiveStrategy(r.Context(), hash)
	if err != nil {
		s.internalError(w, err)
		return
	}
	reasons := []string{}
	if *s.EnvironmentDryRun {
		reasons = append(reasons, "environment_dry_run")
	}
	if !es.GlobalRealEnabled {
		reasons = append(reasons, "global_disabled")
	}
	if !es.RealEnabled && len(es.Params) > 0 {
		reasons = append(reasons, "template_disabled")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dry_run": len(reasons) > 0, "reasons": reasons, "environment_dry_run": *s.EnvironmentDryRun,
		"global_real_enabled": es.GlobalRealEnabled, "effective_real_enabled": es.RealEnabled,
		"hash_name": hash, "channel_route": es.Route, "checked_at": time.Now().UTC(),
	})
}

// All time-range list endpoints use the same strict [since, until) contract.
func timeWindow(w http.ResponseWriter, r *http.Request) (since, until time.Time, ok bool) {
	var err error
	q := r.URL.Query()
	if v := q.Get("since"); v != "" {
		since, err = time.Parse(time.RFC3339, v)
		if err != nil {
			writeErr(w, 400, "bad_request", "起始时间无效")
			return
		}
	}
	if v := q.Get("until"); v != "" {
		until, err = time.Parse(time.RFC3339, v)
		if err != nil {
			writeErr(w, 400, "bad_request", "结束时间无效")
			return
		}
	}
	if !since.IsZero() && !until.IsZero() && !since.Before(until) {
		writeErr(w, 400, "bad_request", "结束时间必须晚于起始时间")
		return
	}
	return since, until, true
}

func (s *Server) handlePriceActions(w http.ResponseWriter, r *http.Request) {
	since, until, ok := timeWindow(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	mode, result := q.Get("mode"), q.Get("result")
	if mode != "" && mode != "dry_run" && mode != "real" || result != "" && result != "skip" && result != "success" && result != "failed" {
		writeErr(w, 400, "bad_request", "模式或结果过滤无效")
		return
	}
	ids := map[string]int64{}
	for _, key := range []string{"id", "listing_id"} {
		if q.Get(key) == "" {
			continue
		}
		v, err := strconv.ParseInt(q.Get(key), 10, 64)
		if err != nil || v <= 0 {
			writeErr(w, 400, "bad_request", "记录 ID 无效")
			return
		}
		ids[key] = v
	}
	limit, offset := pageParams(r)
	items, total, err := s.Store.ListPriceActions(r.Context(), store.PriceActionFilter{
		Channel: q.Get("channel"), HashName: q.Get("hash_name"), AssetID: q.Get("asset_id"), Action: q.Get("action"),
		Mode: mode, Result: result, ListingID: ids["listing_id"], ID: ids["id"], Since: since, Until: until, Limit: limit, Offset: offset,
	})
	if err != nil {
		s.internalError(w, err)
		return
	}
	for i := range items {
		var detail any
		if json.Unmarshal(items[i].Decision, &detail) == nil {
			items[i].Decision, _ = json.Marshal(publicDetail(detail))
		} else {
			items[i].Decision = json.RawMessage(`{}`)
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total})
}

func (s *Server) handleTemplateCatalog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if v := q.Get("blacklisted"); v != "" && v != "true" && v != "false" {
		writeErr(w, 400, "bad_request", "黑名单过滤无效")
		return
	}
	limit, offset := pageParams(r)
	items, total, err := s.Store.SearchTemplates(r.Context(), q.Get("search"), q.Get("blacklisted"), limit, offset)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total})
}

func (s *Server) handleInventoryCategories(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.InventoryCategories(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, items)
}

var panelSecretValue = regexp.MustCompile(`(?is)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----|\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+|\bBearer\s+\S+`)
var panelFingerprint = regexp.MustCompile(`^[a-f0-9]{12}$`)
var panelTokenTail = regexp.MustCompile(`^[A-Za-z0-9_-]{8}$`)

// Reads must not expose old credential/error payloads merely because the UI
// now makes full details readable. Persisted forensic records are unchanged.
func publicDetail(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			lower := strings.ToLower(key)
			if text, ok := item.(string); ok && ((key == "key_fp" || key == "secret_fp") && panelFingerprint.MatchString(text) || key == "token_tail" && panelTokenTail.MatchString(text)) {
				out[key] = text
				continue
			}
			sensitive := false
			for _, word := range []string{"password", "token", "secret", "private", "credential", "authorization", "cookie", "captcha", "session", "ticket", "randstr", "raw", "payload", "error"} {
				if strings.Contains(lower, word) {
					sensitive = true
					break
				}
			}
			if sensitive {
				out[key] = "[已隐藏敏感或内部信息]"
			} else {
				out[key] = publicDetail(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = publicDetail(item)
		}
		return out
	case string:
		return panelSecretValue.ReplaceAllString(v, "[已隐藏凭证]")
	default:
		return value
	}
}
