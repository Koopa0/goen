package contact

import (
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Handler serves 聯絡我們.
type Handler struct {
	store *Store
	log   *slog.Logger
}

// NewHandler returns a Handler writing through store.
func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("contact: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

// Page serves GET /contact. The sent flag is the one-shot signal the plain
// form's redirect carries, so a reload shows the acknowledgement instead of
// re-submitting.
func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	form := pages.ContactForm{
		Subjects: Subjects,
		Done:     r.URL.Query().Get("sent") == "1",
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Contact(pages.ContactMeta, form))
}

// Submit serves POST /contact.
//
// The plain form is the whole write path: it validates, stores, and redirects
// (303) so a reload is safe. htmx, when present, only changes what is written
// back — the panel alone instead of the page — and never how the write happens.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		h.log.WarnContext(r.Context(), "parse contact form", "error", err)
		http.Error(w, "400 表單格式錯誤", http.StatusBadRequest)
		return
	}

	msg := Clean(Message{
		Name:     r.PostFormValue("name"),
		Email:    r.PostFormValue("email"),
		Subject:  r.PostFormValue("subject"),
		OrderRef: r.PostFormValue("order_ref"),
		Body:     r.PostFormValue("message"),
	})

	form := pages.ContactForm{
		Name:     msg.Name,
		Email:    msg.Email,
		Subject:  msg.Subject,
		OrderRef: msg.OrderRef,
		Message:  msg.Body,
		Subjects: Subjects,
	}

	if errs := Validate(msg); len(errs) > 0 {
		form.Errors = errs
		h.respond(w, r, http.StatusUnprocessableEntity, form)
		return
	}

	if err := h.store.Create(r.Context(), msg); err != nil {
		h.log.ErrorContext(r.Context(), "store contact message", "error", err)
		form.Errors = map[string]string{"": "系統暫時無法接收訊息,請稍後再試,或直接寄信到 support@goen.tw。"}
		h.respond(w, r, http.StatusInternalServerError, form)
		return
	}

	if web.IsHTMX(r) {
		done := pages.ContactForm{Subjects: Subjects, Done: true}
		web.Render(w, r, h.log, http.StatusOK, pages.ContactPanel(done))
		return
	}
	http.Redirect(w, r, "/contact?sent=1", http.StatusSeeOther)
}

// respond writes the panel back to htmx and the whole page to a plain browser,
// under the same status either way.
func (h *Handler) respond(w http.ResponseWriter, r *http.Request, status int, form pages.ContactForm) {
	if web.IsHTMX(r) {
		web.Render(w, r, h.log, status, pages.ContactPanel(form))
		return
	}
	web.Render(w, r, h.log, status, pages.Contact(pages.ContactMeta, form))
}
