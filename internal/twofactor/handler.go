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
	// limit bounds code submissions: unthrottled, a million possibilities is
	// under twenty minutes at a thousand guesses a second.
	limit *ratelimit.Limiter
	// secure selects the session cookie's name.
	secure bool
}

// NewHandler returns a Handler.
func NewHandler(store *Store, log *slog.Logger, secure bool) *Handler {
	if store == nil || log == nil {
		panic("twofactor: NewHandler requires a store and a logger")
	}
	return &Handler{
		store: store, log: log, secure: secure,
		// Ten attempts, one back a minute: 60 guesses an hour against a million
		// possibilities is 1,900 years.
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
		if errors.Is(err, ErrSecretUnreadable) {
			h.logUnreadable(r)
			// A credential row exists; only its configured key is stale. Treat it
			// as enrolled so this password-only page cannot replace the factor,
			// and render the recovery instruction here rather than redirecting
			// back into the same failing GET.
			enrolled = true
		} else {
			h.log.ErrorContext(r.Context(), "read totp state", "error", err)
			http.Error(w, "500", http.StatusInternalServerError)
			return
		}
	}
	notice := noticeFor(r)
	if errors.Is(err, ErrSecretUnreadable) {
		notice = i18n.T(r.Context(), i18n.KeyTOTPSecretUnreadable)
	}
	view := pages.TwoFactorView{
		Enabled:  h.store.Enabled(),
		Enrolled: enrolled,
		Notice:   notice,
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
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}

	if err := h.store.Verify(r.Context(), u.ID, r.PostFormValue("code")); err != nil {
		if errors.Is(err, ErrSecretUnreadable) {
			h.logUnreadable(r)
			http.Redirect(w, r, "/admin/verify?stale=1", http.StatusSeeOther)
			return
		}
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
		if errors.Is(err, ErrEnrolled) {
			http.Redirect(w, r, "/admin/verify?enrolled=1", http.StatusSeeOther)
			return
		}
		h.log.ErrorContext(r.Context(), "begin enrolment", "error", err)
		http.Error(w, "500", http.StatusInternalServerError)
		return
	}
	// Rendered rather than redirected to: a redirect would have to carry the
	// secret in a URL, into the browser history and every access log.
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
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}
	if err := h.store.Confirm(r.Context(), u.ID, r.PostFormValue("code")); err != nil {
		h.log.WarnContext(r.Context(), "totp confirm", "error", err)
		http.Redirect(w, r, "/admin/verify?badenrol=1", http.StatusSeeOther)
		return
	}
	// Confirming proves the factor, so the session is verified too.
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
	case r.URL.Query().Get("stale") == "1":
		return i18n.T(r.Context(), i18n.KeyTOTPSecretUnreadable)
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

func (h *Handler) logUnreadable(r *http.Request) {
	h.log.ErrorContext(r.Context(),
		"totp secret does not open; GOEN_TOTP_KEY does not match the key these rows were sealed under",
		"set", "GOEN_TOTP_KEY")
}

// StepUp reports whether a request's session has proved a second factor
// recently.
func (h *Handler) StepUp(r *http.Request) (bool, error) {
	token := account.ReadSessionCookie(r, h.secure)
	if token == "" {
		return false, nil
	}
	return h.store.SessionVerified(r.Context(), token)
}

// Staff serves GET /admin/staff.
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
		view.Notice = i18n.T(r.Context(), i18n.KeyTOTPNoKeyNotice)
	}
	if u, ok := account.FromContext(r.Context()); ok {
		view.Actor = u.ID
	}
	// Only when there IS one: an unconditional assignment overwrites the
	// no-key warning set above with an empty string on an ordinary visit.
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
	cleared, err := h.store.AddStaff(r.Context(),
		r.PostFormValue("email"), r.PostFormValue("name"), r.PostFormValue("role"),
		actorID(r))
	if err == nil && cleared {
		// A success the admin has to relay: the new colleague cannot get in
		// until they set a password through /forgot.
		http.Redirect(w, r, "/admin/staff?cleared=1", http.StatusSeeOther)
		return
	}
	h.redirectStaff(w, r, err)
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
	case r.URL.Query().Get("cleared") == "1":
		return i18n.T(r.Context(), i18n.KeyStaffCredentialCleared)
	case r.URL.Query().Get("self") == "1":
		return i18n.T(r.Context(), i18n.KeyStaffSelf)
	case r.URL.Query().Get("last") == "1":
		return i18n.T(r.Context(), i18n.KeyStaffLastAdmin)
	case r.URL.Query().Get("needs") == "1":
		return i18n.T(r.Context(), i18n.KeyStaffInvalid)
	}
	return ""
}
