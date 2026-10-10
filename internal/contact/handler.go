package contact

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/contactsubject"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store *Store
	limit *ratelimit.Limiter
	log   *slog.Logger
}

// NewHandler requires the limiter: unbounded, a script fills contact_messages
// and buries the real customer on a page ordered oldest-first.
func NewHandler(store *Store, limit *ratelimit.Limiter, log *slog.Logger) *Handler {
	if store == nil || limit == nil || log == nil {
		panic("contact: NewHandler requires a store, a limiter and a logger")
	}
	return &Handler{store: store, limit: limit, log: log}
}

// Page reads a sent flag, which the plain form's redirect carries, so a reload
// shows the acknowledgement instead of re-submitting.
func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	form := pages.ContactForm{
		Subjects: subjectChoices(r.Context()),
		Done:     r.URL.Query().Get("sent") == "1",
		SignedIn: signedIn(r.Context()),
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Contact(pages.ContactMeta(r.Context()), form))
}

func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		h.log.WarnContext(r.Context(), "parse contact form", "error", err)
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	msg := Clean(Message{
		Name:     r.PostFormValue("name"),
		Email:    r.PostFormValue("email"),
		Subject:  contactsubject.Subject(r.PostFormValue("subject")),
		OrderRef: r.PostFormValue("order_ref"),
		Body:     r.PostFormValue("message"),
	})

	form := pages.ContactForm{
		Name:     msg.Name,
		Email:    msg.Email,
		Subject:  string(msg.Subject),
		OrderRef: msg.OrderRef,
		Message:  msg.Body,
		Subjects: subjectChoices(r.Context()),
	}

	// Keyed on the IP alone, unlike /forgot and the newsletter: this form mails
	// nobody, and its address field is the sender's own claim, so bounding on
	// it would let anybody buy more attempts by editing a field. After Clean so
	// a 429 fragment can keep the values the way 422 does.
	if retryAfter, allowed := h.limit.Allow(ratelimit.ClientKey(r)); !allowed {
		form.Errors = map[string]string{"": i18n.T(r.Context(), i18n.KeyTooManyRequests)}
		if web.IsHTMX(r) {
			ratelimit.SetRetryAfter(w, retryAfter)
			h.respond(w, r, http.StatusTooManyRequests, form)
			return
		}
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}

	if errs := Validate(r.Context(), msg); len(errs) > 0 {
		form.Errors = errs
		h.respond(w, r, http.StatusUnprocessableEntity, form)
		return
	}

	if err := h.store.Create(r.Context(), msg); err != nil {
		h.log.ErrorContext(r.Context(), "store contact message", "error", err)
		form.Errors = map[string]string{"": i18n.T(r.Context(), i18n.KeyContactBusy)}
		h.respond(w, r, http.StatusInternalServerError, form)
		return
	}

	if web.IsHTMX(r) {
		done := pages.ContactForm{Subjects: subjectChoices(r.Context()), Done: true, SignedIn: signedIn(r.Context())}
		web.Render(w, r, h.log, http.StatusOK, pages.ContactPanel(done))
		return
	}
	http.Redirect(w, r, "/contact?sent=1", http.StatusSeeOther)
}

func signedIn(ctx context.Context) bool {
	_, ok := user.FromContext(ctx)
	return ok
}

func (h *Handler) respond(w http.ResponseWriter, r *http.Request, status int, form pages.ContactForm) {
	if web.IsHTMX(r) {
		web.Render(w, r, h.log, status, pages.ContactPanel(form))
		return
	}
	web.Render(w, r, h.log, status, pages.Contact(pages.ContactMeta(r.Context()), form))
}

func subjectChoices(ctx context.Context) []pages.ContactSubject {
	choices := contactsubject.Choices()
	out := make([]pages.ContactSubject, 0, len(choices))
	for _, choice := range choices {
		out = append(out, pages.ContactSubject{Value: string(choice.Value), Label: i18n.T(ctx, choice.Key)})
	}
	return out
}
