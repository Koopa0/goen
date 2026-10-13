// Package reports is the back office's sales report: figures for a fixed
// window of days, read from the committed orders.
package reports

import (
	"context"
	"errors"
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
	mux.HandleFunc("GET /admin/reports/orders.csv", ac.RequireStaff(h.ordersCSV))
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	// A parse failure is zero, which the store's allowlist turns into the
	// default — the same answer an out-of-range number gets.
	days, parseErr := strconv.ParseInt(r.URL.Query().Get("days"), 10, 32)
	if parseErr != nil {
		days = 0
	}
	view, err := h.store.ReportAt(r.Context(), int32(days), time.Now())
	if errors.Is(err, ErrDailyRevenue) {
		h.log.ErrorContext(r.Context(), "read daily revenue", "error", err)
		err = nil
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "read report", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	if view.ReturnedErr != nil && !errors.Is(view.ReturnedErr, context.Canceled) {
		h.log.ErrorContext(r.Context(), "read returned products", "error", view.ReturnedErr)
	}
	web.Render(w, r, h.log, http.StatusOK, admin.Report(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageReports)}, &view))
}
