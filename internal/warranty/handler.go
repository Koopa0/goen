package warranty

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/koopa0/goen/internal/formtext"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("warranty: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

// Routes registers the customer's warranty list and registering an order's
// cover. requireUser sends a signed-out visitor to sign in and back; it is the
// caller's, because signing in is not this package's.
func (h *Handler) Routes(mux *http.ServeMux, requireUser func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /account/warranty", requireUser(h.List))
	mux.HandleFunc("GET /account/warranty/{number}", requireUser(h.Order))
	mux.HandleFunc("POST /account/warranty/{number}", requireUser(h.Register))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
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
	ready, err := h.store.Ready(r.Context(), u.ID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read ready warranties", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, WarrantyList(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyWarrantyTitle)},
		WarrantyListView{Rows: rows, Ready: ready}))
}

func (h *Handler) Order(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	view, ok := h.orderView(w, r, u.ID)
	if !ok {
		return
	}
	view.RegisteredUnit = registeredUnitFor(r, view.Registered)
	web.Render(w, r, h.log, http.StatusOK, WarrantyOrder(
		layouts.Page{Title: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyWarrantyRegisterMeta), view.Number)}, view))
}

func (h *Handler) orderView(w http.ResponseWriter, r *http.Request, userID string) (OrderView, bool) {
	view, err := h.store.Registrable(r.Context(), r.PathValue("number"), userID)
	if err == nil {
		return view, true
	}
	if errors.Is(err, ErrNotFound) {
		web.Render(w, r, h.log, http.StatusNotFound, layouts.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyOrderNotFound)}, "404",
			i18n.T(r.Context(), i18n.KeyOrderNotFound),
			i18n.T(r.Context(), i18n.KeyWarrantyOrderNotFound)))
	} else {
		h.log.ErrorContext(r.Context(), "read registrable lines", "error", err)
		h.serverError(w, r)
	}
	return OrderView{}, false
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	unit, err := strconv.Atoi(r.PostFormValue("unit"))
	if err != nil {
		unit = 0
	}

	back := "/account/warranty/" + url.PathEscape(number)
	switch err := h.store.Register(r.Context(), number, r.PostFormValue("line"), u.ID,
		r.PostFormValue("serial"), unit); {
	case err == nil:
		query := url.Values{"ok": {"1"}, "line": {r.PostFormValue("line")}, "unit": {strconv.Itoa(unit)}}
		http.Redirect(w, r, back+"?"+query.Encode(), http.StatusSeeOther)
	case errors.Is(err, ErrSerialTaken):
		h.rejectRegistration(w, r, u.ID, i18n.KeyWarrantyDuplicateSerial)
	case errors.Is(err, ErrSerialTooLong):
		h.rejectRegistration(w, r, u.ID, i18n.KeyWarrantySerialTooLong)
	case errors.Is(err, ErrInvalid):
		h.rejectRegistration(w, r, u.ID, i18n.KeyWarrantyRefused)
	case errors.Is(err, ErrNotRegistrable), errors.Is(err, ErrNotFound):
		h.rejectRegistration(w, r, u.ID, i18n.KeyWarrantyRefused)
	default:
		h.log.ErrorContext(r.Context(), "register warranty", "error", err)
		h.serverError(w, r)
	}
}

func (h *Handler) rejectRegistration(w http.ResponseWriter, r *http.Request, userID string, refusal i18n.Key) {
	view, ok := h.orderView(w, r, userID)
	if !ok {
		return
	}
	message := i18n.T(r.Context(), refusal)
	if refusal == i18n.KeyWarrantySerialTooLong {
		message = fmt.Sprintf(message, MaxSerialRunes)
	}
	view.Refusal = message
	for i := range view.Lines {
		line := &view.Lines[i]
		if line.ID != r.PostFormValue("line") {
			continue
		}
		line.DraftSerial = r.PostFormValue("serial")
		if line.Registrable() && refusal != i18n.KeyWarrantyRefused {
			line.SerialRefusal = message
			view.Refusal = ""
		}
		break
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, WarrantyOrder(
		layouts.Page{Title: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyWarrantyRegisterMeta), view.Number)}, view))
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyTryAgainBody)))
}

func registeredUnitFor(r *http.Request, rows []Warranty) *Warranty {
	if r.URL.Query().Get("ok") != "1" {
		return nil
	}
	unit, err := strconv.Atoi(r.URL.Query().Get("unit"))
	if err != nil || unit < 1 || unit > maxUnits {
		return nil
	}
	for i := range rows {
		if rows[i].LineID == r.URL.Query().Get("line") && rows[i].Unit == unit && !rows[i].Returned {
			return &rows[i]
		}
	}
	return nil
}
