package messages

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/web"
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

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/messages", ac.RequireStaff(h.Messages))
	mux.HandleFunc("POST /admin/messages/handle", ac.RequireStaff(h.HandleMessage))
	mux.HandleFunc("POST /admin/messages/reopen", ac.RequireStaff(h.ReopenMessage))
}

var notices = map[string]components.NoticeEntry{
	"ok":   components.NoticeDone(i18n.KeyAdminNoticeOK),
	"gone": components.NoticeRefused(i18n.KeyAdminNoticeGone),
}

func (h *Handler) Messages(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Messages(r.Context(), r.URL.Query().Get(keyset.Param))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read contact messages", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = components.NoticeFrom(r, notices)
	web.Render(w, r, h.log, http.StatusOK, Messages(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageMessages)}, view))
}

func (h *Handler) HandleMessage(w http.ResponseWriter, r *http.Request) {
	h.setMessageHandled(w, r, true)
}

func (h *Handler) ReopenMessage(w http.ResponseWriter, r *http.Request) {
	h.setMessageHandled(w, r, false)
}

func (h *Handler) setMessageHandled(w http.ResponseWriter, r *http.Request, handled bool) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	switch err := h.store.SetMessageHandled(r.Context(), r.PostFormValue("message"), handled); {
	case err == nil:
		http.Redirect(w, r, "/admin/messages?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		http.Redirect(w, r, "/admin/messages?gone=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set message handled", "error", err)
		access.ServerError(w, r, h.log)
	}
}
