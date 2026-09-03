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

func (l *loginLimiter) reset(key, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
	delete(l.ipFails, ip)
}

// clientIP (trusted-proxy aware) lives on Server; see server.go.

// NOTE (fail-closed revocation, parallel lane owns server.go): the
// requireAuth epoch comparison must NOT fall back to ver=0 when the session
// store is nil/unreadable — that would skip revocation forever. The auth
// package exposes auth.ErrStoreUnavailable / auth.FailClosedError for exactly
// this: wrap the store read error and answer 401. This file intentionally
// does not touch server.go to avoid clashing with that lane.

// writeBucket is a process-wide per-IP fixed-window throttle for expensive
// mutating endpoints (POST /jobs/*/run, PUT /channels/*). It lives here —
// not on Server in server.go — so handlers gain rate limiting without
// touching server.go (parallel lane owns the lock logic there).
type writeBucket struct {
	mu     sync.Mutex
	slots  map[string]*failSlot
	max    int
	window time.Duration
}

func newWriteBucket(limit int, window time.Duration) *writeBucket {
	return &writeBucket{slots: map[string]*failSlot{}, max: limit, window: window}
}

// allow counts the attempt and reports whether it is within budget.
func (b *writeBucket) allow(ip string) bool {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.slots) >= 4096 {
		for k, s := range b.slots {
			if now.Sub(s.start) > b.window {
				delete(b.slots, k)
			}
		}
	}
	sl := b.slots[ip]
	if sl == nil || now.Sub(sl.start) > b.window {
		sl = &failSlot{start: now}
		b.slots[ip] = sl
	}
	if sl.count >= b.max {
		return false
	}
	sl.count++
	return true
}

func (b *writeBucket) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.slots = map[string]*failSlot{}
}

// writeBuckets throttle expensive mutating endpoints per IP: 30 attempts
// per 10min window, one bucket for job runs and one for channel writes.
var jobRunBucket = newWriteBucket(30, 10*time.Minute)
var channelWriteBucket = newWriteBucket(30, 10*time.Minute)

// smsAllow throttles UU sms sends per client IP (10 sends / 10min): the
// upstream UU risk control (84104) and sms fees punish unbounded retries.
func (s *Server) smsAllow(ip string) bool {
	const maxSends = 10
	now := time.Now()
	s.smsMu.Lock()
	defer s.smsMu.Unlock()
	if s.smsFails == nil {
		s.smsFails = map[string]*failSlot{}
	}
	sl := s.smsFails[ip]
	if sl == nil || now.Sub(sl.start) > loginFailWindow {
		sl = &failSlot{start: now}
		s.smsFails[ip] = sl
	}
	if sl.count >= maxSends {
		return false
	}
	sl.count++
	return true
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
	ip := s.clientIP(r)
	key := ip + "|" + req.Username
	if !s.logins.allow(key, ip) {
		s.audit(r, "login.rate_limited", map[string]any{"username": req.Username})
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, retry later")
		return
	}
	hash, err := s.PasswordHash(r.Context())
	if err != nil {
		s.Log.Error("login credential load failed", "err", err)
		s.logins.fail(key, ip)
		s.audit(r, "login.failed", map[string]any{"username": req.Username, "error": "credential_load"})
		// Generic 500: the store failure detail stays server-side; the
		// client sees the same shape as any other internal error so login
		// failures remain indistinguishable (no user enumeration).
		s.internalError(w, err)
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
	s.logins.reset(key, ip)
	tok, exp, err := s.JWT.Sign(req.Username, s.sessionEpoch(r.Context()), s.TTL)
	if err != nil {
		s.internalError(w, err)
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
