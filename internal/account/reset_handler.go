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
		// Already signed in: they can change the password from the account
		// page, which asks for the current one.
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Forgot(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyForgotTitle)},
		pages.ForgotView{Sent: r.URL.Query().Get("sent") == "1"}))
}

// Forgot serves POST /forgot.
//
// It answers the SAME thing whether or not the address belongs to anybody. A
// form that says "no such account" is an oracle for which addresses are
// registered, and the person asking is rarely the account's owner.
func (h *Handler) Forgot(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	email := r.PostFormValue("email")

	// Bounded per address AND per IP. Per-address, because an unbounded form
	// lets anybody fill somebody else's mailbox with reset mail — a nuisance
	// that also trains them to ignore the real one. Per-IP is the middleware in
	// cmd/goen, the same limiter the sign-in uses.
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
		// Enqueued in the outbox rather than sent here: a reset the customer
		// never receives is a customer who stays locked out, and sending from
		// the handler loses it when the process dies mid-send.
		if err := h.store.EnqueueReset(r.Context(), sendTo, token); err != nil {
			h.log.ErrorContext(r.Context(), "enqueue password reset", "error", err)
			h.serverError(w, r)
			return
		}
	}
	// Same answer either way.
	http.Redirect(w, r, "/forgot?sent=1", http.StatusSeeOther)
}

// ResetPage serves GET /reset.
//
// The token stays in the URL and is echoed into the form rather than being
// checked here. Checking it on GET would tell somebody holding a guessed token
// whether it was real without spending anything — and the page cannot act on
// the answer anyway, because the password is not typed yet.
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

	// NOT rate limited on the token, deliberately. Somebody guessing tokens
	// sends a DIFFERENT one every attempt, so a per-token bucket never sees two
	// of their requests — it would only throttle the one person retrying their
	// own valid link. What bounds guessing here is the per-IP limiter on the
	// route, and what bounds the argon2 amplifier is the cheap token read
	// CompleteReset does before it hashes anything.

	// Typed twice, the same as registering. A reset exists because somebody
	// cannot get in; setting a password with a typo in it and being locked out
	// again is the exact failure this feature is here to end.
	if password != r.PostFormValue("confirm") {
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.Reset(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyResetTitle)},
			pages.ResetView{Token: token, Error: i18n.T(r.Context(), i18n.KeyPasswordMismatch)}))
		return
	}

	err := h.store.CompleteReset(r.Context(), token, password)
	switch {
	case err == nil:
		// Not signed in afterwards. Somebody who reset the password should
		// prove they can use it, and an automatic session would mean a stolen
		// link is a session rather than one more step.
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

// normaliseForLimit keys the rate limiter on the address as typed, lowercased.
//
// Not the canonical address: keying on what the DATABASE would match would let
// somebody vary the case to get a fresh allowance, and keying on the raw string
// would do the same. Lowercasing is what makes the two agree.
func normaliseForLimit(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
