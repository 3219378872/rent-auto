package api

import (
	"net/http"
	"strconv"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

func pageParams(r *http.Request) (int, int) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if page < 1 {
		page = 1
	}
	return size, (page - 1) * size
}

func (s *Server) handleInventory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := pageParams(r)
	items, total, err := s.Store.ListInventory(r.Context(), store.InventoryFilter{
		Channel: domain.Channel(q.Get("channel")),
		Status:  q.Get("status"),
		Search:  q.Get("search"),
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": itemsOrEmpty(items), "total": total})
}

func (s *Server) handleListings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := pageParams(r)
	items, total, err := s.Store.ListListings(r.Context(), store.ListingFilter{
		Channel: domain.Channel(q.Get("channel")),
		State:   q.Get("state"),
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": itemsOrEmpty(items), "total": total})
}

func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := pageParams(r)
	items, total, err := s.Store.ListOrders(r.Context(), store.OrderFilter{
		Channel: domain.Channel(q.Get("channel")),
		Status:  q.Get("status"),
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": itemsOrEmpty(items), "total": total})
}

func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListTemplates(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	if items == nil {
		items = []store.Template{}
	}
	writeJSON(w, http.StatusOK, items)
}

func itemsOrEmpty[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}
