package api

import (
	"crypto/subtle"
	"encoding/json"
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
	loginIPMaxFails  = 30                                      // per-IP tier: caps global brute force across usernames
	loginDummyPrefix = "$2a$10$disabledaccountdisabledaccount" // never matches a real password
)

// loginLimiter is a fixed-window lockout per client IP + username, with a
// second per-IP-only tier so rotating usernames cannot extend an attack
// indefinitely. Expired slots are swept once the maps grow past thresholds.
type loginLimiter struct {
	mu      sync.Mutex
	fails   map[string]*failSlot // ip|username
	ipFails map[string]*failSlot // ip
}

type failSlot struct {
	count int
	start time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{fails: map[string]*failSlot{}, ipFails: map[string]*failSlot{}}
}

func (l *loginLimiter) slot(m map[string]*failSlot, key string, now time.Time) *failSlot {
	s := m[key]
	if s == nil || now.Sub(s.start) > loginFailWindow {
		s = &failSlot{start: now}
		m[key] = s
	}
	return s
}

// sweepLocked evicts expired slots once a map grows past a threshold. Keys
// embed user-controlled usernames (and spoofable-without-trust IPs), so
// without eviction probing grows the maps without bound.
func (l *loginLimiter) sweepLocked(now time.Time) {
	for _, m := range []map[string]*failSlot{l.fails, l.ipFails} {
		if len(m) < 1024 {
			continue
		}
		for k, s := range m {
			if now.Sub(s.start) > loginFailWindow {
				delete(m, k)
			}
		}
	}
}

// allow reports whether a login attempt may proceed: the (ip,user) bucket and
// the per-IP bucket must both be under their fail limits.
func (l *loginLimiter) allow(key, ip string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now)
	return l.slot(l.fails, key, now).count < loginMaxFails &&
		l.slot(l.ipFails, ip, now).count < loginIPMaxFails
}

func (l *loginLimiter) fail(key, ip string) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.slot(l.fails, key, now).count++
	l.slot(l.ipFails, ip, now).count++
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

// clientIP (trusted-proxy aware) lives on Server; see server.go.

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
	ip := s.clientIP(r)
	key := ip + "|" + req.Username
	if !s.logins.allow(key, ip) {
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
		s.logins.fail(key, ip)
		s.audit(r, "login.failed", map[string]any{"username": req.Username})
		writeErr(w, http.StatusUnauthorized, "unauthorized", "bad credentials")
		return
	}
	s.logins.reset(key)
	tok, exp, err := s.JWT.Sign(req.Username, s.sessionEpoch(r.Context()), s.TTL)
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

// handleLogout revokes every outstanding token by bumping the session epoch
// (ADR-0006). The client discards its local copy regardless; the audit trail
// records who logged out.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	ver := s.bumpSessionEpoch(r.Context())
	s.audit(r, "auth.logout", map[string]any{"epoch": ver})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
