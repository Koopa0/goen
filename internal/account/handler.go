package account

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

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

// CartFinder is what account needs of the cart: the cart a request's cookie
// names, so sign-in can adopt it; forgetting that cookie and the one naming the
// browser's orders when a session ends; and whether the shop takes payment,
// which decides whether an unpaid order offers 付款.
type CartFinder interface {
	IDForRequest(ctx context.Context, r *http.Request) (uuid.UUID, bool, error)
	ForgetCart(w http.ResponseWriter, r *http.Request)
	ForgetOrders(w http.ResponseWriter, r *http.Request)
	TakesPayment() bool
}

type Handler struct {
	signinLimit *ratelimit.Limiter
	// resetLimit has a budget of its own: spending the registration and change
	// budget for an address must not stop its owner getting a reset link.
	resetLimit *ratelimit.Limiter
	store      *Store
	carts      CartFinder
	log        *slog.Logger
	secure     bool
	google     *Google
	demo       DemoAccount

	// mailLimit bounds, per address, the forms that mail an address whoever
	// names it: each mails the address whether or not it has an account, so
	// without it either form is a way to fill somebody else's inbox. One budget
	// for both, so using the two does not double it.
	mailLimit *ratelimit.Limiter
}

func NewHandler(store *Store, carts CartFinder, log *slog.Logger, secure bool, google *Google) *Handler {
	if store == nil || log == nil {
		panic("account: NewHandler requires a store and a logger")
	}
	if google == nil {
		google = &Google{}
	}
	return &Handler{
		store: store, carts: carts, log: log, secure: secure, google: google,
		signinLimit: ratelimit.New(ratelimit.Config{
			Every: 15 * time.Second, Burst: 6, TTL: time.Hour, MaxKeys: 65_536,
		}),
		resetLimit: ratelimit.New(addressMailPace),
		mailLimit:  ratelimit.New(addressMailPace),
	}
}

// OfferDemoAccount must be called before the handler serves.
func (h *Handler) OfferDemoAccount(d DemoAccount) { h.demo = d }

func (h *Handler) refuseDemoChange(w http.ResponseWriter, r *http.Request, u user.User) bool {
	if !h.demo.holds(u.Email) {
		return false
	}
	http.Redirect(w, r, "/account?demo=fixed", http.StatusSeeOther)
	return true
}

// addressMailPace is three at once, then one every ten minutes: more than
// somebody retyping or asking again needs.
var addressMailPace = ratelimit.Config{
	Every: 10 * time.Minute, Burst: 3, TTL: time.Hour, MaxKeys: 65_536,
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
		next.ServeHTTP(w, r.WithContext(user.NewContext(r.Context(), u)))
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
	web.NoCompress(w)
	view := h.signInView(pages.AuthView{
		Next:          web.SitePathOr(r.URL.Query().Get("next"), "/account"),
		ReturnMessage: signInReturnMessage(r.Context(), r.URL.Query().Get("next")),
		GoogleSignIn:  h.google.Enabled(),
	})
	switch {
	case r.URL.Query().Get("reset") == "1":
		view.Notice = i18n.T(r.Context(), i18n.KeyPasswordReset)
	case r.URL.Query().Get("reauth") == "erase":
		view.Notice = i18n.T(r.Context(), i18n.KeyEraseNeedsRecentSignIn)
		view.HideRegister = true
	default:
		view.Errors = oauthOutcome(r.Context(), r.URL.Query().Get("oauth"))
		switch r.URL.Query().Get("oauth") {
		case "collision":
			view.Recovery = pages.SignInResetPassword
		case "unverified":
			view.Recovery = pages.SignInRegister
		}
	}
	purpose, address := h.takeSignInContext(w, r)
	if purpose == signInAfterReset && r.URL.Query().Get("reset") == "1" ||
		purpose == signInBeforeErasure && r.URL.Query().Get("reauth") == "erase" {
		view.Email = address
		view.PasswordFocus = view.Email != ""
	}
	web.Render(w, r, h.log, http.StatusOK, pages.SignIn(pages.SignInMeta(r.Context()), view))
}

func (h *Handler) SignIn(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	addr := r.PostFormValue("email")
	password := r.PostFormValue("password")
	next := web.SitePathOr(r.PostFormValue("next"), "/account")
	normalised := email.Clean(addr)
	// email.Max is the application's address policy; the column is text, so the
	// handler must bound the value before it becomes a long-lived limiter key.
	if len(normalised) > email.Max {
		h.signInFailed(w, r, addr, next)
		return
	}

	// Before Authenticate: argon2 at 64 MiB is the cost this limit protects.
	// The demo account's password is printed on this page, so a per-account
	// bound would guard nothing and let one visitor's mistakes shut out every
	// other; the per-client bound still holds the cost.
	if !h.demo.holds(normalised) {
		if retryAfter, ok := h.signinLimit.Allow("account:" + normalised); !ok {
			h.log.WarnContext(r.Context(), "sign-in throttled by account")
			ratelimit.Refuse(r.Context(), w, retryAfter)
			return
		}
	}

	u, err := h.store.Authenticate(r.Context(), addr, password)
	if err != nil {
		if !errors.Is(err, ErrBadCredentials) {
			h.log.ErrorContext(r.Context(), "authenticate", "error", err)
			h.serverError(w, r)
			return
		}
		h.signInFailed(w, r, addr, next)
		return
	}

	started, adoption := h.startSession(w, r, u)
	if !started {
		return
	}
	next = cartAdoptionLanding(next, adoption)
	http.Redirect(w, r, next, http.StatusSeeOther) //nolint:gosec // G710: bounded by web.SitePathOr
}

// signInFailed is one response for an unusable address, an unknown account and
// a wrong password, so two submissions never differ in information about an
// account.
func (h *Handler) signInFailed(w http.ResponseWriter, r *http.Request, addr, next string) {
	web.NoCompress(w)
	web.Render(w, r, h.log, http.StatusUnprocessableEntity,
		pages.SignIn(pages.SignInMeta(r.Context()), h.signInView(pages.AuthView{
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
	if len(address) > email.Max || !email.Valid(address) {
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
	if err != nil || len(decoded) > email.Max || !email.Valid(string(decoded)) {
		return "", ""
	}
	return purpose, string(decoded)
}

func signInReturnMessage(ctx context.Context, next string) string {
	target, ok := web.SitePath(next)
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

func (h *Handler) signInView(v pages.AuthView) pages.AuthView {
	v.DemoEmail, v.DemoPassword = h.demo.email, h.demo.password
	return v
}

func (h *Handler) RegisterPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := user.FromContext(r.Context()); ok {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	q := r.URL.Query()
	view := pages.AuthView{Next: web.SitePathOr(q.Get("next"), "/account")}
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
	web.Render(w, r, h.log, http.StatusOK, pages.Register(pages.RegisterMeta(r.Context()), view))
}

// ResendRegistration gives every usable address the same answer, so the form
// cannot be asked who has an account.
func (h *Handler) ResendRegistration(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	addr := email.Clean(r.PostFormValue("email"))
	next := web.SitePathOr(r.PostFormValue("next"), "/account")
	if EmailError(addr) != "" {
		http.Redirect(w, r, "/register?"+url.Values{"next": {next}}.Encode(), http.StatusSeeOther)
		return
	}
	if retryAfter, ok := h.mailLimit.Allow("mail:" + addr); !ok {
		clearPendingRegistration(w, h.secure)
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}
	if err := h.store.ResendRegistration(r.Context(), addr, next); err != nil {
		h.log.ErrorContext(r.Context(), "resend registration", "error", err)
		h.serverError(w, r)
		return
	}
	writePendingRegistration(w, addr, next, h.secure)
	http.Redirect(w, r, "/register?sent=1&again=1", http.StatusSeeOther)
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
	return email.Clean(addr), web.SitePathOr(next, "/account"), true
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
	if err := web.ParseForm(w, r); err != nil {
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
	next := web.SitePathOr(r.PostFormValue("next"), "/account")

	view := pages.AuthView{Email: c.Email, Name: c.Name, Next: next}
	if errs := FieldMessages(r.Context(), c.ValidateRegistration()); len(errs) > 0 {
		view.Errors = errs
		web.Render(w, r, h.log, http.StatusUnprocessableEntity,
			pages.Register(pages.RegisterMeta(r.Context()), view))
		return
	}

	// After validation, so only an address the rules accept becomes a key.
	if retryAfter, ok := h.mailLimit.Allow("mail:" + email.Clean(c.Email)); !ok {
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}

	if err := h.store.Register(r.Context(), c, next); err != nil {
		h.log.ErrorContext(r.Context(), "register", "error", err)
		h.serverError(w, r)
		return
	}
	writePendingRegistration(w, email.Clean(c.Email), next, h.secure)
	http.Redirect(w, r, "/register?sent=1", http.StatusSeeOther)
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
		h.carts.ForgetOrders(w, r)
	}
}

func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	h.overview(w, r, http.StatusOK, nil)
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request, status int, refused func(*pages.AccountView)) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	view, err := h.store.Overview(r.Context(), u, r.URL.Query().Get("after"))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read account", "error", err)
		h.serverError(w, r)
		return
	}
	// A failure here loses the badge and not the page, which is the safe direction.
	if state, vErr := h.store.EmailVerification(r.Context(), u.ID); vErr != nil {
		h.log.WarnContext(r.Context(), "read verification state", "error", vErr)
	} else {
		view.EmailVerified, view.PendingEmail = state.Verified, state.PendingEmail
	}
	view.Notice = accountNotice(r)
	if r.URL.Query().Get("welcome") == "1" {
		view.ReturnAfterWelcome, _ = web.SitePath(r.URL.Query().Get("next"))
		view.CartAdjusted = r.URL.Query().Get("adjusted") == "1"
	}
	view.PaymentsEnabled = h.carts != nil && h.carts.TakesPayment()
	if refused != nil {
		refused(&view)
	}
	web.Render(w, r, h.log, status, pages.Account(pages.AccountMeta(r.Context()), &view))
}

func accountNotice(r *http.Request) string {
	ctx := r.Context()
	q := r.URL.Query()
	switch {
	case q.Get("welcome") == "1":
		return i18n.T(ctx, i18n.KeyAccountWelcome)
	case q.Get("saved") == "1":
		return i18n.T(ctx, i18n.KeyProfileSaved)
	case q.Get("profile") == "invalid":
		return i18n.T(ctx, i18n.KeyProfileInvalid)
	case q.Get("password") == "wrong":
		return i18n.T(ctx, i18n.KeyWrongCurrentPassword)
	case q.Get("password") == "invalid":
		return i18n.T(ctx, i18n.KeyNewPasswordRefused)
	case q.Get("erase") == "confirm":
		return i18n.T(ctx, i18n.KeyEraseNeedsEmail)
	case q.Get("erase") == "return":
		return i18n.T(ctx, i18n.KeyEraseOpenReturn)
	case q.Get("erase") == "admin":
		return i18n.T(ctx, i18n.KeyEraseLastAdmin)
	case q.Get("email") == "sent":
		return i18n.T(ctx, i18n.KeyEmailSent)
	case q.Get("email") == "invalid":
		return i18n.T(ctx, i18n.KeyEmailInvalidNotice)
	case q.Get("email") == "staff":
		return i18n.T(ctx, i18n.KeyEmailStaffFixed)
	case q.Get("unlinked") == "1":
		return i18n.T(ctx, i18n.KeyGoogleUnlinked)
	case q.Get("lastmethod") == "1":
		return i18n.T(ctx, i18n.KeyGoogleLastMethod)
	case q.Get("demo") == "fixed":
		return i18n.T(ctx, i18n.KeyDemoAccountFixed)
	}
	return ""
}

func (h *Handler) CartRecoveryPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := user.FromContext(r.Context()); !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	next := web.SitePathOr(r.URL.Query().Get("next"), "/account")
	web.Render(w, r, h.log, http.StatusOK, pages.CartRecovery(
		pages.CartRecoveryMeta(r.Context()), pages.CartRecoveryView{
			Next:    next,
			Welcome: r.URL.Query().Get("welcome") == "1",
			Notice:  i18n.T(r.Context(), i18n.KeyCartMergeFailed),
			Retry:   i18n.T(r.Context(), i18n.KeyCartMergeRetry),
		}))
}

func (h *Handler) RetryCartAdoption(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	next := web.SitePathOr(r.PostFormValue("next"), "/account")
	next = cartAdoptionLanding(next, h.adoptRequestCart(r, u.ID))
	http.Redirect(w, r, next, http.StatusSeeOther) //nolint:gosec // G710: bounded by web.SitePathOr
}

func (h *Handler) OrderPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := user.FromContext(r.Context()); !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	number := r.PathValue("number")
	http.Redirect(w, r, "/orders/"+url.PathEscape(number), http.StatusSeeOther)
}

func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	if err := h.store.UpdateProfile(r.Context(), u.ID,
		r.PostFormValue("name"), r.PostFormValue("phone")); err != nil {
		if errors.Is(err, ErrInvalidInput) {
			http.Redirect(w, r, "/account?profile=invalid", http.StatusSeeOther)
			return
		}
		h.log.ErrorContext(r.Context(), "update profile", "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/account?saved=1", http.StatusSeeOther)
}

type cartAdoption uint8

const (
	cartAdoptionUnchanged cartAdoption = iota
	cartAdoptionAdjusted
	cartAdoptionFailed
)

func (h *Handler) startSession(w http.ResponseWriter, r *http.Request, u user.User) (started bool, adoption cartAdoption) {
	token, err := h.store.StartSession(r.Context(), u.ID, r.UserAgent(), clientIP(r))
	if err != nil {
		h.log.ErrorContext(r.Context(), "start session", "error", err)
		h.serverError(w, r)
		return false, cartAdoptionUnchanged
	}
	SetSessionCookie(w, token, h.secure)
	// Authentication succeeds even when the preserved guest cart needs recovery.
	return true, h.adoptRequestCart(r, u.ID)
}

func (h *Handler) adoptRequestCart(r *http.Request, userID string) cartAdoption {
	if h.carts == nil {
		return cartAdoptionUnchanged
	}
	cartID, ok, err := h.carts.IDForRequest(r.Context(), r)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read cart for sign-in", "error", err)
		return cartAdoptionFailed
	}
	if !ok {
		return cartAdoptionUnchanged
	}
	err = h.store.AdoptCart(r.Context(), userID, cartID)
	if err == nil {
		return cartAdoptionUnchanged
	}
	if errors.Is(err, ErrQuantityAdjusted) {
		return cartAdoptionAdjusted
	}
	h.log.ErrorContext(r.Context(), "adopt cart", "error", err, "user_id", userID)
	return cartAdoptionFailed
}

func registrationLanding(next string, outcome cartAdoption) string {
	next = web.SitePathOr(next, "/account")
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
	next := web.SitePathOr(target, "/account")
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

func clientIP(r *http.Request) string {
	return ratelimit.ClientIP(r)
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
	next := web.SitePathOr(continuation, "/account")
	u := &url.URL{Path: "/account/cart-recovery"}
	q := u.Query()
	q.Set("next", next)
	u.RawQuery = q.Encode()
	return u.String()
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyBusyTitle)}, "",
		i18n.T(r.Context(), i18n.KeyBusyTitle),
		i18n.T(r.Context(), i18n.KeyBusyBody)))
}

func (h *Handler) AddAddress(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	a := &Address{
		Label: r.PostFormValue("label"), Name: r.PostFormValue("name"),
		Phone: r.PostFormValue("phone"), PostalCode: r.PostFormValue("postal_code"),
		City: r.PostFormValue("city"), District: r.PostFormValue("district"),
		Street: r.PostFormValue("street"), Default: r.PostFormValue("default") == "1",
	}
	a.Trim()
	if errs := a.Validate(); len(errs) > 0 {
		h.refuseAddress(w, r, a, FieldMessages(r.Context(), errs), "")
		return
	}
	if err := h.store.AddAddress(r.Context(), u.ID, a); err != nil {
		if errors.Is(err, ErrInvalidInput) {
			h.refuseAddress(w, r, a, nil, i18n.T(r.Context(), i18n.KeyAddressIncomplete))
			return
		}
		h.log.ErrorContext(r.Context(), "add address", "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/account?saved=1", http.StatusSeeOther)
}

func (h *Handler) refuseAddress(w http.ResponseWriter, r *http.Request, a *Address, errs map[string]string, notice string) {
	h.overview(w, r, http.StatusUnprocessableEntity, func(v *pages.AccountView) {
		v.AddressDraft = &pages.AddressDraft{
			Label: a.Label, Name: a.Name, Phone: a.Phone, PostalCode: a.PostalCode,
			City: a.City, District: a.District, Street: a.Street, Default: a.Default,
		}
		v.AddressErrors = errs
		if notice != "" {
			v.Notice = notice
		}
	})
}

func (h *Handler) MakeDefaultAddress(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	switch err := h.store.MakeDefaultAddress(r.Context(), u.ID, r.PostFormValue("address")); {
	case err == nil:
		http.Redirect(w, r, "/account?saved=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		http.Redirect(w, r, "/account", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set default address", "error", err)
		h.serverError(w, r)
	}
}

func (h *Handler) DeleteAddress(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	if err := h.store.DeleteAddress(r.Context(), u.ID, r.PostFormValue("address")); err != nil {
		if !errors.Is(err, ErrNotFound) {
			h.log.ErrorContext(r.Context(), "delete address", "error", err)
			h.serverError(w, r)
			return
		}
	}
	http.Redirect(w, r, "/account?saved=1", http.StatusSeeOther)
}

func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if h.refuseDemoChange(w, r, u) {
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	current := r.PostFormValue("current")
	if err := h.store.ConfirmPassword(r.Context(), u.Email, current); err != nil {
		if !errors.Is(err, ErrBadCredentials) {
			h.log.ErrorContext(r.Context(), "confirm password change", "error", err)
			h.serverError(w, r)
			return
		}
		http.Redirect(w, r, "/account?password=wrong", http.StatusSeeOther)
		return
	}

	next := r.PostFormValue("password")
	if PasswordError(next) != "" || next != r.PostFormValue("confirm") {
		http.Redirect(w, r, "/account?password=invalid", http.StatusSeeOther)
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
	if h.refuseDemoChange(w, r, u) {
		return
	}
	if err := web.ParseForm(w, r); err != nil {
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
	if r.PostFormValue("confirm") != u.Email {
		http.Redirect(w, r, "/account?erase=confirm", http.StatusSeeOther)
		return
	}
	switch err := h.store.Erase(r.Context(), u.ID); {
	case errors.Is(err, ErrOpenReturn):
		http.Redirect(w, r, "/account?erase=return", http.StatusSeeOther)
		return
	case errors.Is(err, ErrLastAdmin):
		http.Redirect(w, r, "/account?erase=admin", http.StatusSeeOther)
		return
	case err != nil:
		h.log.ErrorContext(r.Context(), "erase account", "error", err)
		h.serverError(w, r)
		return
	}
	ClearSessionCookie(w, h.secure)
	http.Redirect(w, r, "/?erased=1", http.StatusSeeOther)
}

func (h *Handler) Wishlist(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin?next=/account/wishlist", http.StatusSeeOther)
		return
	}
	tiles, err := h.store.Wishlist(r.Context(), u.ID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read wishlist", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Wishlist(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyWishlistTitle)}, pages.WishlistView{Products: tiles, Added: pages.AddOutcome(r.URL.Query().Get("added"))}))
}

func (h *Handler) SaveWishlist(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	// web.SitePathOr refuses anything but a same-site path, which makes a
	// redirect target read off a form safe.
	back := web.SitePathOr(r.PostFormValue("return"), "/account/wishlist")

	u, ok := user.FromContext(r.Context())
	if !ok {
		// Authentication does not replay writes; this redirect only preserves
		// the page for callers outside the authenticated route wrapper.
		http.Redirect(w, r, "/signin?next="+urlQueryEscape(back), http.StatusSeeOther)
		return
	}

	slug := r.PostFormValue("slug")
	var err error
	if r.PostFormValue("action") == "remove" {
		err = h.store.RemoveFromWishlist(r.Context(), u.ID, slug)
	} else {
		err = h.store.SaveToWishlist(r.Context(), u.ID, slug)
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "update wishlist", "error", err, "slug", slug)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (h *Handler) ChangeEmail(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if h.refuseDemoChange(w, r, u) {
		return
	}
	if u.IsStaff() {
		http.Redirect(w, r, "/account?email=staff", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	if err := h.store.ConfirmPassword(r.Context(), u.Email, r.PostFormValue("current")); err != nil {
		if !errors.Is(err, ErrBadCredentials) {
			h.log.ErrorContext(r.Context(), "confirm email change", "error", err)
			h.serverError(w, r)
			return
		}
		http.Redirect(w, r, "/account?password=wrong", http.StatusSeeOther)
		return
	}

	addr := r.PostFormValue("email")
	if EmailError(addr) != "" {
		http.Redirect(w, r, "/account?email=invalid", http.StatusSeeOther)
		return
	}
	// After validation, so only an address the rules accept becomes a key, and
	// on the address's own count, so the refusal says nothing about its holder.
	if retryAfter, ok := h.mailLimit.Allow("mail:" + email.Clean(addr)); !ok {
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}

	// The same answer whether or not the address has an account: only the
	// mailbox is told which, by the outbox worker.
	if err := h.store.requestVerification(r.Context(), u.ID, addr); err != nil {
		h.log.ErrorContext(r.Context(), "request email verification", "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/account?email=sent", http.StatusSeeOther)
}

func (h *Handler) ResendVerification(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if h.refuseDemoChange(w, r, u) {
		return
	}
	if err := h.store.requestVerification(r.Context(), u.ID, u.Email); err != nil {
		h.log.ErrorContext(r.Context(), "resend email verification", "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/account?email=sent", http.StatusSeeOther)
}

func (h *Handler) VerifyPage(w http.ResponseWriter, r *http.Request) {
	// The live address-verification token changes identity data; never compress it.
	web.NoCompress(w)
	ctx := r.Context()
	if r.URL.Query().Get("done") == "1" {
		web.Render(w, r, h.log, http.StatusOK, pages.EmailLinkPage(
			pages.EmailLinkMeta(i18n.T(ctx, i18n.KeyVerifyDone)),
			pages.EmailLinkView{
				Heading: i18n.T(ctx, i18n.KeyVerifyDone),
				Body:    i18n.T(ctx, i18n.KeyVerifyDoneBody),
			}))
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		web.Render(w, r, h.log, http.StatusOK, pages.EmailLinkPage(
			pages.EmailLinkMeta(i18n.T(ctx, i18n.KeyVerifyTitle)),
			pages.EmailLinkView{
				Heading:  i18n.T(ctx, i18n.KeyVerifyTitle),
				Body:     i18n.T(ctx, i18n.KeyEmailLinkIncomplete),
				Recovery: pages.EmailLinkVerify,
			}))
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.EmailLinkPage(
		pages.EmailLinkMeta(i18n.T(ctx, i18n.KeyVerifyTitle)),
		pages.EmailLinkView{
			Heading: i18n.T(ctx, i18n.KeyVerifyTitle),
			Body:    i18n.T(ctx, i18n.KeyVerifyBody),
			Action:  "/verify",
			Submit:  i18n.T(ctx, i18n.KeyVerifySubmit),
			Token:   token,
		}))
}

func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
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
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.EmailLinkPage(
			pages.EmailLinkMeta(heading),
			pages.EmailLinkView{
				Heading:  heading,
				Body:     i18n.T(ctx, i18n.KeyVerifyDeadBody),
				Recovery: pages.EmailLinkVerify,
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
	web.NoCompress(w)
	web.Render(w, r, h.log, http.StatusOK, pages.RegisterComplete(
		pages.RegisterCompleteMeta(r.Context()), pages.RegisterCompleteView{
			Token: r.URL.Query().Get("token"),
			Next:  web.SitePathOr(r.URL.Query().Get("next"), "/account"),
		}))
}

// CompleteRegistration needs both: the link proves the mailbox and the password proves who
// registered; only both together prove the address, sign this browser in and
// adopt its cart. A wrong password is answered as sign-in answers one, under
// sign-in's per-account limit, so this is no second place to guess it.
func (h *Handler) CompleteRegistration(w http.ResponseWriter, r *http.Request) {
	// A refused password re-renders the still-live token; never compress it.
	web.NoCompress(w)
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	token := r.PostFormValue("token")
	next := web.SitePathOr(r.PostFormValue("next"), "/account")

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
	// Before the hash, and the key sign-in uses, so the two share one budget.
	if retryAfter, ok := h.signinLimit.Allow("account:" + email.Clean(addr)); !ok {
		h.log.WarnContext(ctx, "registration completion throttled by account")
		ratelimit.Refuse(ctx, w, retryAfter)
		return
	}

	confirmed, err := h.store.CompleteRegistration(ctx, token, r.PostFormValue("password"))
	switch {
	case err == nil:
		started, adoption := h.startSession(w, r, user.User{ID: confirmed.UserID})
		if !started {
			return
		}
		clearPendingRegistration(w, h.secure)
		http.Redirect(w, r, registrationLanding(next, adoption), http.StatusSeeOther)
	case errors.Is(err, ErrBadCredentials):
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.RegisterComplete(
			pages.RegisterCompleteMeta(ctx), pages.RegisterCompleteView{
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
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.RegistrationDead(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyVerifyDeadTitle)}, next))
}

func (h *Handler) verifyFailed(w http.ResponseWriter, r *http.Request, heading, body string) {
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.EmailLinkPage(
		pages.EmailLinkMeta(heading),
		pages.EmailLinkView{Heading: heading, Body: body}))
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
		http.NotFound(w, r)
		return
	}
	if retryAfter, ok := h.signinLimit.Allow("oauth:" + ratelimit.ClientKey(r)); !ok {
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}

	target, state, err := h.google.AuthorizeURL(web.SitePathOr(r.URL.Query().Get("next"), "/account"))
	if err != nil {
		h.log.ErrorContext(r.Context(), "build the google authorisation url", "error", err)
		h.serverError(w, r)
		return
	}
	writeOAuthState(w, state, h.secure)
	//nolint:gosec // G710: no request value reaches target
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// GoogleCallback ends every failure at /signin with a message rather than an
// error page: the customer is mid-sign-in.
func (h *Handler) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	if !h.google.Enabled() {
		http.NotFound(w, r)
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
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (h *Handler) UnlinkGoogle(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
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

func writeOAuthState(w http.ResponseWriter, s OAuthState, secure bool) {
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

func readOAuthState(r *http.Request, secure bool) (OAuthState, bool) {
	c, err := r.Cookie(oauthCookieName(secure))
	if err != nil || c.Value == "" {
		return OAuthState{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return OAuthState{}, false
	}
	parts := strings.SplitN(string(raw), "|", 3)
	if len(parts) != 3 {
		return OAuthState{}, false
	}
	return OAuthState{
		Value: parts[0], Verifier: parts[1], Next: web.SitePathOr(parts[2], "/account"),
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
