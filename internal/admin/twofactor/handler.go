package twofactor

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"rsc.io/qr"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web/compress"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/ratelimit"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	store *Store
	log   *slog.Logger
	// limit bounds code submissions: unthrottled, a million possibilities is
	// under twenty minutes at a thousand guesses a second.
	limit *ratelimit.Limiter
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("twofactor: NewHandler requires a store and a logger")
	}
	return &Handler{
		store: store, log: log,
		// Ten attempts, one back a minute: 60 guesses an hour against a million.
		limit: ratelimit.New(ratelimit.Config{
			Every: time.Minute, Burst: 10, TTL: time.Hour, MaxKeys: 8_192,
		}),
	}
}

// Routes registers the second-factor pages behind [admin.Control.StaffOnly],
// not RequireStaff: RequireStaff sends a staff member who has not passed the
// second factor here, so these pages behind it would redirect to themselves.
func (h *Handler) Routes(mux *http.ServeMux, ac *admin.Control) {
	mux.HandleFunc("GET /admin/verify", ac.StaffOnly(h.Challenge))
	mux.HandleFunc("POST /admin/verify", ac.StaffOnly(h.Verify))
	mux.HandleFunc("POST /admin/verify/enrol", ac.StaffOnly(h.Enrol))
	mux.HandleFunc("POST /admin/verify/confirm", ac.StaffOnly(h.Confirm))
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
			admin.Fault(w, r, h.log)
			return
		}
	}
	notice := noticeFor(r)
	if errors.Is(err, ErrSecretUnreadable) {
		notice = i18n.T(r.Context(), i18n.KeyTOTPSecretUnreadable)
	}
	view := TwoFactorView{
		Enabled:  h.store.Enabled(),
		Enrolled: enrolled,
		Notice:   notice,
	}
	respond.Component(w, r, h.log, http.StatusOK, TwoFactor(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageTwoFactor)}, view))
}

func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := formtext.ParseForm(w, r); err != nil {
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
		case errors.Is(err, ErrLocked):
			h.log.WarnContext(r.Context(), "totp verify", "user", u.ID, "error", err)
			http.Redirect(w, r, "/admin/verify?locked=1", http.StatusSeeOther)
			return
		case errors.Is(err, ErrBadCode), errors.Is(err, ErrNotEnrolled):
			h.log.WarnContext(r.Context(), "totp verify", "user", u.ID, "error", err)
			http.Redirect(w, r, "/admin/verify?bad=1", http.StatusSeeOther)
			return
		default:
			h.log.ErrorContext(r.Context(), "verify totp", "error", err)
			admin.Fault(w, r, h.log)
			return
		}
	}
	session, ok := user.Session(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := h.store.MarkVerified(r.Context(), session); err != nil {
		h.log.ErrorContext(r.Context(), "mark session verified", "error", err)
		admin.Fault(w, r, h.log)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h *Handler) Enrol(w http.ResponseWriter, r *http.Request) {
	// The one-time TOTP seed is password-equivalent; keep its page out of BREACH's reach.
	compress.Skip(w)
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	// Each enrolment mails the account.
	if retryAfter, allowed := h.limit.Allow("totp:" + u.ID); !allowed {
		ratelimit.Refuse(r.Context(), w, retryAfter)
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
		admin.Fault(w, r, h.log)
		return
	}
	// Rendered rather than redirected to: a redirect would have to carry the
	// secret in a URL, into the browser history and every access log.
	code, err := qr.Encode(uri, qr.M)
	if err != nil {
		h.log.ErrorContext(r.Context(), "encode enrolment QR", "error", err)
		admin.Fault(w, r, h.log)
		return
	}
	respond.Component(w, r, h.log, http.StatusOK, TwoFactor(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageTwoFactor)}, TwoFactorView{
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
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	if retryAfter, allowed := h.limit.Allow("totp:" + u.ID); !allowed {
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}
	if err := h.store.Confirm(r.Context(), u.ID, r.PostFormValue("code"), r.PostFormValue("mailed_code")); err != nil {
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
			admin.Fault(w, r, h.log)
		}
		return
	}
	session, ok := user.Session(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := h.store.MarkVerified(r.Context(), session); err != nil {
		h.log.ErrorContext(r.Context(), "mark session verified", "error", err)
		admin.Fault(w, r, h.log)
		return
	}
	http.Redirect(w, r, "/admin?enrolled=1", http.StatusSeeOther)
}

func noticeFor(r *http.Request) string {
	switch {
	case r.URL.Query().Get("stale") == "1":
		return i18n.T(r.Context(), i18n.KeyTOTPSecretUnreadable)
	case r.URL.Query().Get("bad") == "1":
		return fmt.Sprintf(i18n.T(r.Context(), i18n.KeyTOTPWrongCode), MaxFailures)
	case r.URL.Query().Get("locked") == "1":
		return i18n.T(r.Context(), i18n.KeyTOTPLocked)
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
	session, ok := user.Session(r.Context())
	if !ok {
		return false, nil
	}
	return h.store.SessionVerified(r.Context(), session)
}
