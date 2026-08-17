package account

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// ForgotPage serves GET /forgot.
func (h *Handler) ForgotPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := FromContext(r.Context()); ok {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Forgot(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyForgotTitle)},
		pages.ForgotView{Sent: r.URL.Query().Get("sent") == "1"}))
}

// Forgot serves POST /forgot. It answers the same thing whether or not the
// address belongs to anybody.
func (h *Handler) Forgot(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	email := r.PostFormValue("email")

	if retryAfter, ok := h.resetLimit.Allow("forgot:" + normaliseForLimit(email)); !ok {
		ratelimit.Refuse(w, retryAfter)
		return
	}

	token, sendTo, found, err := h.store.BeginReset(r.Context(), email)
	if err != nil {
		h.log.ErrorContext(r.Context(), "begin password reset", "error", err)
		h.serverError(w, r)
		return
	}
	if found {
		if err := h.store.EnqueueReset(r.Context(), sendTo, token); err != nil {
			h.log.ErrorContext(r.Context(), "enqueue password reset", "error", err)
			h.serverError(w, r)
			return
		}
	}
	http.Redirect(w, r, "/forgot?sent=1", http.StatusSeeOther)
}

// ResetPage serves GET /reset. The token is echoed into the form and never
// checked here: checking it would tell a guesser whether it was real.
func (h *Handler) ResetPage(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusOK, pages.Reset(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyResetTitle)},
		pages.ResetView{Token: r.URL.Query().Get("token")}))
}

// Reset serves POST /reset.
func (h *Handler) Reset(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	token := r.PostFormValue("token")
	password := r.PostFormValue("password")

	if password != r.PostFormValue("confirm") {
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.Reset(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyResetTitle)},
			pages.ResetView{Token: token, Error: i18n.T(r.Context(), i18n.KeyPasswordMismatch)}))
		return
	}

	err := h.store.CompleteReset(r.Context(), token, password)
	switch {
	case err == nil:
		http.Redirect(w, r, "/signin?reset=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalidPassword):
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.Reset(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyResetTitle)},
			pages.ResetView{Token: token, Error: fmt.Sprintf(
				i18n.T(r.Context(), PasswordError(password)), MinPasswordRunes)}))
	case errors.Is(err, ErrResetInvalid):
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.Reset(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyResetTitle)},
			pages.ResetView{Expired: true}))
	default:
		h.log.ErrorContext(r.Context(), "complete password reset", "error", err)
		h.serverError(w, r)
	}
}

// normaliseForLimit keys the rate limiter on the address lowercased, so varying
// the case does not buy a fresh allowance.
func normaliseForLimit(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
