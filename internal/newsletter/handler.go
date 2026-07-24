package newsletter

import (
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Handler serves the footer's newsletter form.
type Handler struct {
	store *Store
	log   *slog.Logger
}

// NewHandler returns a Handler writing through store.
func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("newsletter: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

// Submit serves POST /newsletter. The form lives in the footer of every page,
// so a plain browser cannot be answered with "the same page again" — it is
// redirected to the acknowledgement instead.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		h.log.WarnContext(r.Context(), "parse newsletter form", "error", err)
		http.Error(w, "400 表單格式錯誤", http.StatusBadRequest)
		return
	}

	addr := email.Clean(r.PostFormValue("email"))

	if msg := Validate(addr); msg != "" {
		h.fail(w, r, http.StatusUnprocessableEntity, addr, msg)
		return
	}

	if err := h.store.Subscribe(r.Context(), addr); err != nil {
		h.log.ErrorContext(r.Context(), "subscribe newsletter", "error", err)
		h.fail(w, r, http.StatusInternalServerError, addr, "系統暫時無法處理訂閱,請稍後再試。")
		return
	}

	if web.IsHTMX(r) {
		done := layouts.NewsletterState{Done: true}
		web.Render(w, r, h.log, http.StatusOK, layouts.NewsletterForm(done))
		return
	}
	http.Redirect(w, r, "/newsletter/thanks", http.StatusSeeOther)
}

// Thanks serves GET /newsletter/thanks, the landing point of the plain form's
// redirect.
func (h *Handler) Thanks(w http.ResponseWriter, r *http.Request) {
	meta := layouts.Page{
		Title:       "已訂閱電子報",
		Description: "goen 電子報訂閱確認。",
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Notice(
		meta,
		"",
		"已訂閱,感謝你的信任",
		"每月一封,新品與比價重點,不灌水。任何一封信的頁尾都可以隨時退訂。",
	))
}

// fail answers a rejected subscription: the form itself for htmx, and a
// standalone page for a browser that has left the page the form was on.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, status int, addr, msg string) {
	if web.IsHTMX(r) {
		state := layouts.NewsletterState{Email: addr, Error: msg}
		web.Render(w, r, h.log, status, layouts.NewsletterForm(state))
		return
	}
	meta := layouts.Page{Title: "訂閱未完成"}
	web.Render(w, r, h.log, status, pages.Notice(meta, "", "訂閱未完成", msg))
}
