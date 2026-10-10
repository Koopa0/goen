package warranty

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
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
	web.Render(w, r, h.log, http.StatusOK, pages.WarrantyList(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyWarrantyTitle)},
		pages.WarrantyListView{Rows: rows, Notice: noticeFor(r)}))
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
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.WarrantyOrder(
		layouts.Page{Title: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyWarrantyRegisterMeta), view.Number)}, view))
}

func (h *Handler) orderView(w http.ResponseWriter, r *http.Request, userID string) (pages.WarrantyOrderView, bool) {
	view, err := h.store.Registrable(r.Context(), r.PathValue("number"), userID)
	if err == nil {
		return view, true
	}
	if errors.Is(err, ErrNotFound) {
		web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyOrderNotFound)}, "404",
			i18n.T(r.Context(), i18n.KeyOrderNotFound),
			i18n.T(r.Context(), i18n.KeyWarrantyOrderNotFound)))
	} else {
		h.log.ErrorContext(r.Context(), "read registrable lines", "error", err)
		h.serverError(w, r)
	}
	return pages.WarrantyOrderView{}, false
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
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

	back := "/account/warranty/" + url.PathEscape(number)
	switch err := h.store.Register(r.Context(), number, r.PostFormValue("line"), u.ID,
		r.PostFormValue("serial"), unit); {
	case err == nil:
		http.Redirect(w, r, back+"?ok=1", http.StatusSeeOther)
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
		if line.ID != r.PostFormValue("line") || !line.Registrable() {
			continue
		}
		line.DraftSerial = r.PostFormValue("serial")
		if refusal != i18n.KeyWarrantyRefused {
			line.SerialRefusal = message
			view.Refusal = ""
		}
		break
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.WarrantyOrder(
		layouts.Page{Title: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyWarrantyRegisterMeta), view.Number)}, view))
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyTryAgainBody)))
}

func noticeFor(r *http.Request) string {
	if r.URL.Query().Get("ok") != "1" {
		return ""
	}
	return i18n.T(r.Context(), i18n.KeyWarrantyAlready)
}
