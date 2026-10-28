package messages

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("messages: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *admin.Control) {
	mux.HandleFunc("GET /admin/messages", ac.RequireStaff(h.Messages))
	mux.HandleFunc("POST /admin/messages/handle", ac.RequireStaff(h.HandleMessage))
	mux.HandleFunc("POST /admin/messages/reopen", ac.RequireStaff(h.ReopenMessage))
}

var notices = map[string]widget.NoticeEntry{
	"handled":  widget.NoticeDone(i18n.KeyAdminNoticeMessageHandled),
	"reopened": widget.NoticeDone(i18n.KeyAdminNoticeMessageReopened),
	"gone":     widget.NoticeRefused(i18n.KeyAdminNoticeGone),
}

func (h *Handler) Messages(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Messages(r.Context(), r.URL.Query().Get(keyset.Param), time.Now())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read contact messages", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	view.Notice = widget.NoticeFrom(r, notices)
	respond.Component(w, r, h.log, http.StatusOK, Messages(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageMessages)}, view))
}

func (h *Handler) HandleMessage(w http.ResponseWriter, r *http.Request) {
	h.setMessageHandled(w, r, true)
}

func (h *Handler) ReopenMessage(w http.ResponseWriter, r *http.Request) {
	h.setMessageHandled(w, r, false)
}

func (h *Handler) setMessageHandled(w http.ResponseWriter, r *http.Request, handled bool) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	switch err := h.store.SetMessageHandled(r.Context(), r.PostFormValue("message"), handled); {
	case err == nil:
		http.Redirect(w, r, handledTarget(handled), http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		http.Redirect(w, r, "/admin/messages?gone=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set message handled", "error", err)
		admin.ServerError(w, r, h.log)
	}
}

// handledTarget is where a write that took goes: the page says which change it was.
func handledTarget(handled bool) string {
	if handled {
		return "/admin/messages?handled=1"
	}
	return "/admin/messages?reopened=1"
}
