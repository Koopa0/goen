package warranty

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Handler serves warranty registration.
type Handler struct {
	store *Store
	log   *slog.Logger
}

// NewHandler returns a Handler over store.
func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("warranty: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

// List serves GET /account/warranty.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	u, ok := account.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin?next=/account/warranty", http.StatusSeeOther)
		return
	}
	rows, err := h.store.Mine(r.Context(), u.ID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read warranties", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.WarrantyList(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyWarrantyTitle)},
		pages.WarrantyListView{Rows: rows, Notice: noticeFor(r)}))
}

// Order serves GET /account/warranty/{number}.
func (h *Handler) Order(w http.ResponseWriter, r *http.Request) {
	u, ok := account.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	number := r.PathValue("number")
	view, err := h.store.Registrable(r.Context(), number, u.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// The same answer for "not yours" and "does not exist". Telling
			// them apart is what somebody probing order numbers wants.
			web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
				layouts.Page{Title: i18n.T(r.Context(), i18n.KeyOrderNotFound)}, "404",
				i18n.T(r.Context(), i18n.KeyOrderNotFound),
				i18n.T(r.Context(), i18n.KeyWarrantyOrderNotFound)))
			return
		}
		h.log.ErrorContext(r.Context(), "read registrable lines", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.WarrantyOrder(
		layouts.Page{Title: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyWarrantyRegisterMeta), number)}, view))
}

// Register serves POST /account/warranty/{number}.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	u, ok := account.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	unit, err := strconv.Atoi(r.PostFormValue("unit"))
	if err != nil {
		unit = 0
	}

	// PathEscape, so an order number carrying anything odd cannot start a new
	// path segment or a query. It is the route's own path value, but escaping
	// it costs nothing and removes the question.
	back := "/account/warranty/" + url.PathEscape(number)
	switch err := h.store.Register(r.Context(), r.PostFormValue("line"), u.ID,
		r.PostFormValue("serial"), unit); {
	case err == nil:
		http.Redirect(w, r, back+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrSerialTaken):
		http.Redirect(w, r, back+"?serial=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotRegistrable), errors.Is(err, ErrInvalid),
		errors.Is(err, ErrNotFound):
		http.Redirect(w, r, back+"?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "register warranty", "error", err)
		h.serverError(w, r)
	}
}

// serverError renders the 500 page.
func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyTryAgainBody)))
}

// noticeFor turns a query flag into a sentence.
func noticeFor(r *http.Request) string {
	ctx := r.Context()
	switch {
	case r.URL.Query().Get("ok") == "1":
		return i18n.T(ctx, i18n.KeyWarrantyAlready)
	case r.URL.Query().Get("serial") == "1":
		return i18n.T(ctx, i18n.KeyWarrantyDuplicateSerial)
	case r.URL.Query().Get("refused") == "1":
		return i18n.T(ctx, i18n.KeyWarrantyRefused)
	default:
		return ""
	}
}
