package twofactor

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Handler serves enrolment and the step-up challenge.
type Handler struct {
	store *Store
	log   *slog.Logger
	// limit bounds code submissions. A six-digit code is a million
	// possibilities, which sounds like a lot until an unthrottled endpoint is
	// asked a thousand times a second — that is under twenty minutes for a
	// single account, and the code only has to be right once.
	limit *ratelimit.Limiter
	// secure selects the session cookie's name, the same way every other
	// package that reads it does.
	secure bool
}

// NewHandler returns a Handler.
func NewHandler(store *Store, log *slog.Logger, secure bool) *Handler {
	if store == nil || log == nil {
		panic("twofactor: NewHandler requires a store and a logger")
	}
	return &Handler{
		store: store, log: log, secure: secure,
		// Ten attempts, one back a minute. A person reading a code off their
		// phone never meets it; a script gets 60 guesses an hour against a
		// million possibilities, which is 1,900 years.
		limit: ratelimit.New(ratelimit.Config{
			Every: time.Minute, Burst: 10, TTL: time.Hour,
		}),
	}
}

// Challenge serves GET /admin/verify.
func (h *Handler) Challenge(w http.ResponseWriter, r *http.Request) {
	u, ok := account.FromContext(r.Context())
	if !ok {
		http.NotFound(w, r)
		return
	}
	enrolled, err := h.store.Enrolled(r.Context(), u.ID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read totp state", "error", err)
		http.Error(w, "500", http.StatusInternalServerError)
		return
	}
	view := pages.TwoFactorView{
		Enabled:  h.store.Enabled(),
		Enrolled: enrolled,
		Notice:   noticeFor(r),
	}
	web.Render(w, r, h.log, http.StatusOK, pages.TwoFactor(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageTwoFactor)}, view))
}

// Verify serves POST /admin/verify.
func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	u, ok := account.FromContext(r.Context())
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	if retryAfter, allowed := h.limit.Allow("totp:" + u.ID); !allowed {
		h.log.WarnContext(r.Context(), "totp throttled")
		ratelimit.Refuse(w, retryAfter)
		return
	}

	if err := h.store.Verify(r.Context(), u.ID, r.PostFormValue("code")); err != nil {
		h.log.WarnContext(r.Context(), "totp verify", "error", err)
		http.Redirect(w, r, "/admin/verify?bad=1", http.StatusSeeOther)
		return
	}
	token := account.ReadSessionCookie(r, h.secure)
	if token == "" {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := h.store.MarkVerified(r.Context(), token); err != nil {
		h.log.ErrorContext(r.Context(), "mark session verified", "error", err)
		http.Error(w, "500", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// Enrol serves POST /admin/verify/enrol.
func (h *Handler) Enrol(w http.ResponseWriter, r *http.Request) {
	u, ok := account.FromContext(r.Context())
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	secret, uri, err := h.store.Begin(r.Context(), u.ID, u.Email)
	if err != nil {
		if errors.Is(err, ErrDisabled) {
			http.Redirect(w, r, "/admin/verify?disabled=1", http.StatusSeeOther)
			return
		}
		// Already proved. Not an error the person can act on by retrying, so it
		// says who CAN act: another admin removes the credential.
		if errors.Is(err, ErrEnrolled) {
			http.Redirect(w, r, "/admin/verify?enrolled=1", http.StatusSeeOther)
			return
		}
		h.log.ErrorContext(r.Context(), "begin enrolment", "error", err)
		http.Error(w, "500", http.StatusInternalServerError)
		return
	}
	// Rendered directly rather than redirected to: the secret exists in this
	// response and nowhere else, and a redirect would either lose it or have to
	// carry it in a URL — into the browser history and every access log.
	web.Render(w, r, h.log, http.StatusOK, pages.TwoFactor(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageTwoFactor)}, pages.TwoFactorView{
			Enabled: true, Enrolling: true,
			Secret: EncodeSecret(secret), URI: uri,
		}))
}

// Confirm serves POST /admin/verify/confirm.
func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	u, ok := account.FromContext(r.Context())
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	if retryAfter, allowed := h.limit.Allow("totp:" + u.ID); !allowed {
		ratelimit.Refuse(w, retryAfter)
		return
	}
	if err := h.store.Confirm(r.Context(), u.ID, r.PostFormValue("code")); err != nil {
		h.log.WarnContext(r.Context(), "totp confirm", "error", err)
		http.Redirect(w, r, "/admin/verify?badenrol=1", http.StatusSeeOther)
		return
	}
	// Confirming proves the factor, so the session is verified too — asking for
	// a second code immediately after the first would be ceremony, not security.
	if token := account.ReadSessionCookie(r, h.secure); token != "" {
		if err := h.store.MarkVerified(r.Context(), token); err != nil {
			h.log.ErrorContext(r.Context(), "mark session verified", "error", err)
		}
	}
	http.Redirect(w, r, "/admin?enrolled=1", http.StatusSeeOther)
}

// noticeFor turns a query flag into a sentence.
func noticeFor(r *http.Request) string {
	switch {
	case r.URL.Query().Get("bad") == "1":
		return i18n.T(r.Context(), i18n.KeyTOTPWrongCode)
	case r.URL.Query().Get("badenrol") == "1":
		return i18n.T(r.Context(), i18n.KeyTOTPWrongSecret)
	case r.URL.Query().Get("disabled") == "1":
		return i18n.T(r.Context(), i18n.KeyTOTPNoKey)
	case r.URL.Query().Get("enrolled") == "1":
		return i18n.T(r.Context(), i18n.KeyTOTPAlreadyEnrolled)
	default:
		return ""
	}
}

// StepUp reports whether a request's session has proved a second factor
// recently.
//
// Handed to internal/admin as a function so that package depends on one answer
// rather than on this whole package — and so a deployment with no encryption
// key can pass nil and get a back office that behaves as it did before.
func (h *Handler) StepUp(r *http.Request) (bool, error) {
	token := account.ReadSessionCookie(r, h.secure)
	if token == "" {
		return false, nil
	}
	return h.store.SessionVerified(r.Context(), token)
}

// Staff serves GET /admin/staff.
//
// Behind RequireStaff and the step-up like every other back-office page: who
// has a second factor is a map of where the back office is weakest, and that is
// not a thing to leave reachable with a password alone.
func (h *Handler) Staff(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Staff(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read staff 2FA status", "error", err)
		web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminFaultTitle)}, "",
			i18n.T(r.Context(), i18n.KeyAdminFaultHead),
			i18n.T(r.Context(), i18n.KeyAdminFaultBody)))
		return
	}
	if !h.store.Enabled() {
		// GOEN_TOTP_KEY is empty, so enrolment is off. Saying so is the honest
		// answer: without it the page reads as "nobody has bothered".
		view.Notice = i18n.T(r.Context(), i18n.KeyTOTPNoKeyNotice)
	}
	if u, ok := account.FromContext(r.Context()); ok {
		view.Actor = u.ID
	}
	// Only when there IS one. This used to assign unconditionally, which on an
	// ordinary visit — no query flag, so an empty string — silently overwrote the
	// "GOEN_TOTP_KEY 沒有設定" warning set above. The one message telling an
	// operator their second factor is off was dead code from the day it was
	// written, on the only page that could have shown it.
	if n := staffNotice(r); n != "" {
		view.Notice = n
	}
	web.Render(w, r, h.log, http.StatusOK, pages.AdminStaff(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageStaff)}, view))
}

// AddStaff serves POST /admin/staff.
func (h *Handler) AddStaff(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	h.redirectStaff(w, r, h.store.AddStaff(r.Context(),
		r.PostFormValue("email"), r.PostFormValue("name"), r.PostFormValue("role"),
		actorID(r)))
}

// RevokeStaff serves POST /admin/staff/revoke.
func (h *Handler) RevokeStaff(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	h.redirectStaff(w, r, h.store.RevokeStaff(r.Context(),
		r.PostFormValue("user"), actorID(r)))
}

// RemoveFactor serves POST /admin/staff/factor.
//
// The recovery path CLAUDE.md has described since 2FA shipped: another admin
// removes the credential, and the person enrols again on their new phone. The
// store method for it existed with no caller, so an admin who lost their
// authenticator was locked out of the back office permanently.
func (h *Handler) RemoveFactor(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	h.redirectStaff(w, r, h.store.RemoveFactor(r.Context(),
		r.PostFormValue("user"), actorID(r)))
}

// actorID is the signed-in admin, for the guards that refuse self-service.
func actorID(r *http.Request) string {
	if u, ok := account.FromContext(r.Context()); ok {
		return u.ID
	}
	return ""
}

// redirectStaff turns a store error into the page's own answer.
func (h *Handler) redirectStaff(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/staff?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrSelf):
		http.Redirect(w, r, "/admin/staff?self=1", http.StatusSeeOther)
	case errors.Is(err, ErrLastAdmin):
		http.Redirect(w, r, "/admin/staff?last=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalidStaff), errors.Is(err, ErrNotEnrolled):
		http.Redirect(w, r, "/admin/staff?needs=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "change staff", "error", err)
		web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminFaultTitle)}, "",
			i18n.T(r.Context(), i18n.KeyAdminFaultHead),
			i18n.T(r.Context(), i18n.KeyAdminFaultBody)))
	}
}

// staffNotice is what the redirect is reporting.
func staffNotice(r *http.Request) string {
	switch {
	case r.URL.Query().Get("ok") == "1":
		return i18n.T(r.Context(), i18n.KeyAdminNoticeOK)
	case r.URL.Query().Get("self") == "1":
		return i18n.T(r.Context(), i18n.KeyStaffSelf)
	case r.URL.Query().Get("last") == "1":
		return i18n.T(r.Context(), i18n.KeyStaffLastAdmin)
	case r.URL.Query().Get("needs") == "1":
		return i18n.T(r.Context(), i18n.KeyStaffInvalid)
	}
	return ""
}
