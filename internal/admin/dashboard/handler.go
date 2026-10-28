package dashboard

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/worklist"
	"github.com/koopa0/goen/internal/sales"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("dashboard: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *admin.Control) {
	mux.HandleFunc("GET /admin", ac.RequireStaff(h.Dashboard))
}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	view, err := h.store.Dashboard(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read dashboard", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	health, err := h.store.HealthTasks(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read health tasks for the dashboard", "error", err)
		view.HealthUnavailable = true
	} else {
		view.Tasks = append(view.Tasks, healthTasks(health, now)...)
	}
	recent, err := h.store.RecentSales(r.Context(), WeekDays, now)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read the last seven days for the dashboard", "error", err)
	}
	showWeek(&view, recent, now, err)
	respond.Component(w, r, h.log, http.StatusOK, Dashboard(Meta(r.Context()), view))
}

// healthTasks lists the health desk's tasks as the dashboard shows them. The
// desk reports how long its oldest item has waited, so its start is worked back
// from now.
func healthTasks(tasks []worklist.Task, now time.Time) []Task {
	shown := make([]Task, len(tasks))
	for i, t := range tasks {
		shown[i] = Task{Label: t.Label, Count: t.Count, Href: t.Href, HasAge: t.HasAge, Alert: t.Alert}
		if t.HasAge {
			shown[i].WaitDays = shoptime.DaysAgo(now.Add(-time.Duration(t.AgeSeconds)*time.Second), now)
		}
	}
	return shown
}

// showWeek places the reports desk's figures, read with err at now, on the
// dashboard. A latest paid order that could not be read leaves the week
// standing and says so; any other error leaves the week out, which is not the
// same as a week without orders.
func showWeek(view *View, recent sales.Recent, now time.Time, err error) {
	if err != nil && !errors.Is(err, sales.ErrLatestPaid) {
		view.WeekUnavailable = true
		return
	}
	current := &recent.Current
	view.Week = Week{
		Orders: current.Orders, RevenueCents: current.RevenueCents, RevenueSquares: current.RevenueSquares,
		Previous: recent.Previous,
	}
	view.Daily = chart.Series{Partial: current.Partial, Buckets: make([]chart.Bucket, len(recent.Days))}
	for i, d := range recent.Days {
		view.Daily.Buckets[i] = chart.Bucket{Day: d.Day, Value: d.RevenueCents}
	}
	view.ReadAt = now
	view.Sellers = recent.Sellers
	if l := recent.Latest; l != nil {
		view.Latest = &LatestPaid{Number: l.Number, TotalCents: l.TotalCents, Elapsed: l.Elapsed}
	}
	view.LatestUnavailable = err != nil
}
