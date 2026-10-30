package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web/compress"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/respond"
)

func (h *Handler) ForgotPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := user.FromContext(r.Context()); ok {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	view := ForgotView{Sent: r.URL.Query().Get("sent") == "1", Address: email.ReadMasked(r.URL.Query().Get("address"))}
	if r.URL.Query().Get("demo") == "fixed" {
		view.Notice = widget.Result{Outcome: widget.OutcomeRefused, Text: i18n.T(r.Context(), i18n.KeyDemoPasswordNoReset)}
	}
	respond.Component(w, r, h.log, http.StatusOK, Forgot(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyForgotTitle)}, view))
}

// Forgot answers identically whether or not the address is known.
func (h *Handler) Forgot(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
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
	if len(normalised) > email.MaxBytes {
		http.Redirect(w, r, "/forgot?sent=1", http.StatusSeeOther)
		return
	}

	if err := h.requestResetLink(r.Context(), addr); err != nil {
		h.log.ErrorContext(r.Context(), "queue password reset request", "error", err)
		h.serverError(w, r)
		return
	}
	destination := "/forgot?sent=1"
	if masked := email.Mask(normalised); masked != "" {
		destination = "/forgot?" + url.Values{"sent": {"1"}, "address": {masked}}.Encode()
	}
	http.Redirect(w, r, destination, http.StatusSeeOther)
}

// requestResetLink queues a reset link for addr unless the address has had its
// allowance of them, and either way the caller answers that the link is on its
// way. The allowance bounds the mail and never the request. Whoever names the
// address spends it, so refusing the request would refuse the owner on a
// stranger's say, and every request that spent it queued a link to the owner,
// the newest replacing the rest. An empty allowance was last spent within one
// addressMailPace interval, which is shorter than ResetTokenTTL, so an owner
// who asks then already has a live link. That is the OWASP Forgot Password
// Cheat Sheet's rule: bound the requests per account, answer every request
// alike, and never lock the account out.
func (h *Handler) requestResetLink(ctx context.Context, addr string) error {
	if !h.mayMail(ctx, "forgot", addr) {
		return nil
	}
	return h.store.requestReset(ctx, addr)
}

func (h *Handler) RequestPasswordSetup(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if h.refuseDemoChange(w, r, u, PasswordForm) {
		return
	}
	if EmailError(email.Clean(u.Email)) != "" {
		h.refuseOnAccountPage(w, r, AccountRefusal{Form: PasswordForm, Notice: i18n.T(r.Context(), i18n.KeyEmailInvalidNotice)})
		return
	}
	if err := h.requestResetLink(r.Context(), u.Email); err != nil {
		h.log.ErrorContext(r.Context(), "queue password setup request", "error", err)
		h.serverError(w, r)
		return
	}
	h.writePasswordSetupNotice(w, u)
	http.Redirect(w, r, "/account?password_setup=sent#password-heading", http.StatusSeeOther)
}

// ResetPage does not check the token: that would tell a guesser it is real.
func (h *Handler) ResetPage(w http.ResponseWriter, r *http.Request) {
	compress.Skip(w)
	respond.Component(w, r, h.log, http.StatusOK, Reset(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyResetTitle)},
		ResetView{Token: r.URL.Query().Get("token")}))
}

func (h *Handler) Reset(w http.ResponseWriter, r *http.Request) {
	// A rejected password can re-render the still-live reset token; never compress it.
	compress.Skip(w)
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	token := r.PostFormValue("token")
	password := r.PostFormValue("password")

	if password != r.PostFormValue("confirm") {
		respond.Component(w, r, h.log, http.StatusUnprocessableEntity, Reset(
			frame.Page{Title: i18n.T(r.Context(), i18n.KeyResetTitle)},
			ResetView{Token: token, Error: i18n.T(r.Context(), i18n.KeyPasswordMismatch), ErrorField: "confirm"}))
		return
	}

	done, err := h.store.CompleteReset(r.Context(), token, password)
	switch {
	case err == nil:
		SetDeviceCookie(w, done.Device, h.secure)
		h.writeSignInContext(w, signInAfterReset, done.Address)
		http.Redirect(w, r, "/signin?reset=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalidPassword):
		refusal, refused := PasswordRefusal("password", password)
		if !refused {
			h.log.ErrorContext(r.Context(), "complete password reset",
				"error", "the store refused a password that passes the policy")
			h.serverError(w, r)
			return
		}
		errs := formtext.FieldMessages(r.Context(), []formtext.FieldRefusal{refusal})
		respond.Component(w, r, h.log, http.StatusUnprocessableEntity, Reset(
			frame.Page{Title: i18n.T(r.Context(), i18n.KeyResetTitle)},
			ResetView{Token: token, Error: errs["password"]}))
	case errors.Is(err, ErrResetInvalid):
		respond.Component(w, r, h.log, http.StatusUnprocessableEntity, Reset(
			frame.Page{Title: i18n.T(r.Context(), i18n.KeyEmailLinkDeadTitle)},
			ResetView{Expired: true}))
	default:
		h.log.ErrorContext(r.Context(), "complete password reset", "error", err)
		h.serverError(w, r)
	}
}
