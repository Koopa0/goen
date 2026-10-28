package newsletters

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/i18n"
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
		panic("newsletters: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *admin.Control) {
	mux.HandleFunc("GET /admin/newsletters", ac.RequireStaff(h.Newsletter))
	mux.HandleFunc("POST /admin/newsletters", ac.RequireStaff(h.ComposeNewsletter))
	mux.HandleFunc("POST /admin/newsletters/{id}/send", ac.RequireStaff(h.SendNewsletter))
}

var notices = map[string]widget.NoticeEntry{
	"saved":   widget.NoticeDone(i18n.KeyAdminNoticeSaved),
	"sent":    widget.NoticeDone(i18n.KeyAdminNoticeSent),
	"already": widget.NoticeDone(i18n.KeyAdminNoticeAlready),
}

const IssueLimit = 50

func (h *Handler) Newsletter(w http.ResponseWriter, r *http.Request) {
	view, err := h.newsletterView(r)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read the newsletter", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	view.Notice = widget.NoticeFrom(r, notices)
	respond.Component(w, r, h.log, http.StatusOK, Newsletter(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageNewsletter)}, view))
}

// ComposeNewsletter writes a DRAFT and sends nothing: the irreversible step gets its own button.
func (h *Handler) ComposeNewsletter(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseLongTextForm(w, r, MaxNewsletterSubjectRunes+MaxNewsletterBodyRunes); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	subject, body := r.PostFormValue("subject"), r.PostFormValue("body")

	if keys := validateNewsletter(subject, body); len(keys) > 0 {
		view, err := h.newsletterView(r)
		if err != nil {
			h.log.ErrorContext(r.Context(), "read the newsletter", "error", err)
			admin.ServerError(w, r, h.log)
			return
		}
		view.Draft = Draft{Subject: subject, Body: body}
		view.Errors = make(map[string]string, len(keys))
		for field, k := range keys {
			view.Errors[field] = i18n.T(r.Context(), k)
		}
		respond.Component(w, r, h.log, http.StatusUnprocessableEntity, Newsletter(
			frame.Page{Refused: true, Title: i18n.T(r.Context(), i18n.KeyAdminPageNewsletter)}, view))
		return
	}

	if _, err := h.store.ComposeNewsletter(r.Context(), subject, body, audit.ActorID(r.Context())); err != nil {
		h.log.ErrorContext(r.Context(), "compose a newsletter issue", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	http.Redirect(w, r, "/admin/newsletters?saved=1", http.StatusSeeOther)
}

// SendNewsletter relies on the store refusing a second send in the UPDATE's own
// WHERE clause, because a reload is not the only way two of these arrive.
func (h *Handler) SendNewsletter(w http.ResponseWriter, r *http.Request) {
	switch _, err := h.store.SendNewsletter(r.Context(), r.PathValue("id"), audit.ActorID(r.Context())); {
	case err == nil:
		http.Redirect(w, r, "/admin/newsletters?sent=1", http.StatusSeeOther)
	case errors.Is(err, ErrAlreadySent):
		http.Redirect(w, r, "/admin/newsletters?already=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		admin.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "send a newsletter issue", "error", err)
		admin.ServerError(w, r, h.log)
	}
}

func (h *Handler) newsletterView(r *http.Request) (View, error) {
	counts, err := h.store.SubscriberCounts(r.Context())
	if err != nil {
		return View{}, err
	}
	issues, err := h.store.NewsletterIssues(r.Context(), IssueLimit)
	if err != nil {
		return View{}, err
	}
	return View{
		Active: counts.Active, Unsubscribed: counts.Unsubscribed, Awaiting: counts.Awaiting,
		EnglishActive: counts.EnglishActive,
		Issues:        issues,
	}, nil
}
