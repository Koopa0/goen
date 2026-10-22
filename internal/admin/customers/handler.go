package customers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("customers: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/customers", ac.RequireStaff(h.Search))
	mux.HandleFunc("GET /admin/customers/{id}", ac.RequireStaff(h.Profile))
}

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Search(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get(keyset.Param))
	if err != nil {
		h.log.ErrorContext(r.Context(), "search customers", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	respond.Component(w, r, h.log, http.StatusOK, Customers(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCustomers)}, view))
}

func (h *Handler) Profile(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Profile(r.Context(), r.PathValue("id"))
	switch {
	case err == nil:
		respond.Component(w, r, h.log, http.StatusOK, Customer(
			frame.Page{Title: view.DisplayName()}, &view))
	case errors.Is(err, ErrNotFound):
		respond.Component(w, r, h.log, http.StatusNotFound, frame.MissingRecord(
			frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminMissingCustomer)},
			frame.MissingRecordView{Section: "customers", Heading: i18n.T(r.Context(), i18n.KeyAdminMissingCustomer), Body: i18n.T(r.Context(), i18n.KeyAdminNotFoundBody), BackLabel: i18n.KeyAdminBackCustomers}))
	default:
		h.log.ErrorContext(r.Context(), "read customer", "error", err)
		access.ServerError(w, r, h.log)
	}
}
