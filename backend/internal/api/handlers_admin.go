package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

// ---- inventory cost ----

type costRequest struct {
	Cost float64 `json:"cost"`
}

func (s *Server) handleSetCost(w http.ResponseWriter, r *http.Request) {
	channel := domain.Channel(r.PathValue("channel"))
	assetID := r.PathValue("asset_id")
	if !channel.Valid() {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid channel")
		return
	}
	var req costRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Cost < 0 {
		writeErr(w, http.StatusBadRequest, "bad_request", "cost must be >= 0")
		return
	}
	if err := s.Store.SetCostBasis(r.Context(), channel, assetID, req.Cost); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "not_found", "inventory item missing")
			return
		}
		s.internalError(w, err)
		return
	}
	s.audit(r, "inventory.cost_update", map[string]any{
		"channel": string(channel), "target": assetID, "cost": req.Cost,
	})
	writeJSON(w, http.StatusOK, map[string]float64{"cost": req.Cost})
}

// ---- strategies ----

type strategyView struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Scope       string          `json:"scope"`
	Route       string          `json:"channel_route"`
	Params      json.RawMessage `json:"params"`
	RealEnabled bool            `json:"real_execution_enabled"`
	Priority    int             `json:"priority"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func (s *Server) handleStrategiesList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(),
		`SELECT id, name, scope::text, COALESCE(hash_name,''), channel_route, params::text,
		        real_execution_enabled, priority, updated_at
		 FROM strategies ORDER BY scope DESC, priority DESC, id`)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	out := []strategyView{}
	for rows.Next() {
		var v strategyView
		var paramsText string
		var hashName string
		if err := rows.Scan(&v.ID, &v.Name, &v.Scope, &hashName, &v.Route,
			&paramsText, &v.RealEnabled, &v.Priority, &v.UpdatedAt); err != nil {
			s.internalError(w, err)
			return
		}
		v.Params = json.RawMessage(paramsText)
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

type strategyUpdate struct {
	Params      *json.RawMessage `json:"params,omitempty"`
	Route       *string          `json:"channel_route,omitempty"`
	RealEnabled *bool            `json:"real_execution_enabled,omitempty"`
}

var validRoutes = map[string]bool{"uu_only": true, "eco_only": true, "both": true, "uu_primary_eco_fallback": true}

func (s *Server) handleStrategyUpdateGlobal(w http.ResponseWriter, r *http.Request) {
	var req strategyUpdate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	if req.Route != nil && !validRoutes[*req.Route] {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid channel_route")
		return
	}
	ctx := r.Context()
	id, _, err := s.Store.EnsureGlobalStrategy(ctx, "{}")
	if err != nil {
		s.internalError(w, err)
		return
	}
	if req.Params != nil && !json.Valid(*req.Params) {
		writeErr(w, http.StatusBadRequest, "bad_request", "params must be valid json")
		return
	}
	patch := store.StrategyGlobalPatch{}
	detail := map[string]any{"target": strconv.FormatInt(id, 10)}
	if req.Params != nil {
		patch.Params = []byte(*req.Params)
	}
	if req.Route != nil {
		patch.Route = req.Route
		detail["channel_route"] = *req.Route
	}
	if req.RealEnabled != nil {
		patch.RealEnabled = req.RealEnabled
		detail["real_execution_enabled"] = *req.RealEnabled
	}
	// one transaction: params/route/real_execution_enabled drive live repricing
	// together — a partial update (e.g. new params but old real flag) is worse
	// than a rejected one.
	if err := s.Store.UpdateGlobalStrategy(ctx, id, patch); err != nil {
		s.internalError(w, err)
		return
	}
	s.audit(r, "strategy.update_global", detail)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- audit ----

func (s *Server) handleAuditList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("page_size"))
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	parseTS := func(k string) time.Time {
		if v := q.Get(k); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				return t
			}
		}
		return time.Time{}
	}
	items, total, err := s.Store.ListAudit(r.Context(), store.AuditFilter{
		Action:  q.Get("action"),
		Channel: q.Get("channel"),
		Since:   parseTS("since"),
		Before:  parseTS("until"),
		Limit:   limit,
		Offset:  (page - 1) * limit,
	})
	if err != nil {
		s.internalError(w, err)
		return
	}
	if items == nil {
		items = []domain.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

// ---- template blacklist management ----

type blacklistRequest struct {
	HashName    string `json:"hash_name"`
	Blacklisted bool   `json:"blacklisted"`
}

func (s *Server) handleTemplateBlacklist(w http.ResponseWriter, r *http.Request) {
	var req blacklistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.HashName == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "hash_name required")
		return
	}
	if err := s.Store.SetTemplateBlacklist(r.Context(), req.HashName, req.Blacklisted); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "unknown template")
			return
		}
		s.internalError(w, err)
		return
	}
	s.audit(r, "template.blacklist", map[string]any{
		"hash": req.HashName, "blacklisted": req.Blacklisted,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
