// Package api wires HTTP routing, middleware and handlers.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/auth"
	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

const timeRFC3339Milli = "2006-01-02T15:04:05.000Z07:00"

type Server struct {
	Store     *store.Store
	JWT       *auth.JWT
	TTL       time.Duration
	AdminUser string
	Version   string
	Log       *slog.Logger
	// PasswordHash resolves the current admin bcrypt hash (env- or DB-backed).
	PasswordHash func(ctx context.Context) (string, error)
}

func NewServer(st *store.Store, jwt *auth.JWT, adminUser, version string, log *slog.Logger) *Server {
	return &Server{Store: st, JWT: jwt, TTL: 24 * time.Hour, AdminUser: adminUser, Version: version, Log: log}
}

// Routes builds the handler tree:
//   - exact public endpoints: /api/v1/health, /api/v1/auth/login
//   - everything under /api/v1/ requires a valid JWT
func (s *Server) Routes() http.Handler {
	protected := http.NewServeMux()
	protected.HandleFunc("GET /api/v1/auth/me", s.handleMe)
	protected.HandleFunc("GET /api/v1/inventory", s.handleInventory)
	protected.HandleFunc("GET /api/v1/listings", s.handleListings)
	protected.HandleFunc("GET /api/v1/orders", s.handleOrders)
	protected.HandleFunc("GET /api/v1/templates", s.handleTemplates)

	root := http.NewServeMux()
	root.HandleFunc("GET /api/v1/health", s.handleHealth)
	root.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	root.Handle("/api/v1/", s.requireAuth(protected))
	root.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, http.StatusNotFound, "not_found", "unknown endpoint")
	})
	return withRecover(s.Log)(withRequestLog(s.Log)(root))
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"username": userFrom(r.Context())})
}

// ---- helpers shared by handlers ----

func userFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxUser).(string)
	return v
}

func auditEntry(r *http.Request, action string, detail map[string]any) domain.AuditEntry {
	return domain.AuditEntry{
		Time:   time.Now().UTC(),
		Actor:  "user:" + userFrom(r.Context()),
		Action: action,
		Detail: detail,
	}
}

// audit writes an entry best-effort; nil store (tests) and write errors are non-fatal.
func (s *Server) audit(r *http.Request, action string, detail map[string]any) {
	if s.Store == nil {
		return
	}
	if err := s.Store.InsertAudit(r.Context(), auditEntry(r, action, detail)); err != nil {
		s.Log.Warn("audit insert failed", "action", action, "err", err)
	}
}

// ---- middleware ----

type ctxKeyType int

const ctxUser ctxKeyType = 1

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		tok, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || tok == "" {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		claims, err := s.JWT.Verify(tok)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid token")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, claims.Sub)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func withRecover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered", "path", r.URL.Path, "panic", rec)
					writeErr(w, http.StatusInternalServerError, "internal", "panic")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func withRequestLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, r)
			log.Debug("http", "method", r.Method, "path", r.URL.Path, "dur_ms", time.Since(start).Milliseconds())
		})
	}
}
