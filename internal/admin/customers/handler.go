package customers

import (
	"errors"
	"log/slog"
	"net/http"

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
		panic("customers: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/warranty", ac.RequireStaff(h.Warranties))
	mux.HandleFunc("GET /admin/customers", ac.RequireStaff(h.Search))
	mux.HandleFunc("GET /admin/customers/{id}", ac.RequireStaff(h.Profile))
}

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Search(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "search customers", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, admin.Customers(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCustomers)}, view))
}

func (h *Handler) Warranties(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Warranties(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "search warranties", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, admin.Warranties(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageWarranty)}, view))
}

func (h *Handler) Profile(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Profile(r.Context(), r.PathValue("id"))
	switch {
	case err == nil:
		web.Render(w, r, h.log, http.StatusOK, admin.Customer(
			layouts.Page{Title: view.DisplayName()}, &view))
	case errors.Is(err, ErrNotFound):
		web.Render(w, r, h.log, http.StatusNotFound, admin.MissingRecord(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminMissingCustomer)},
			admin.MissingRecordView{Section: "customers", Heading: i18n.T(r.Context(), i18n.KeyAdminMissingCustomer), Body: i18n.T(r.Context(), i18n.KeyAdminNotFoundBody), BackLabel: i18n.KeyAdminBackCustomers}))
	default:
		h.log.ErrorContext(r.Context(), "read customer", "error", err)
		access.ServerError(w, r, h.log)
	}
}
