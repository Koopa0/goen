package twofactor

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"rsc.io/qr"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store *Store
	log   *slog.Logger
	// limit bounds code submissions: unthrottled, a million possibilities is
	// under twenty minutes at a thousand guesses a second.
	limit  *ratelimit.Limiter
	secure bool
}

func NewHandler(store *Store, log *slog.Logger, secure bool) *Handler {
	if store == nil || log == nil {
		panic("twofactor: NewHandler requires a store and a logger")
	}
	return &Handler{
		store: store, log: log, secure: secure,
		// Ten attempts, one back a minute: 60 guesses an hour against a million.
		limit: ratelimit.New(ratelimit.Config{
			Every: time.Minute, Burst: 10, TTL: time.Hour, MaxKeys: 8_192,
		}),
	}
}

func (h *Handler) Challenge(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.NotFound(w, r)
		return
	}
	enrolled, err := h.store.Enrolled(r.Context(), u.ID)
	if err != nil {
		switch {
		case errors.Is(err, ErrSecretUnreadable):
			h.logUnreadable(r)
			// A credential row exists; only its configured key is stale. Treat it
			// as enrolled so this password-only page cannot replace the factor,
			// and render the recovery instruction rather than redirecting back
			// into the same failing GET.
			enrolled = true
		case errors.Is(err, ErrDisabled):
			// A deployment fact, not a fault: the view below has a branch that
			// names the missing key.
			enrolled = false
		default:
			h.log.ErrorContext(r.Context(), "read totp state", "error", err)
			access.Fault(w, r, h.log)
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

func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
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
		switch {
		case errors.Is(err, ErrSecretUnreadable):
			h.logUnreadable(r)
			http.Redirect(w, r, "/admin/verify?stale=1", http.StatusSeeOther)
			return
		case errors.Is(err, ErrDisabled):
			http.Redirect(w, r, "/admin/verify?disabled=1", http.StatusSeeOther)
			return
		case errors.Is(err, ErrBadCode), errors.Is(err, ErrNotEnrolled):
			h.log.WarnContext(r.Context(), "totp verify", "error", err)
			http.Redirect(w, r, "/admin/verify?bad=1", http.StatusSeeOther)
			return
		default:
			h.log.ErrorContext(r.Context(), "verify totp", "error", err)
			access.Fault(w, r, h.log)
			return
		}
	}
	token := account.ReadSessionCookie(r, h.secure)
	if token == "" {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := h.store.MarkVerified(r.Context(), token); err != nil {
		h.log.ErrorContext(r.Context(), "mark session verified", "error", err)
		access.Fault(w, r, h.log)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h *Handler) Enrol(w http.ResponseWriter, r *http.Request) {
	// The one-time TOTP seed is password-equivalent; keep its page out of BREACH's reach.
	web.NoCompress(w)
	u, ok := user.FromContext(r.Context())
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
		access.Fault(w, r, h.log)
		return
	}
	// Rendered rather than redirected to: a redirect would have to carry the
	// secret in a URL, into the browser history and every access log.
	code, err := qr.Encode(uri, qr.M)
	if err != nil {
		h.log.ErrorContext(r.Context(), "encode enrolment QR", "error", err)
		access.Fault(w, r, h.log)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.TwoFactor(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageTwoFactor)}, pages.TwoFactorView{
			Enabled: true, Enrolling: true,
			Secret: EncodeSecret(secret), URI: uri,
			QRCode: "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG()),
		}))
}

func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
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
		switch {
		case errors.Is(err, ErrSecretUnreadable):
			h.logUnreadable(r)
			http.Redirect(w, r, "/admin/verify?stale=1", http.StatusSeeOther)
		case errors.Is(err, ErrDisabled):
			http.Redirect(w, r, "/admin/verify?disabled=1", http.StatusSeeOther)
		case errors.Is(err, ErrBadCode), errors.Is(err, ErrNotEnrolled):
			h.log.WarnContext(r.Context(), "totp confirm", "error", err)
			http.Redirect(w, r, "/admin/verify?badenrol=1", http.StatusSeeOther)
		default:
			h.log.ErrorContext(r.Context(), "confirm totp", "error", err)
			access.Fault(w, r, h.log)
		}
		return
	}
	token := account.ReadSessionCookie(r, h.secure)
	if token == "" {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := h.store.MarkVerified(r.Context(), token); err != nil {
		h.log.ErrorContext(r.Context(), "mark session verified", "error", err)
		access.Fault(w, r, h.log)
		return
	}
	http.Redirect(w, r, "/admin?enrolled=1", http.StatusSeeOther)
}

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

func (h *Handler) StepUp(r *http.Request) (bool, error) {
	token := account.ReadSessionCookie(r, h.secure)
	if token == "" {
		return false, nil
	}
	return h.store.SessionVerified(r.Context(), token)
}
