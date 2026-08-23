package api

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

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

// internalError logs the real cause server-side and answers with a generic
// message — internal details (DB errors, constraint names) never reach clients.
func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.Log.Error("handler error", "err", err)
	writeErr(w, http.StatusInternalServerError, "internal", "internal error")
}

// ---- login brute-force guard ----

const (
	loginMaxFails    = 5
	loginFailWindow  = 10 * time.Minute
	loginDummyPrefix = "$2a$10$disabledaccountdisabledaccount" // never matches a real password
)

// loginLimiter is a fixed-window lockout per client IP + username.
// After loginMaxFails failed attempts within loginFailWindow, further attempts
// are rejected until the window elapses.
type loginLimiter struct {
	mu    sync.Mutex
	fails map[string]*failSlot
}

type failSlot struct {
	count int
	start time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{fails: map[string]*failSlot{}}
}

func (l *loginLimiter) slot(key string, now time.Time) *failSlot {
	s := l.fails[key]
	if s == nil || now.Sub(s.start) > loginFailWindow {
		s = &failSlot{start: now}
		l.fails[key] = s
	}
	return s
}

// allow reports whether a login attempt may proceed.
func (l *loginLimiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.slot(key, now).count < loginMaxFails
}

func (l *loginLimiter) fail(key string) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.slot(key, now).count++
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

// clientIP prefers the reverse-proxy-set X-Real-IP (Caddy overwrites it),
// falling back to the transport peer address.
func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// dummyHash burns the same bcrypt cost as a real check when the username is
// wrong, keeping response timing uniform (no user enumeration).
func (s *Server) dummyHash() string {
	s.dummyOnce.Do(func() {
		if h, err := auth.HashPassword(loginDummyPrefix); err == nil {
			s.dummyHashVal = h
		}
	})
	return s.dummyHashVal
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	key := clientIP(r) + "|" + req.Username
	if !s.logins.allow(key) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, retry later")
		return
	}
	hash, err := s.PasswordHash(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "load credentials")
		return
	}
	userOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(s.AdminUser)) == 1
	checkHash := hash
	if !userOK {
		checkHash = s.dummyHash()
	}
	pwOK := auth.CheckPassword(checkHash, req.Password)
	if !userOK || !pwOK {
		s.logins.fail(key)
		s.audit(r, "login.failed", map[string]any{"username": req.Username})
		writeErr(w, http.StatusUnauthorized, "unauthorized", "bad credentials")
		return
	}
	s.logins.reset(key)
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
