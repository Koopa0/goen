package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/auth/google"
	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/cart/cartitem"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web/clientip"
	"github.com/koopa0/goen/internal/web/compress"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/ratelimit"
	"github.com/koopa0/goen/internal/web/respond"
	"github.com/koopa0/goen/internal/web/siteurl"
)

// Carts is what auth needs of the cart: adopting the guest cart a request's
// cookie names when someone signs in, showing both carts when that fails, and
// forgetting that cookie when a session ends.
type Carts interface {
	// AdoptRequestCart reports whether a line came out smaller than the guest had
	// it or was left out. An error means the cart was not carried over and has
	// been logged.
	AdoptRequestCart(ctx context.Context, r *http.Request, userID string) (adjusted bool, err error)
	RecoveryContents(ctx context.Context, r *http.Request) (beforeSignIn, account []cartitem.Item, err error)
	ForgetCart(w http.ResponseWriter, r *http.Request)
}

// Grants is the order access a browser holds: the grants that open the pages
// of orders it placed, which a session's end takes away.
type Grants interface {
	Revoke(w http.ResponseWriter, r *http.Request) error
}

type Handler struct {
	// signinLimit bounds password guesses per account and client network, and
	// starting Google sign-in per client.
	signinLimit *ratelimit.Limiter
	// guessCeiling bounds, per account, the guesses that missed, from every
	// network together: the bound on a guess spread over many networks.
	guessCeiling *ratelimit.Limiter
	store        *Store
	carts        Carts
	grants       Grants
	log          *slog.Logger
	secure       bool
	google       *google.Client
	demo         DemoAccount
	// stepUp reports whether the request's session passed the second factor; nil
	// is a deployment with none. See [Handler.RequireStepUpFromStaff].
	stepUp func(*http.Request) (bool, error)
	// refuseOnAccountPage answers a refused request with the account page. See
	// [Handler.RefuseOnAccountPage].
	refuseOnAccountPage func(w http.ResponseWriter, r *http.Request, refusal AccountRefusal)

	// letterLimit bounds the letters one address is sent by each flow that
	// mails the address it is given, keyed by the flow and the address:
	// "register:", "resend:", "change:" and "forgot:". Each flow has an
	// allowance of its own and spends it only on the letters it sends. It never
	// refuses a request: once a flow has had its allowance, the request is
	// answered as if its letter were sent and nothing is queued, so the newest
	// letter already sent stays the live one. See [Handler.mayMail].
	letterLimit *ratelimit.Limiter
	// accountMailLimit bounds, per signed-in account, the letters it asks to be
	// sent to any address, and refuses visibly: only that account spends it.
	accountMailLimit *ratelimit.Limiter
}

func NewHandler(store *Store, carts Carts, grants Grants, log *slog.Logger, secure bool, googleSignIn *google.Client) *Handler {
	if store == nil || log == nil {
		panic("auth: NewHandler requires a store and a logger")
	}
	if googleSignIn == nil {
		googleSignIn = &google.Client{}
	}
	return &Handler{
		store: store, carts: carts, grants: grants, log: log, secure: secure, google: googleSignIn,
		// Six wrong passwords at once from one network, then one every fifteen
		// seconds: more than someone retyping needs.
		signinLimit: ratelimit.New(ratelimit.Config{
			Every: 15 * time.Second, Burst: 6, TTL: time.Hour, MaxKeys: 65_536,
		}),
		// Ten wrong passwords before the first refusal, then ten every fifteen
		// minutes, whoever sent them: a guess spread over many networks is
		// held to forty an hour per account. Only misses are charged, so the
		// owner's right password costs nothing, but the ceiling is shared: an
		// attacker who keeps it empty keeps every sign-in out until the
		// refill, which is the cost of bounding a distributed guess.
		guessCeiling: ratelimit.New(ratelimit.Config{
			Every: 90 * time.Second, Burst: 10, TTL: time.Hour, MaxKeys: 65_536,
		}),
		letterLimit:      ratelimit.New(addressMailPace),
		accountMailLimit: ratelimit.New(accountMailPace),
	}
}

// Routes registers signing in and out, registering, recovering an account,
// confirming an address, and the account doors that need a password or a
// session. limit bounds per client, before the handler runs, every route that
// verifies a password with argon2 or mails an address; the caller owns that
// budget, and one limiter across them all means moving between them earns no
// fresh allowance. clearSpeculations wraps the writes that change who the
// shared header shows, and clearCache adds emptying the browser's cache to
// signing out.
func (h *Handler) Routes(mux *http.ServeMux, limit *ratelimit.Limiter, clearSpeculations, clearCache func(http.HandlerFunc) http.HandlerFunc) {
	guard := func(next http.HandlerFunc) http.HandlerFunc { return ratelimit.Guard(limit, h.log, next) }
	mux.HandleFunc("GET /signin", h.SignInPage)
	// GET on both: the callback is a redirect from Google whose method goen does
	// not choose, and nothing about an account changes until the callback has
	// matched the state it issued.
	mux.HandleFunc("GET /auth/google", h.GoogleSignIn)
	mux.HandleFunc("GET /auth/google/callback", h.GoogleCallback)
	// The limiter runs before argon2 does: at 64 MiB a hash, an unbounded
	// endpoint is a memory exhaustion anybody can trigger.
	mux.HandleFunc("POST /signin", clearSpeculations(guard(h.SignIn)))
	mux.HandleFunc("GET /forgot", h.ForgotPage)
	mux.HandleFunc("POST /forgot", guard(h.Forgot))
	// RequireUser's sign-in return URL would issue GET to this POST-only action.
	mux.HandleFunc("POST /account/password/setup", guard(h.RequestPasswordSetup))
	mux.HandleFunc("GET /reset", h.ResetPage)
	mux.HandleFunc("POST /reset", guard(h.Reset))
	// Open to a signed-out visitor, who is sent on rather than refused: a
	// registration link to the page that asks for its password, and a new
	// address's link to sign in first. The token proves only the mailbox, and
	// the address goes to the account that asked for it.
	mux.HandleFunc("GET /verify", h.VerifyPage)
	mux.HandleFunc("POST /verify", guard(h.Verify))
	mux.HandleFunc("GET /register", h.RegisterPage)
	mux.HandleFunc("POST /register", clearSpeculations(guard(h.Register)))
	mux.HandleFunc("POST /register/resend", guard(h.ResendRegistration))
	// Open to a signed-out visitor: the link and the password chosen at
	// registration are the proof. Under limit because it runs argon2, and it
	// signs in, so it clears speculations.
	mux.HandleFunc("GET /register/complete", h.CompleteRegistrationPage)
	mux.HandleFunc("POST /register/complete", clearSpeculations(guard(h.CompleteRegistration)))
	mux.HandleFunc("POST /signout", clearSpeculations(clearCache(h.SignOut)))
	mux.HandleFunc("GET /account/cart-recovery", h.RequireUser(h.CartRecoveryPage))
	mux.HandleFunc("POST /account/cart/retry", h.RequireUser(h.RetryCartAdoption))
	mux.HandleFunc("POST /account/sessions/others/end", h.RequireUser(h.SignOutOtherDevices))
	// Also guarded: these verify a password with argon2, so a signed-in session
	// is otherwise an unbounded supply of 64 MiB hashes.
	mux.HandleFunc("POST /account/email", guard(h.RequireUser(h.ChangeEmail)))
	mux.HandleFunc("POST /account/email/resend", guard(h.RequireUser(h.ResendVerification)))
	mux.HandleFunc("POST /account/password", guard(h.RequireUser(h.ChangePassword)))
	mux.HandleFunc("POST /account/erase", h.RequireUser(h.Erase))
	mux.HandleFunc("POST /account/google/unlink", h.RequireUser(h.UnlinkGoogle))
}

// OfferDemoAccount must be called before the handler serves.
func (h *Handler) OfferDemoAccount(d DemoAccount) { h.demo = d }

// RequireStepUpFromStaff makes the doors that end, replace or re-credential an
// account (erase, password change, sign out of other devices, Google unlink)
// refuse a staff account whose session has not passed the second factor, and
// send it to /admin/verify as /admin does. stepUp is the back office's own
// predicate; nil, a deployment with no second factor, turns the check off, as it
// does for /admin. A customer is never asked. It must be called before the
// handler serves.
//
// The password alone opens /account, so without this the second factor guards
// only /admin while the same users row could be erased or locked out from here.
func (h *Handler) RequireStepUpFromStaff(stepUp func(*http.Request) (bool, error)) {
	h.stepUp = stepUp
}

// RefuseOnAccountPage names the page a refused request from the account page
// is answered with: package account renders it at status 422, with the
// refusal's section open, its messages under their controls and what was typed
// put back, and auth may not import account. It must be called before the
// handler serves.
func (h *Handler) RefuseOnAccountPage(refuse func(w http.ResponseWriter, r *http.Request, refusal AccountRefusal)) {
	h.refuseOnAccountPage = refuse
}

// stepUpPath is where /admin sends a session that has not passed the second
// factor. It is the back office's route, named here because auth may not
// import the back office.
const stepUpPath = "/admin/verify"

// refuseUnverifiedStaff answers a staff account whose session has not passed
// the second factor and reports that it did; the caller returns without acting.
// A customer, and every account while no second factor is configured, passes.
func (h *Handler) refuseUnverifiedStaff(w http.ResponseWriter, r *http.Request, u user.User) bool {
	if !u.IsStaff() || h.stepUp == nil {
		return false
	}
	verified, err := h.stepUp(r)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read second factor", "error", err)
		h.serverError(w, r)
		return true
	}
	if verified {
		return false
	}
	http.Redirect(w, r, stepUpPath, http.StatusSeeOther)
	return true
}

func (h *Handler) refuseDemoChange(w http.ResponseWriter, r *http.Request, u user.User, form AccountForm) bool {
	if !h.demo.holds(u.Email) {
		return false
	}
	h.refuseOnAccountPage(w, r, AccountRefusal{Form: form, Notice: i18n.T(r.Context(), i18n.KeyDemoAccountFixed)})
	return true
}

// addressMailPace is three at once, then one every ten minutes: more than
// somebody retyping or asking again needs.
var addressMailPace = ratelimit.Config{
	Every: 10 * time.Minute, Burst: 3, TTL: time.Hour, MaxKeys: 65_536,
}

// accountMailPace is three at once, then one every twenty minutes: slower
// than addressMailPace on purpose, so the address's allowance is what bounds
// one account's letters to one address.
var accountMailPace = ratelimit.Config{
	Every: 20 * time.Minute, Burst: 3, TTL: time.Hour, MaxKeys: 65_536,
}

// mayMail reports whether flow may send addr another letter, and spends one of
// its allowance if so. When it may not, the caller answers as if the letter
// were sent and queues nothing. attrs go into the log line, which never names
// the address.
func (h *Handler) mayMail(ctx context.Context, flow, addr string, attrs ...any) bool {
	if _, ok := h.letterLimit.Allow(flow + ":" + email.Clean(addr)); ok {
		return true
	}
	h.letterWithheld(ctx, flow, attrs...)
	return false
}

func (h *Handler) letterWithheld(ctx context.Context, flow string, attrs ...any) {
	h.log.WarnContext(ctx, "letter not queued: the address has had its allowance",
		append([]any{"flow", flow}, attrs...)...)
}

func (h *Handler) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ReadSessionCookie(r, h.secure)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, err := h.store.SessionUser(r.Context(), token)
		if err != nil {
			// The cookie is cleared only when the session is genuinely GONE. A
			// database that cannot answer has not said the session is invalid,
			// and clearing on any error signs everybody out at once during a
			// blip, unrecoverably: the row survives and the browser no longer
			// holds its token.
			if errors.Is(err, ErrNotFound) {
				h.forgetSession(w, r)
			} else {
				h.log.ErrorContext(r.Context(), "read session", "error", err)
			}
			next.ServeHTTP(w, r)
			return
		}
		ctx := user.WithSession(user.NewContext(r.Context(), u), bearer.Digest(token))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *Handler) RequireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := user.FromContext(r.Context()); !ok {
			http.Redirect(w, r, "/signin?next="+urlQueryEscape(r.URL.Path), http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (h *Handler) SignInPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := user.FromContext(r.Context()); ok {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	// next can be a live link back to /verify; never compress it.
	compress.Skip(w)
	view := h.signInView(View{
		Next:          siteurl.PathOr(r.URL.Query().Get("next"), "/account"),
		ReturnMessage: signInReturnMessage(r.Context(), r.URL.Query().Get("next")),
		GoogleSignIn:  h.google.Enabled(),
	})
	switch {
	case r.URL.Query().Get("reset") == "1":
		view.Notice = i18n.T(r.Context(), i18n.KeyPasswordReset)
	case r.URL.Query().Get("changed") == "1":
		view.Notice = i18n.T(r.Context(), i18n.KeyPasswordChanged)
	case r.URL.Query().Get("reauth") == "erase":
		view.Notice = i18n.T(r.Context(), i18n.KeyEraseNeedsRecentSignIn)
		view.HideRegister = true
	default:
		view.Errors = oauthOutcome(r.Context(), r.URL.Query().Get("oauth"))
		switch r.URL.Query().Get("oauth") {
		case "collision":
			view.Recovery = SignInResetPassword
		case "unverified":
			view.Recovery = SignInRegister
		}
	}
	purpose, address := h.takeSignInContext(w, r)
	if purpose == signInAfterReset && r.URL.Query().Get("reset") == "1" ||
		purpose == signInBeforeErasure && r.URL.Query().Get("reauth") == "erase" {
		view.Email = address
		view.PasswordFocus = view.Email != ""
	}
	respond.Component(w, r, h.log, http.StatusOK, SignIn(SignInMeta(r.Context()), view))
}

func (h *Handler) SignIn(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	addr := r.PostFormValue("email")
	password := r.PostFormValue("password")
	next := siteurl.PathOr(r.PostFormValue("next"), "/account")
	normalised := email.Clean(addr)
	// email.MaxBytes is the application's address policy; the column is text, so
	// the handler must bound the value before it becomes a long-lived limiter key.
	if len(normalised) > email.MaxBytes {
		h.signInFailed(w, r, addr, next)
		return
	}

	guess, device, ok := h.reserveSignIn(w, r, normalised)
	if !ok {
		return
	}
	defer guess.settle(false)

	u, err := h.store.Authenticate(r.Context(), addr, password)
	if err != nil {
		if !errors.Is(err, ErrBadCredentials) {
			h.log.ErrorContext(r.Context(), "authenticate", "error", err)
			h.serverError(w, r)
			return
		}
		guess.settle(true)
		h.countDeviceMiss(r, device)
		h.signInFailed(w, r, addr, next)
		return
	}

	started, adoption := h.startSession(w, r, u)
	if !started {
		return
	}
	next = cartAdoptionLanding(next, adoption)
	http.Redirect(w, r, next, http.StatusSeeOther) //nolint:gosec // G710: bounded by siteurl.PathOr
}

// reserveSignIn sets aside one password guess for address before the hash,
// and answers 429 itself when there is none to set aside. argon2 at 64 MiB is
// the cost this protects. A browser remembered for the account is charged to
// its own budget (device, the row's digest); every other browser to the
// network's and the account's. The demo account's password is printed on the
// sign-in page, so a per-account bound would guard nothing and let one
// visitor's mistakes shut out every other; the per-client bound still holds
// the cost.
func (h *Handler) reserveSignIn(w http.ResponseWriter, r *http.Request, address string) (passwordGuess, []byte, bool) {
	if h.demo.holds(address) {
		return nil, nil, true
	}
	device, err := h.store.knownDevice(r.Context(), readDeviceCookie(r, h.secure), address)
	if err != nil {
		// Not knowing the browser costs it only the stranger's budgets.
		h.log.ErrorContext(r.Context(), "read known device", "error", err)
	}
	guess, retryAfter, ok := h.reservePasswordGuess(r, address, device)
	if !ok {
		by := "account"
		if device != nil {
			by = "device"
		}
		h.log.WarnContext(r.Context(), "sign-in throttled by "+by)
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return nil, nil, false
	}
	return guess, device, true
}

// countDeviceMiss records a wrong password sent by a remembered browser. A
// failure is logged: the miss was already charged to the browser's budget.
func (h *Handler) countDeviceMiss(r *http.Request, device []byte) {
	if device == nil {
		return
	}
	if err := h.store.countDeviceMiss(r.Context(), device); err != nil {
		h.log.ErrorContext(r.Context(), "count known device miss", "error", err)
	}
}

// signInFailed is one response for an unusable address, an unknown account and
// a wrong password, so two submissions never differ in information about an
// account.
func (h *Handler) signInFailed(w http.ResponseWriter, r *http.Request, addr, next string) {
	compress.Skip(w)
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity,
		SignIn(SignInMeta(r.Context()), h.signInView(View{
			Email: addr, Next: next, ReturnMessage: signInReturnMessage(r.Context(), r.PostFormValue("next")),
			HideRegister: r.PostFormValue("reauth") == "erase", PasswordFocus: true,
			GoogleSignIn: h.google.Enabled(),
			Errors:       map[string]string{"form": i18n.T(r.Context(), i18n.KeyBadCredentials)},
		})))
}

type signInPurpose string

const (
	signInAfterReset    signInPurpose = "reset"
	signInBeforeErasure signInPurpose = "erase"
)

func (h *Handler) signInContextCookie() string {
	if h.secure {
		return "__Host-goen_signin_context"
	}
	return "goen_signin_context"
}

// This cookie carries a short-lived input suggestion, never sign-in authority.
func (h *Handler) writeSignInContext(w http.ResponseWriter, purpose signInPurpose, address string) {
	address = email.Clean(address)
	if len(address) > email.MaxBytes || !email.Valid(address) {
		return
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: development-only secure opt-out follows the session cookie.
		Name: h.signInContextCookie(), Value: string(purpose) + ":" + base64.RawURLEncoding.EncodeToString([]byte(address)),
		Path: "/", MaxAge: 120, HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) takeSignInContext(w http.ResponseWriter, r *http.Request) (purpose signInPurpose, address string) {
	cookie, err := r.Cookie(h.signInContextCookie())
	if err != nil {
		return "", ""
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: clear with the same attributes that created it.
		Name: h.signInContextCookie(), Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode,
	})
	if len(cookie.Value) > 512 {
		return "", ""
	}
	kind, encoded, ok := strings.Cut(cookie.Value, ":")
	purpose = signInPurpose(kind)
	if !ok || purpose != signInAfterReset && purpose != signInBeforeErasure {
		return "", ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) > email.MaxBytes || !email.Valid(string(decoded)) {
		return "", ""
	}
	return purpose, string(decoded)
}

func signInReturnMessage(ctx context.Context, next string) string {
	target, ok := siteurl.Path(next)
	if !ok {
		return ""
	}
	path, err := url.Parse(target)
	if err != nil {
		return ""
	}
	key := i18n.KeySignInReturnPage
	switch {
	case path.Path == "/account/wishlist":
		key = i18n.KeySignInReturnWishlist
	case path.Path == "/checkout":
		key = i18n.KeySignInReturnCheckout
	case path.Path == "/cart":
		key = i18n.KeySignInReturnCart
	case path.Path == "/account":
		key = i18n.KeySignInReturnAccount
	case strings.HasPrefix(path.Path, "/p/"):
		if path.Fragment == "wishlist" {
			return fmt.Sprintf(i18n.T(ctx, i18n.KeySignInReturnProductWishlist), i18n.T(ctx, i18n.KeyWishlistAdd))
		}
		key = i18n.KeySignInReturnProduct
	}
	return i18n.T(ctx, key)
}

func (h *Handler) signInView(v View) View {
	v.DemoEmail, v.DemoPassword = h.demo.email, h.demo.password
	return v
}

func (h *Handler) RegisterPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := user.FromContext(r.Context()); ok {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	q := r.URL.Query()
	view := View{Next: siteurl.PathOr(q.Get("next"), "/account")}
	view.OffersResend = q.Get("resend") == "1" || q.Get("sent") == "1"
	if q.Get("sent") == "1" {
		view.Notice = i18n.T(r.Context(), i18n.KeyRegisterSent)
		// Not in the URL: history and proxy logs keep URLs.
		if addr, next, ok := readPendingRegistration(r); ok {
			view.Email, view.Next = addr, next
			view.Notice = fmt.Sprintf(i18n.T(r.Context(), i18n.KeyRegisterSentTo), addr)
		}
		if q.Get("again") == "1" {
			view.Notice = i18n.T(r.Context(), i18n.KeyRegisterResent)
		}
	}
	respond.Component(w, r, h.log, http.StatusOK, Register(RegisterMeta(r.Context()), view))
}

// ResendRegistration gives every usable address the same answer, so the form
// cannot be asked who has an account.
func (h *Handler) ResendRegistration(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	addr := email.Clean(r.PostFormValue("email"))
	next := siteurl.PathOr(r.PostFormValue("next"), "/account")
	if EmailError(addr) != "" {
		http.Redirect(w, r, "/register?"+url.Values{"next": {next}}.Encode(), http.StatusSeeOther)
		return
	}
	if err := h.queueResend(r.Context(), addr, next); err != nil {
		h.log.ErrorContext(r.Context(), "resend registration", "error", err)
		h.serverError(w, r)
		return
	}
	writePendingRegistration(w, addr, next, h.secure)
	http.Redirect(w, r, "/register?sent=1&again=1", http.StatusSeeOther)
}

// queueResend queues another registration link for addr unless the address
// has had its allowance of them, and either way the caller answers that it is
// on its way. Only a resend that sends a link spends the allowance: one is
// reserved before the store is asked, since a link queued now would replace
// the live one even when it is never sent, and given back unless the store
// named an account to send it to.
func (h *Handler) queueResend(ctx context.Context, addr, next string) error {
	reserved, _, ok := h.letterLimit.Reserve("resend:" + addr)
	if !ok {
		h.letterWithheld(ctx, "resend")
		return nil
	}
	named, err := h.store.ResendRegistration(ctx, addr, next)
	if err != nil || !named {
		reserved.Refund()
		return err
	}
	reserved.Keep()
	return nil
}

// The address is personal data on a page nobody signs in to, so it expires with
// the visit.
const pendingRegistrationTTL = 10 * time.Minute

const pendingRegistrationCookie = "goen_register_sent"

func writePendingRegistration(w http.ResponseWriter, addr, next string, secure bool) {
	raw := base64.RawURLEncoding.EncodeToString([]byte(addr + "|" + next))
	//nolint:gosec // G124: Secure follows the deployment's own flag, as every cookie here does
	http.SetCookie(w, &http.Cookie{
		Name: pendingRegistrationCookie, Value: raw, Path: "/register",
		MaxAge:   int(pendingRegistrationTTL.Seconds()),
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

func readPendingRegistration(r *http.Request) (addr, next string, ok bool) {
	c, err := r.Cookie(pendingRegistrationCookie)
	if err != nil {
		return "", "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return "", "", false
	}
	addr, next, found := strings.Cut(string(raw), "|")
	if !found || EmailError(email.Clean(addr)) != "" {
		return "", "", false
	}
	return email.Clean(addr), siteurl.PathOr(next, "/account"), true
}

func clearPendingRegistration(w http.ResponseWriter, secure bool) {
	//nolint:gosec // G124: as above
	http.SetCookie(w, &http.Cookie{
		Name: pendingRegistrationCookie, Value: "", Path: "/register", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

// Register answers every usable submission the same way, whether or not the
// address already has an account: the mailbox is told which, never the visitor.
// A refusal is only ever about what was typed.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	c := &Credentials{
		Email:    r.PostFormValue("email"),
		Password: r.PostFormValue("password"),
		Confirm:  r.PostFormValue("confirm"),
		Name:     r.PostFormValue("name"),
	}
	c.Trim()
	next := siteurl.PathOr(r.PostFormValue("next"), "/account")

	view := View{Email: c.Email, Name: c.Name, Next: next}
	if errs := formtext.FieldMessages(r.Context(), c.ValidateRegistration()); len(errs) > 0 {
		view.Errors = errs
		respond.Component(w, r, h.log, http.StatusUnprocessableEntity,
			Register(RegisterMeta(r.Context()), view))
		return
	}

	// After validation, so only an address the rules accept becomes a key. Every
	// registration mails the address it names, so each spends its allowance.
	if h.mayMail(r.Context(), "register", c.Email) {
		if err := h.store.Register(r.Context(), c, next); err != nil {
			h.log.ErrorContext(r.Context(), "register", "error", err)
			h.serverError(w, r)
			return
		}
	}
	writePendingRegistration(w, email.Clean(c.Email), next, h.secure)
	http.Redirect(w, r, "/register?sent=1", http.StatusSeeOther)
}

func (h *Handler) SignOutOtherDevices(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	currentToken := ReadSessionCookie(r, h.secure)
	if !ok || currentToken == "" {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if h.refuseUnverifiedStaff(w, r, u) {
		return
	}
	if err := h.store.endOtherSessions(r.Context(), u.ID, currentToken, readDeviceCookie(r, h.secure)); err != nil {
		h.log.ErrorContext(r.Context(), "end other sessions", "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/account?other_devices=signed_out", http.StatusSeeOther)
}

func (h *Handler) SignOut(w http.ResponseWriter, r *http.Request) {
	if err := h.store.EndSession(r.Context(), ReadSessionCookie(r, h.secure)); err != nil {
		h.log.ErrorContext(r.Context(), "end session", "error", err)
	}
	h.forgetSession(w, r)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// forgetSession also runs when a session ends without its browser signing out
// (expired, or ended from another device): the next person at that browser must
// inherit nothing.
func (h *Handler) forgetSession(w http.ResponseWriter, r *http.Request) {
	ClearSessionCookie(w, h.secure)
	if h.carts != nil {
		h.carts.ForgetCart(w, r)
	}
	// The grants are revoked as well as their cookie expired, because a client
	// can ignore an expiry. A failure to revoke is logged rather than stopping
	// the session's end.
	if h.grants != nil {
		if err := h.grants.Revoke(w, r); err != nil {
			h.log.ErrorContext(r.Context(), "forget this browser's orders", "error", err)
		}
	}
}

// Notice is what the account page says after one of the changes here sent the
// customer back to it, named in the query of the redirect. The account page's
// own notices come first; this is the rest. A change that was refused reports
// [widget.OutcomeRefused], so the page shows it as a refusal; the zero Result
// means nothing applies.
func Notice(r *http.Request) widget.Result {
	ctx := r.Context()
	q := r.URL.Query()
	refused := func(key i18n.Key) widget.Result {
		return widget.Result{Outcome: widget.OutcomeRefused, Text: i18n.T(ctx, key)}
	}
	done := func(text string) widget.Result {
		return widget.Result{Outcome: widget.OutcomeDone, Text: text}
	}
	switch {
	case q.Get("email") == "sent":
		return done(emailSentNotice(ctx, q))
	case q.Get("unlinked") == "1":
		return done(i18n.T(ctx, i18n.KeyGoogleUnlinked))
	case q.Get("lastmethod") == "1":
		return refused(i18n.KeyGoogleLastMethod)
	}
	return otherDevicesNotice(r)
}

func otherDevicesNotice(r *http.Request) widget.Result {
	if r.URL.Query().Get("other_devices") == "signed_out" {
		return widget.Result{Outcome: widget.OutcomeDone, Text: i18n.T(r.Context(), i18n.KeyOtherDevicesSignedOut)}
	}
	return widget.Result{}
}

func emailSentNotice(ctx context.Context, q url.Values) string {
	if address := email.ReadMasked(q.Get("address")); address != "" {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyEmailSentTo), address)
	}
	return i18n.T(ctx, i18n.KeyEmailSent)
}

func (h *Handler) CartRecoveryPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := user.FromContext(r.Context()); !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	var beforeSignIn, account []cartitem.Item
	if h.carts != nil {
		var err error
		beforeSignIn, account, err = h.carts.RecoveryContents(r.Context(), r)
		if err != nil {
			h.log.ErrorContext(r.Context(), "read carts for recovery", "error", err)
			h.serverError(w, r)
			return
		}
	}
	next := siteurl.PathOr(r.URL.Query().Get("next"), "/account")
	respond.Component(w, r, h.log, http.StatusOK, CartRecovery(
		CartRecoveryMeta(r.Context()), CartRecoveryView{
			Next:    next,
			Welcome: r.URL.Query().Get("welcome") == "1",
			Notice:  widget.Result{Outcome: widget.OutcomeFailed, Text: i18n.T(r.Context(), i18n.KeyCartMergeFailed)},
			Retry:   i18n.T(r.Context(), i18n.KeyCartMergeRetry),
			Contents: CartRecoveryContents{
				BeforeSignIn: beforeSignIn,
				Account:      account,
			},
		}))
}

func (h *Handler) RetryCartAdoption(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	next := siteurl.PathOr(r.PostFormValue("next"), "/account")
	next = cartAdoptionLanding(next, h.adoptRequestCart(r, u.ID))
	http.Redirect(w, r, next, http.StatusSeeOther) //nolint:gosec // G710: bounded by siteurl.PathOr
}

type cartAdoption uint8

const (
	cartAdoptionUnchanged cartAdoption = iota
	cartAdoptionAdjusted
	cartAdoptionFailed
)

func (h *Handler) startSession(w http.ResponseWriter, r *http.Request, u user.User) (started bool, adoption cartAdoption) {
	token, err := h.store.StartSession(r.Context(), u.ID, r.UserAgent(), clientip.From(r))
	if err != nil {
		h.log.ErrorContext(r.Context(), "start session", "error", err)
		h.serverError(w, r)
		return false, cartAdoptionUnchanged
	}
	SetSessionCookie(w, token, h.secure)
	h.rememberBrowser(w, r, u.ID)
	// Authentication succeeds even when the preserved guest cart needs recovery.
	return true, h.adoptRequestCart(r, u.ID)
}

// rememberBrowser replaces the browser's remembered row with a new one for the
// account it just signed in to. A failure is logged and the sign-in stands: the
// browser is then only a stranger to the guess budgets until it signs in again.
func (h *Handler) rememberBrowser(w http.ResponseWriter, r *http.Request, userID string) {
	token, err := h.store.rememberDevice(r.Context(), userID, readDeviceCookie(r, h.secure))
	if err != nil {
		h.log.ErrorContext(r.Context(), "remember this browser", "error", err)
		return
	}
	SetDeviceCookie(w, token, h.secure)
}

func (h *Handler) adoptRequestCart(r *http.Request, userID string) cartAdoption {
	if h.carts == nil {
		return cartAdoptionUnchanged
	}
	adjusted, err := h.carts.AdoptRequestCart(r.Context(), r, userID)
	switch {
	case err != nil:
		return cartAdoptionFailed
	case adjusted:
		return cartAdoptionAdjusted
	default:
		return cartAdoptionUnchanged
	}
}

func registrationLanding(next string, outcome cartAdoption) string {
	next = siteurl.PathOr(next, "/account")
	query := url.Values{"welcome": {"1"}}
	if outcome == cartAdoptionAdjusted {
		next = appendCartAdjustNotice(next)
		query.Set("adjusted", "1")
	}
	if next != "/account" {
		query.Set("next", next)
	}
	welcome := "/account?" + query.Encode()
	if outcome == cartAdoptionFailed {
		return cartRecoveryLanding(welcome) + "&welcome=1"
	}
	return welcome
}

func cartAdoptionLanding(next string, outcome cartAdoption) string {
	switch outcome {
	case cartAdoptionAdjusted:
		return appendCartAdjustNotice(next)
	case cartAdoptionFailed:
		return cartRecoveryLanding(next)
	default:
		return next
	}
}

func appendCartAdjustNotice(target string) string {
	next := siteurl.PathOr(target, "/account")
	u, err := url.Parse(next)
	if err != nil {
		u = &url.URL{Path: "/account"}
		next = "/account"
	}
	// Show the changed quantities before continuing to a page without a cart notice.
	if u.Path != "/cart" {
		u = &url.URL{Path: "/cart", RawQuery: url.Values{"next": {next}}.Encode()}
	}
	q := u.Query()
	q.Set("qty", "adjusted")
	u.RawQuery = q.Encode()
	return u.String()
}

// SignInPath is the sign-in page that returns to next once someone has signed in.
func SignInPath(next string) string {
	return "/signin?next=" + urlQueryEscape(next)
}

func urlQueryEscape(s string) string {
	var b []byte
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~', c == '/':
			b = append(b, c)
		default:
			const hex = "0123456789ABCDEF"
			b = append(b, '%', hex[c>>4], hex[c&0x0F])
		}
	}
	return string(b)
}

func cartRecoveryLanding(continuation string) string {
	next := siteurl.PathOr(continuation, "/account")
	u := &url.URL{Path: "/account/cart-recovery"}
	q := u.Query()
	q.Set("next", next)
	u.RawQuery = q.Encode()
	return u.String()
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	respond.Component(w, r, h.log, http.StatusInternalServerError, frame.Notice(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyBusyTitle)}, "",
		i18n.T(r.Context(), i18n.KeyBusyTitle),
		i18n.T(r.Context(), i18n.KeyBusyBody)))
}

func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if h.refuseDemoChange(w, r, u, PasswordForm) || h.refuseUnverifiedStaff(w, r, u) {
		return
	}
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	// Every refused control is named at once, so a wrong current password does
	// not hide a new password that also fails.
	var refusals []formtext.FieldRefusal
	if err := h.store.ConfirmPassword(r.Context(), u.Email, r.PostFormValue("current")); err != nil {
		if !errors.Is(err, ErrBadCredentials) {
			h.log.ErrorContext(r.Context(), "confirm password change", "error", err)
			h.serverError(w, r)
			return
		}
		refusals = append(refusals, formtext.FieldRefusal{Field: "current", MessageKey: i18n.KeyWrongCurrentPassword})
	}

	next := r.PostFormValue("password")
	if refusal, refused := PasswordRefusal("password", next); refused {
		refusals = append(refusals, refusal)
	} else if next != r.PostFormValue("confirm") {
		refusals = append(refusals, formtext.FieldRefusal{Field: "confirm", MessageKey: i18n.KeyPasswordsDiffer})
	}
	if len(refusals) > 0 {
		h.refuseOnAccountPage(w, r, AccountRefusal{Form: PasswordForm, Errors: formtext.FieldMessages(r.Context(), refusals)})
		return
	}

	if err := h.store.ChangePassword(r.Context(), u.ID, next); err != nil {
		h.log.ErrorContext(r.Context(), "change password", "error", err)
		h.serverError(w, r)
		return
	}
	ClearSessionCookie(w, h.secure)
	http.Redirect(w, r, "/signin?changed=1", http.StatusSeeOther)
}

func (h *Handler) Erase(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	// Before the recency check, which would end the session rather than refuse.
	if h.refuseDemoChange(w, r, u, EraseForm) || h.refuseUnverifiedStaff(w, r, u) {
		return
	}
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	token := ReadSessionCookie(r, h.secure)
	recent, err := h.store.SignedInRecently(r.Context(), token, EraseSignInWindow)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read session age", "error", err)
		h.serverError(w, r)
		return
	}
	if !recent {
		// The session ends first: /signin sends a signed-in visitor straight back
		// to /account, so keeping it would loop instead of asking for a sign-in.
		if err := h.store.EndSession(r.Context(), token); err != nil {
			h.log.ErrorContext(r.Context(), "end stale session", "error", err)
		}
		h.forgetSession(w, r)
		h.writeSignInContext(w, signInBeforeErasure, u.Email)
		http.Redirect(w, r, "/signin?next=%2Faccount&reauth=erase", http.StatusSeeOther)
		return
	}
	if typed := r.PostFormValue("confirm"); typed != u.Email {
		h.refuseOnAccountPage(w, r, AccountRefusal{
			Form:   EraseForm,
			Errors: map[string]string{"confirm": i18n.T(r.Context(), i18n.KeyEraseNeedsEmail)},
			Values: map[string]string{"confirm": typed},
		})
		return
	}
	switch err := h.store.Erase(r.Context(), u.ID); {
	case errors.Is(err, ErrOpenReturn):
		h.refuseOnAccountPage(w, r, AccountRefusal{
			Form: EraseForm, Notice: i18n.T(r.Context(), i18n.KeyEraseOpenReturn), OpenReturn: true,
		})
		return
	case errors.Is(err, ErrLastAdmin):
		h.refuseOnAccountPage(w, r, AccountRefusal{Form: EraseForm, Notice: i18n.T(r.Context(), i18n.KeyEraseLastAdmin)})
		return
	case err != nil:
		h.log.ErrorContext(r.Context(), "erase account", "error", err)
		h.serverError(w, r)
		return
	}
	ClearSessionCookie(w, h.secure)
	http.Redirect(w, r, "/?erased=1", http.StatusSeeOther)
}

func (h *Handler) ChangeEmail(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if h.refuseDemoChange(w, r, u, EmailForm) {
		return
	}
	if u.IsStaff() {
		h.refuseOnAccountPage(w, r, AccountRefusal{Form: EmailForm, Notice: i18n.T(r.Context(), i18n.KeyEmailStaffFixed)})
		return
	}
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	var refusals []formtext.FieldRefusal
	if err := h.store.ConfirmPassword(r.Context(), u.Email, r.PostFormValue("current")); err != nil {
		if !errors.Is(err, ErrBadCredentials) {
			h.log.ErrorContext(r.Context(), "confirm email change", "error", err)
			h.serverError(w, r)
			return
		}
		refusals = append(refusals, formtext.FieldRefusal{Field: "current", MessageKey: i18n.KeyWrongCurrentPassword})
	}

	addr := r.PostFormValue("email")
	if k := EmailError(addr); k != "" {
		refusals = append(refusals, formtext.FieldRefusal{Field: "email", MessageKey: k})
	}
	if len(refusals) > 0 {
		h.refuseOnAccountPage(w, r, AccountRefusal{
			Form:   EmailForm,
			Errors: formtext.FieldMessages(r.Context(), refusals),
			Values: map[string]string{"email": addr},
		})
		return
	}
	// After validation, so only an address the rules accept becomes a key.
	if retryAfter, ok := h.accountMailLimit.Allow("account:" + u.ID); !ok {
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}

	// The same answer whether or not the address has an account, or has had its
	// allowance: only the mailbox is told which, by the outbox worker.
	if h.mayMail(r.Context(), "change", addr, "user_id", u.ID) {
		if err := h.store.requestVerification(r.Context(), u.ID, addr); err != nil {
			h.log.ErrorContext(r.Context(), "request email verification", "error", err)
			h.serverError(w, r)
			return
		}
	}
	// The form posts to its section, which a redirect without a fragment keeps;
	// the sentence it sends back is at the top.
	http.Redirect(w, r, "/account?"+url.Values{"email": {"sent"}, "address": {email.Mask(addr)}}.Encode()+"#account-notice", http.StatusSeeOther)
}

func (h *Handler) ResendVerification(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if h.refuseDemoChange(w, r, u, EmailForm) {
		return
	}
	if retryAfter, ok := h.accountMailLimit.Allow("account:" + u.ID); !ok {
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}
	if err := h.store.requestVerification(r.Context(), u.ID, u.Email); err != nil {
		h.log.ErrorContext(r.Context(), "resend email verification", "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/account?"+url.Values{"email": {"sent"}, "address": {email.Mask(u.Email)}}.Encode()+"#account-notice", http.StatusSeeOther)
}

func (h *Handler) VerifyPage(w http.ResponseWriter, r *http.Request) {
	// The live address-verification token changes identity data; never compress it.
	compress.Skip(w)
	ctx := r.Context()
	if r.URL.Query().Get("done") == "1" {
		respond.Component(w, r, h.log, http.StatusOK, frame.EmailLinkPage(
			frame.EmailLinkMeta(i18n.T(ctx, i18n.KeyVerifyDone)),
			frame.EmailLinkView{
				Heading: i18n.T(ctx, i18n.KeyVerifyDone),
				Body:    i18n.T(ctx, i18n.KeyVerifyDoneBody),
			}))
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		respond.Component(w, r, h.log, http.StatusOK, frame.EmailLinkPage(
			frame.EmailLinkMeta(i18n.T(ctx, i18n.KeyVerifyTitle)),
			frame.EmailLinkView{
				Heading:  i18n.T(ctx, i18n.KeyVerifyTitle),
				Body:     i18n.T(ctx, i18n.KeyEmailLinkIncomplete),
				Recovery: frame.EmailLinkVerify,
			}))
		return
	}
	respond.Component(w, r, h.log, http.StatusOK, frame.EmailLinkPage(
		frame.EmailLinkMeta(i18n.T(ctx, i18n.KeyVerifyTitle)),
		frame.EmailLinkView{
			Heading: i18n.T(ctx, i18n.KeyVerifyTitle),
			Body:    i18n.T(ctx, i18n.KeyVerifyBody),
			Action:  "/verify",
			Submit:  i18n.T(ctx, i18n.KeyVerifySubmit),
			Token:   token,
		}))
}

func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	ctx := r.Context()

	token := r.PostFormValue("token")
	var asker string
	if u, ok := user.FromContext(ctx); ok {
		asker = u.ID
	}
	_, err := h.store.ConfirmVerification(ctx, token, asker)
	switch {
	case errors.Is(err, ErrVerifyNeedsPassword):
		// A registration link reached the page for proving an address; it is
		// completed only with the password chosen at registration.
		http.Redirect(w, r, "/register/complete?"+url.Values{"token": {token}}.Encode(), http.StatusSeeOther)
	case errors.Is(err, ErrVerifyNeedsSignIn):
		// The address goes only to the account that asked for it, so the link
		// is followed again once that account is signed in.
		back := "/verify?" + url.Values{"token": {token}}.Encode()
		http.Redirect(w, r, "/signin?"+url.Values{"next": {back}}.Encode(), http.StatusSeeOther)
	case err == nil:
		http.Redirect(w, r, "/verify?done=1", http.StatusSeeOther)
	case errors.Is(err, ErrEmailTaken):
		h.verifyFailed(w, r, i18n.T(ctx, i18n.KeyVerifyTakenTitle), i18n.T(ctx, i18n.KeyVerifyTakenBody))
	case errors.Is(err, ErrStaffAddress):
		h.verifyFailed(w, r, i18n.T(ctx, i18n.KeyVerifyDeadTitle), i18n.T(ctx, i18n.KeyEmailStaffFixed))
	case errors.Is(err, ErrVerifyInvalid):
		heading := i18n.T(ctx, i18n.KeyEmailLinkDeadTitle)
		respond.Component(w, r, h.log, http.StatusUnprocessableEntity, frame.EmailLinkPage(
			frame.EmailLinkMeta(heading),
			frame.EmailLinkView{
				Heading:  heading,
				Body:     i18n.T(ctx, i18n.KeyVerifyDeadBody),
				Recovery: frame.EmailLinkVerify,
			}))
	default:
		h.log.ErrorContext(ctx, "confirm email verification", "error", err)
		h.verifyFailed(w, r, i18n.T(ctx, i18n.KeyTryAgainTitle), i18n.T(ctx, i18n.KeyTryAgainBody))
	}
}

// CompleteRegistrationPage does not check the token: that would tell a guesser
// it is real.
func (h *Handler) CompleteRegistrationPage(w http.ResponseWriter, r *http.Request) {
	// The page carries a live registration token; keep it out of BREACH's reach.
	compress.Skip(w)
	respond.Component(w, r, h.log, http.StatusOK, RegisterComplete(
		RegisterCompleteMeta(r.Context()), RegisterCompleteView{
			Token: r.URL.Query().Get("token"),
			Next:  siteurl.PathOr(r.URL.Query().Get("next"), "/account"),
		}))
}

// CompleteRegistration needs both: the link proves the mailbox and the password proves who
// registered; only both together prove the address, sign this browser in and
// adopt its cart. A wrong password is answered as sign-in answers one, under
// sign-in's per-account limit, so this is no second place to guess it.
func (h *Handler) CompleteRegistration(w http.ResponseWriter, r *http.Request) {
	// A refused password re-renders the still-live token; never compress it.
	compress.Skip(w)
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	token := r.PostFormValue("token")
	next := siteurl.PathOr(r.PostFormValue("next"), "/account")

	addr, err := h.store.RegistrationAddress(ctx, token)
	if errors.Is(err, ErrVerifyInvalid) {
		h.registrationDead(w, r, next)
		return
	}
	if err != nil {
		h.log.ErrorContext(ctx, "read registration link", "error", err)
		h.serverError(w, r)
		return
	}
	// Before the hash, and the budget sign-in uses, so the two share it.
	guess, retryAfter, ok := h.reservePasswordGuess(r, email.Clean(addr), nil)
	if !ok {
		h.log.WarnContext(ctx, "registration completion throttled by account")
		ratelimit.Refuse(ctx, w, retryAfter)
		return
	}
	defer guess.settle(false)

	confirmed, err := h.store.CompleteRegistration(ctx, token, r.PostFormValue("password"))
	guess.settle(errors.Is(err, ErrBadCredentials))
	switch {
	case err == nil:
		started, adoption := h.startSession(w, r, user.User{ID: confirmed.UserID})
		if !started {
			return
		}
		clearPendingRegistration(w, h.secure)
		http.Redirect(w, r, registrationLanding(next, adoption), http.StatusSeeOther)
	case errors.Is(err, ErrBadCredentials):
		respond.Component(w, r, h.log, http.StatusUnprocessableEntity, RegisterComplete(
			RegisterCompleteMeta(ctx), RegisterCompleteView{
				Token: token, Next: next, Error: i18n.T(ctx, i18n.KeyBadCredentials),
			}))
	case errors.Is(err, ErrEmailTaken):
		h.verifyFailed(w, r, i18n.T(ctx, i18n.KeyVerifyTakenTitle), i18n.T(ctx, i18n.KeyVerifyTakenBody))
	case errors.Is(err, ErrVerifyInvalid):
		h.registrationDead(w, r, next)
	default:
		h.log.ErrorContext(ctx, "complete registration", "error", err)
		h.serverError(w, r)
	}
}

func (h *Handler) registrationDead(w http.ResponseWriter, r *http.Request, next string) {
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, RegistrationDead(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyVerifyDeadTitle)}, next))
}

func (h *Handler) verifyFailed(w http.ResponseWriter, r *http.Request, heading, body string) {
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, frame.EmailLinkPage(
		frame.EmailLinkMeta(heading),
		frame.EmailLinkView{Heading: heading, Body: body}))
}

func oauthOutcome(ctx context.Context, outcome string) map[string]string {
	var key i18n.Key
	switch outcome {
	case "failed":
		key = i18n.KeyOAuthFailed
	case "state":
		key = i18n.KeyOAuthState
	case "unverified":
		key = i18n.KeyOAuthUnverified
	case "collision":
		key = i18n.KeyOAuthCollision
	case "demo":
		key = i18n.KeyDemoSignInPassword
	default:
		return nil
	}
	return map[string]string{"oauth": i18n.T(ctx, key)}
}

func (h *Handler) GoogleSignIn(w http.ResponseWriter, r *http.Request) {
	if !h.google.Enabled() {
		h.googleUnavailable(w, r)
		return
	}
	if retryAfter, ok := h.signinLimit.Allow("oauth:" + ratelimit.ClientKey(r)); !ok {
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}

	target, state, err := h.google.AuthorizeURL()
	if err != nil {
		h.log.ErrorContext(r.Context(), "build the google authorisation url", "error", err)
		h.serverError(w, r)
		return
	}
	next := siteurl.PathOr(r.URL.Query().Get("next"), "/account")
	writeOAuthState(w, oauthState{Value: state.Value, Verifier: state.Verifier, Next: next}, h.secure)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// GoogleCallback ends every failure at /signin with a message rather than an
// error page: the customer is mid-sign-in. A success answers the page that
// loads where they were headed.
func (h *Handler) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	if !h.google.Enabled() {
		h.googleUnavailable(w, r)
		return
	}
	state, ok := readOAuthState(r, h.secure)
	clearOAuthState(w, h.secure)

	if !ok || state.Value == "" || r.URL.Query().Get("state") != state.Value {
		h.log.WarnContext(r.Context(), "google callback did not match this browser")
		http.Redirect(w, r, "/signin?oauth=state", http.StatusSeeOther)
		return
	}
	// Google reports a refused consent as error=access_denied, not an absent code.
	if r.URL.Query().Get("error") != "" {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Redirect(w, r, "/signin?oauth=state", http.StatusSeeOther)
		return
	}

	identity, err := h.google.Exchange(r.Context(), code, state.Verifier)
	if err != nil {
		h.log.ErrorContext(r.Context(), "exchange the google code", "error", err)
		http.Redirect(w, r, "/signin?oauth=failed", http.StatusSeeOther)
		return
	}
	// A Google sign-in at an account's address links Google to it.
	if h.demo.holds(identity.Email) {
		http.Redirect(w, r, "/signin?oauth=demo", http.StatusSeeOther)
		return
	}

	u, err := h.store.SignInWithGoogle(r.Context(), identity)
	switch {
	case err == nil:
	case errors.Is(err, ErrOAuthUnverified):
		http.Redirect(w, r, "/signin?oauth=unverified", http.StatusSeeOther)
		return
	case errors.Is(err, ErrOAuthCollision):
		http.Redirect(w, r, "/signin?oauth=collision", http.StatusSeeOther)
		return
	default:
		h.log.ErrorContext(r.Context(), "sign in with google", "error", err)
		http.Redirect(w, r, "/signin?oauth=failed", http.StatusSeeOther)
		return
	}

	started, adoption := h.startSession(w, r, u)
	if !started {
		return
	}
	next := state.Next
	next = cartAdoptionLanding(next, adoption)
	// A page and not a 303, as respond.ReloadSameSite explains: the session
	// cookie is SameSite=Strict, and a redirect stays inside the navigation that
	// began at Google, so the landing page would arrive without it and send the
	// visitor back to /signin. The page's refresh starts a same-site navigation.
	w.Header().Set("Cache-Control", "no-store")
	respond.Component(w, r, h.log, http.StatusOK, SignedInHandoff(next))
}

func (h *Handler) googleUnavailable(w http.ResponseWriter, r *http.Request) {
	respond.Component(w, r, h.log, http.StatusNotFound, frame.Notice(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeySignIn)}, "404",
		i18n.T(r.Context(), i18n.KeySignIn), i18n.T(r.Context(), i18n.KeyGoogleUnavailable),
		frame.NoticeActions{Primary: frame.NoticeLink{Href: "/signin", Label: i18n.KeySignIn}}))
}

func (h *Handler) UnlinkGoogle(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if h.refuseUnverifiedStaff(w, r, u) {
		return
	}
	switch err := h.store.UnlinkGoogle(r.Context(), u); {
	case err == nil:
		http.Redirect(w, r, "/account?unlinked=1", http.StatusSeeOther)
	case errors.Is(err, ErrLastSignInMethod):
		http.Redirect(w, r, "/account?lastmethod=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		http.Redirect(w, r, "/account", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "unlink google", "error", err)
		h.serverError(w, r)
	}
}

const oauthStateCookie = "__Host-goen_oauth"

const oauthStateTTL = 10 * time.Minute

// oauthState is what the state cookie carries through Google and back: the
// client's state and PKCE verifier, and where the visitor was headed.
type oauthState struct {
	Value    string
	Verifier string
	Next     string
}

func writeOAuthState(w http.ResponseWriter, s oauthState, secure bool) {
	raw := strings.Join([]string{s.Value, s.Verifier, s.Next}, "|")
	//nolint:gosec // G124: Secure follows the deployment's own flag, as every cookie here does
	http.SetCookie(w, &http.Cookie{
		Name:     oauthCookieName(secure),
		Value:    base64.RawURLEncoding.EncodeToString([]byte(raw)),
		Path:     "/",
		MaxAge:   int(oauthStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func readOAuthState(r *http.Request, secure bool) (oauthState, bool) {
	c, err := r.Cookie(oauthCookieName(secure))
	if err != nil || c.Value == "" {
		return oauthState{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return oauthState{}, false
	}
	parts := strings.SplitN(string(raw), "|", 3)
	if len(parts) != 3 {
		return oauthState{}, false
	}
	return oauthState{
		Value: parts[0], Verifier: parts[1], Next: siteurl.PathOr(parts[2], "/account"),
	}, true
}

func clearOAuthState(w http.ResponseWriter, secure bool) {
	//nolint:gosec // G124: as above
	http.SetCookie(w, &http.Cookie{
		Name: oauthCookieName(secure), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

func oauthCookieName(secure bool) string {
	if secure {
		return oauthStateCookie
	}
	return "goen_oauth"
}
