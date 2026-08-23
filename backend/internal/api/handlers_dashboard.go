package api

import (
	"context"
	"net/http"

	"github.com/3219378872/rent-auto/backend/internal/analytics"
	"github.com/3219378872/rent-auto/backend/internal/domain"
)

// WalletProvider supplies latest wallet balances per channel (registry-backed).
type WalletProvider func(ctx context.Context) map[domain.Channel]float64

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "store not initialized")
		return
	}
	var wallets map[domain.Channel]float64
	if s.Wallets != nil {
		wallets = s.Wallets(r.Context())
	}
	dash, err := analytics.BuildDashboard(r.Context(), s.Store, wallets)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dash)
}

var _ = domain.ChannelUU
