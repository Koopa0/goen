package dashboard

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/ui/pages/admin"
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
	if health, err := h.store.HealthTasks(r.Context()); err != nil {
		h.log.ErrorContext(r.Context(), "read health tasks for the dashboard", "error", err)
		view.HealthUnavailable = true
	} else {
		view.Tasks = append(view.Tasks, health...)
	}
	if err := h.store.FillWeek(r.Context(), &view, time.Now()); err != nil {
		h.log.ErrorContext(r.Context(), "read the last seven days for the dashboard", "error", err)
		view.WeekUnavailable = !errors.Is(err, admin.ErrLatestPaid)
	}
	web.Render(w, r, h.log, http.StatusOK, admin.Dashboard(admin.Meta(r.Context()), view))
}
