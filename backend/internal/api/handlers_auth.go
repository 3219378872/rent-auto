package api

import (
	"encoding/json"
	"net/http"

	"github.com/3219378872/rent-auto/backend/internal/auth"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errBody{Code: code, Message: msg})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	hash, err := s.PasswordHash(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "load credentials")
		return
	}
	if req.Username != s.AdminUser || !auth.CheckPassword(hash, req.Password) {
		s.audit(r, "login.failed", map[string]any{"username": req.Username})
		writeErr(w, http.StatusUnauthorized, "unauthorized", "bad credentials")
		return
	}
	tok, exp, err := s.JWT.Sign(req.Username, s.TTL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "sign token")
		return
	}
	s.audit(r, "login.success", map[string]any{"username": req.Username})
	writeJSON(w, http.StatusOK, loginResponse{Token: tok, ExpiresAt: exp.UTC().Format(timeRFC3339Milli)})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.Version})
}
