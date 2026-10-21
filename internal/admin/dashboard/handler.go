package dashboard

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/worklist"
	"github.com/koopa0/goen/internal/sales"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/web"
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

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin", ac.RequireStaff(h.Dashboard))
}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Dashboard(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read dashboard", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	health, err := h.store.HealthTasks(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read health tasks for the dashboard", "error", err)
		view.HealthUnavailable = true
	} else {
		view.Tasks = append(view.Tasks, healthTasks(health)...)
	}
	recent, err := h.store.RecentSales(r.Context(), WeekDays, time.Now())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read the last seven days for the dashboard", "error", err)
	}
	showWeek(&view, recent, err)
	web.Render(w, r, h.log, http.StatusOK, Dashboard(Meta(r.Context()), view))
}

// healthTasks lists the health desk's tasks as the dashboard shows them.
func healthTasks(tasks []worklist.Task) []Task {
	shown := make([]Task, len(tasks))
	for i, t := range tasks {
		shown[i] = Task{
			Label: t.Label, Count: t.Count, Href: t.Href,
			HasAge: t.HasAge, AgeSeconds: t.AgeSeconds, Alert: t.Alert,
		}
	}
	return shown
}

// showWeek places the reports desk's figures, read with err, on the dashboard.
// A latest paid order that could not be read leaves the week standing and says
// so; any other error leaves the week out, which is not the same as a week
// without orders.
func showWeek(view *DashboardView, recent sales.Recent, err error) {
	if err != nil && !errors.Is(err, sales.ErrLatestPaid) {
		view.WeekUnavailable = true
		return
	}
	current, previous := &recent.Current, &recent.Previous
	view.Week = Week{
		Orders: current.Orders, RevenueCents: current.RevenueCents, RevenueSquares: current.RevenueSquares,
		Previous:    recent.Previous,
		RevenueDays: chart.SparklineProps{Previous: daySeries(previous, dayRevenue), Current: daySeries(current, dayRevenue)},
		OrderDays:   chart.SparklineProps{Previous: daySeries(previous, dayOrders), Current: daySeries(current, dayOrders)},
	}
	if l := recent.Latest; l != nil {
		view.Latest = &LatestPaid{Number: l.Number, TotalCents: l.TotalCents, Elapsed: l.Elapsed}
	}
	view.LatestUnavailable = err != nil
}

// daySeries draws one figure of each of the period's days.
func daySeries(p *sales.Period, figure func(sales.Day) int64) chart.Series {
	s := chart.Series{Partial: p.Partial, Buckets: make([]chart.Bucket, 0, len(p.Days))}
	for _, d := range p.Days {
		s.Buckets = append(s.Buckets, chart.Bucket{Day: d.Day, Value: figure(d)})
	}
	return s
}

func dayRevenue(d sales.Day) int64 { return d.RevenueCents }

func dayOrders(d sales.Day) int64 { return d.Orders }
