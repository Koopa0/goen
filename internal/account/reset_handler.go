package account

import (
	"errors"
	"net/http"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

func (h *Handler) ForgotPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := user.FromContext(r.Context()); ok {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	view := pages.ForgotView{Sent: r.URL.Query().Get("sent") == "1"}
	if r.URL.Query().Get("demo") == "fixed" {
		view.Notice = i18n.T(r.Context(), i18n.KeyDemoPasswordNoReset)
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Forgot(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyForgotTitle)}, view))
}

// Forgot answers identically whether or not the address is known.
func (h *Handler) Forgot(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	addr := r.PostFormValue("email")
	normalised := email.Clean(addr)
	if h.demo.holds(normalised) {
		http.Redirect(w, r, "/forgot?demo=fixed", http.StatusSeeOther)
		return
	}
	// The success redirect is also the refusal: an address outside the policy
	// cannot name an account and must not become a key.
	if len(normalised) > email.Max {
		http.Redirect(w, r, "/forgot?sent=1", http.StatusSeeOther)
		return
	}

	if retryAfter, ok := h.resetLimit.Allow("forgot:" + normalised); !ok {
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}

	if err := h.store.requestReset(r.Context(), addr); err != nil {
		h.log.ErrorContext(r.Context(), "queue password reset request", "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/forgot?sent=1", http.StatusSeeOther)
}

// ResetPage does not check the token: that would tell a guesser it is real.
func (h *Handler) ResetPage(w http.ResponseWriter, r *http.Request) {
	web.NoCompress(w)
	web.Render(w, r, h.log, http.StatusOK, pages.Reset(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyResetTitle)},
		pages.ResetView{Token: r.URL.Query().Get("token")}))
}

func (h *Handler) Reset(w http.ResponseWriter, r *http.Request) {
	// A rejected password can re-render the still-live reset token; never compress it.
	web.NoCompress(w)
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	token := r.PostFormValue("token")
	password := r.PostFormValue("password")

	if password != r.PostFormValue("confirm") {
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.Reset(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyResetTitle)},
			pages.ResetView{Token: token, Error: i18n.T(r.Context(), i18n.KeyPasswordMismatch), ErrorField: "confirm"}))
		return
	}

	address, err := h.store.CompleteReset(r.Context(), token, password)
	switch {
	case err == nil:
		h.writeSignInContext(w, signInAfterReset, address)
		http.Redirect(w, r, "/signin?reset=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalidPassword):
		errs := FieldMessages(r.Context(), []web.FieldRefusal{
			{Field: "password", MessageKey: PasswordError(password)},
		})
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.Reset(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyResetTitle)},
			pages.ResetView{Token: token, Error: errs["password"]}))
	case errors.Is(err, ErrResetInvalid):
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.Reset(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyEmailLinkDeadTitle)},
			pages.ResetView{Expired: true}))
	default:
		h.log.ErrorContext(r.Context(), "complete password reset", "error", err)
		h.serverError(w, r)
	}
}
