// Package reports is the back office's sales report: figures for a fixed
// window of days, read from the committed orders.
package reports

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("reports: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/reports", ac.RequireStaff(h.Page))
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	// A parse failure is zero, which the store's allowlist turns into the
	// default — the same answer an out-of-range number gets.
	days, parseErr := strconv.ParseInt(r.URL.Query().Get("days"), 10, 32)
	if parseErr != nil {
		days = 0
	}
	now := time.Now()
	view, err := h.store.ReportAt(r.Context(), int32(days), now)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read report", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	// The chart is one figure of the page: failing to read it must not take the
	// tiles with it.
	view.Daily, err = h.store.DailyRevenue(r.Context(), int32(days), now)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read daily revenue", "error", err)
		view.DailyUnavailable = true
	}
	web.Render(w, r, h.log, http.StatusOK, admin.Report(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageReports)}, &view))
}
