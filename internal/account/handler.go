package account

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// CartFinder is what the account package needs from the cart: the id of the
// cart a request's cookie names, so a guest cart can be adopted at sign-in.
//
// The interface is defined HERE, by the consumer, and it is one method — the
// cross-feature boundary rules/interfaces.md describes.
type CartFinder interface {
	CartIDForRequest(ctx context.Context, r *http.Request) (uuid.UUID, bool)
}

// Handler serves sign-in, registration and the customer's own pages.
type Handler struct {
	// signinLimit bounds sign-in attempts per ACCOUNT. The per-IP half is
	// middleware in cmd/goen; both are needed, because either alone leaves the
	// other attack open.
	signinLimit *ratelimit.Limiter
	// resetLimit bounds password-reset requests, keyed on the ADDRESS. Per-IP
	// limiting cannot see this attack: filling somebody else's mailbox with
	// reset mail — a nuisance that also trains them to ignore the real one —
	// takes one request per machine from as many machines as the sender has.
	// The address is the only key the whole attack shares.
	resetLimit *ratelimit.Limiter
	store      *Store
	carts      CartFinder
	log        *slog.Logger
	secure     bool
	// google may be a disabled client, which renders no button and 404s the two
	// routes — the shape payment.Gateway and invoice.Gateway already have.
	google *Google
}

// NewHandler returns a Handler over store.
func NewHandler(store *Store, carts CartFinder, log *slog.Logger, secure bool, google *Google) *Handler {
	if store == nil || log == nil {
		panic("account: NewHandler requires a store and a logger")
	}
	if google == nil {
		// A disabled client rather than a nil one, so every call site can ask
		// Enabled() without a nil check first.
		google = &Google{}
	}
	return &Handler{
		store: store, carts: carts, log: log, secure: secure, google: google,
		// Six attempts, one back every fifteen seconds. A person who mistypes
		// their password three times never sees it; a script gets four
		// guesses a minute against a given account, which turns a dictionary
		// into years.
		signinLimit: ratelimit.New(ratelimit.Config{
			Every: 15 * time.Second, Burst: 6, TTL: time.Hour,
		}),
		// Three, one back a minute. A person asks for one link and occasionally
		// two; anything above that is somebody else's mailbox being filled.
		resetLimit: ratelimit.New(ratelimit.Config{
			Every: time.Minute, Burst: 3, TTL: time.Hour,
		}),
	}
}

// contextKey is this package's own context key type, so nothing else can
// collide with it.
type contextKey struct{}

// userKey carries the signed-in user through a request.
var userKey contextKey

// WithUser attaches a user to a request context.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// FromContext returns the signed-in user, if any.
func FromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userKey).(User)
	return u, ok
}

// Authenticate is middleware that attaches the signed-in user to every request.
//
// It does NOT reject anyone: a signed-out visitor is a valid state on almost
// every page, and pages that need an account say so themselves through
// RequireUser. Mixing the two would make every public page depend on the
// session lookup succeeding.
func (h *Handler) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ReadSessionCookie(r, h.secure)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, err := h.store.SessionUser(r.Context(), token)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				h.log.ErrorContext(r.Context(), "read session", "error", err)
			}
			// An unknown or expired session: clear the cookie so the browser
			// stops sending a token that will never work again.
			ClearSessionCookie(w, h.secure)
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
	})
}

// RequireUser wraps a handler that needs an account, sending a signed-out
// visitor to sign in and back again afterwards.
func (h *Handler) RequireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := FromContext(r.Context()); !ok {
			http.Redirect(w, r, "/signin?next="+urlQueryEscape(r.URL.Path), http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// SignInPage serves GET /signin.
func (h *Handler) SignInPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := FromContext(r.Context()); ok {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	view := pages.AuthView{
		Next:         SafeNext(r.URL.Query().Get("next")),
		GoogleSignIn: h.google.Enabled(),
	}
	switch {
	case r.URL.Query().Get("registered") == "1":
		view.Notice = i18n.T(r.Context(), i18n.KeyAccountCreated)
	case r.URL.Query().Get("reset") == "1":
		// The reset does NOT sign anybody in — see Handler.Reset. This is the
		// page that tells them the new password works by asking for it.
		view.Notice = i18n.T(r.Context(), i18n.KeyPasswordReset)
	default:
		// The four ways a Google sign-in ends other than signed in. Each names a
		// different next move, which is why the callback carries which one.
		view.Errors = oauthOutcome(r.Context(), r.URL.Query().Get("oauth"))
	}
	web.Render(w, r, h.log, http.StatusOK, pages.SignIn(pages.SignInMeta(r.Context()), view))
}

// SignIn serves POST /signin.
func (h *Handler) SignIn(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	email := r.PostFormValue("email")
	password := r.PostFormValue("password")
	next := SafeNext(r.PostFormValue("next"))

	// The per-ACCOUNT limit, which per-IP limiting cannot see: a distributed
	// attack on one address comes from a thousand IPs, each of them under their
	// own allowance. Keyed on the lowercased email, so case is not a way around
	// it, and checked BEFORE Authenticate — argon2 at 64 MiB is the cost this
	// is protecting, and paying it to discover the caller was over the limit
	// defends nothing.
	//
	// It is a throttle and never a lockout: the key recovers on its own, so
	// guessing at somebody's account cannot lock them out of it.
	if retryAfter, ok := h.signinLimit.Allow("account:" + strings.ToLower(strings.TrimSpace(email))); !ok {
		h.log.WarnContext(r.Context(), "sign-in throttled by account")
		ratelimit.Refuse(w, retryAfter)
		return
	}

	u, err := h.store.Authenticate(r.Context(), email, password)
	if err != nil {
		if !errors.Is(err, ErrBadCredentials) {
			h.log.ErrorContext(r.Context(), "authenticate", "error", err)
			h.serverError(w, r)
			return
		}
		// One message for a wrong email and a wrong password. Telling them apart
		// tells an attacker which addresses have accounts.
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.SignIn(pages.SignInMeta(r.Context()),
			pages.AuthView{
				Email: email, Next: next,
				Errors: map[string]string{"form": i18n.T(r.Context(), i18n.KeyBadCredentials)},
			}))
		return
	}

	h.startSession(w, r, u)
	// next passed SafeNext, which admits only a path on this site — TestSafeNext
	// covers the protocol-relative and scheme forms that would make it an open
	// redirect.
	http.Redirect(w, r, next, http.StatusSeeOther) //nolint:gosec // G710: bounded by SafeNext
}

// RegisterPage serves GET /register.
func (h *Handler) RegisterPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := FromContext(r.Context()); ok {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Register(pages.RegisterMeta(r.Context()),
		pages.AuthView{Next: SafeNext(r.URL.Query().Get("next"))}))
}

// Register serves POST /register.
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
	next := SafeNext(r.PostFormValue("next"))

	view := pages.AuthView{Email: c.Email, Name: c.Name, Next: next}
	if errs := fieldMessages(r.Context(), c.ValidateRegistration()); len(errs) > 0 {
		view.Errors = errs
		web.Render(w, r, h.log, http.StatusUnprocessableEntity,
			pages.Register(pages.RegisterMeta(r.Context()), view))
		return
	}

	u, err := h.store.Register(r.Context(), c)
	if err != nil {
		if errors.Is(err, ErrEmailTaken) {
			view.Errors = map[string]string{"email": i18n.T(r.Context(), i18n.KeyEmailTaken)}
			web.Render(w, r, h.log, http.StatusUnprocessableEntity,
				pages.Register(pages.RegisterMeta(r.Context()), view))
			return
		}
		h.log.ErrorContext(r.Context(), "register", "error", err)
		h.serverError(w, r)
		return
	}

	// Ask them to prove the address they just typed. Logged and swallowed rather
	// than failing the registration: an account that exists with an unproved
	// address is a working account, and refusing to create one because a letter
	// could not be queued would turn a mail problem into a lost customer.
	//
	// This is where a typo becomes findable. Somebody who registers with
	// gmial.com otherwise hears nothing, ever, and has no way to notice or to fix
	// it — the account page says 尚未確認 and offers to send it again.
	if _, err := h.store.RequestVerification(r.Context(), u.ID, u.Email); err != nil {
		h.log.WarnContext(r.Context(), "request verification at registration", "error", err)
	}

	h.startSession(w, r, u)
	// See SignIn: next is bounded by SafeNext.
	http.Redirect(w, r, next, http.StatusSeeOther) //nolint:gosec // G710: bounded by SafeNext
}

// SignOut serves POST /signout. A POST, not a GET: signing out is a state
// change, and a GET would let a link or a prefetch do it.
func (h *Handler) SignOut(w http.ResponseWriter, r *http.Request) {
	if err := h.store.EndSession(r.Context(), ReadSessionCookie(r, h.secure)); err != nil {
		h.log.ErrorContext(r.Context(), "end session", "error", err)
	}
	ClearSessionCookie(w, h.secure)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Overview serves GET /account.
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	u, ok := FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	view, err := h.store.Overview(r.Context(), u)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read account", "error", err)
		h.serverError(w, r)
		return
	}
	// Read separately rather than joined into Overview: it is one row on users and
	// one on email_verifications, and Overview is already the widest read on this
	// page. A failure here loses the badge and not the page — the section says
	// "not confirmed", which is the safe direction to be wrong in.
	if state, vErr := h.store.EmailVerification(r.Context(), u.ID); vErr != nil {
		h.log.WarnContext(r.Context(), "read verification state", "error", vErr)
	} else {
		view.EmailVerified, view.PendingEmail = state.Verified, state.PendingEmail
	}
	view.Notice = accountNotice(r)
	web.Render(w, r, h.log, http.StatusOK, pages.Account(pages.AccountMeta(r.Context()), &view))
}

// accountNotice turns the one-shot query parameter a redirect carries into the
// message the page shows. The redirect is what keeps a reload from resubmitting
// the form behind it.
func accountNotice(r *http.Request) string {
	ctx := r.Context()
	q := r.URL.Query()
	switch {
	case q.Get("saved") == "1":
		return i18n.T(ctx, i18n.KeyProfileSaved)
	case q.Get("address") == "invalid":
		return i18n.T(ctx, i18n.KeyAddressIncomplete)
	case q.Get("password") == "wrong":
		return i18n.T(ctx, i18n.KeyWrongCurrentPassword)
	case q.Get("password") == "invalid":
		return i18n.T(ctx, i18n.KeyNewPasswordRefused)
	case q.Get("erase") == "confirm":
		return i18n.T(ctx, i18n.KeyEraseNeedsEmail)
	case q.Get("email") == "sent":
		return i18n.T(ctx, i18n.KeyEmailSent)
	case q.Get("email") == "taken":
		return i18n.T(ctx, i18n.KeyEmailTakenNotice)
	case q.Get("email") == "invalid":
		return i18n.T(ctx, i18n.KeyEmailInvalidNotice)
	case q.Get("unlinked") == "1":
		return i18n.T(ctx, i18n.KeyGoogleUnlinked)
	case q.Get("lastmethod") == "1":
		return i18n.T(ctx, i18n.KeyGoogleLastMethod)
	}
	return ""
}

// OrderPage serves GET /account/orders/{number}.
func (h *Handler) OrderPage(w http.ResponseWriter, r *http.Request) {
	u, ok := FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	view, err := h.store.Order(r.Context(), u, r.PathValue("number"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Someone else's order and a nonexistent one answer identically. A
			// distinguishable 403 would confirm that an order number is real.
			web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
				layouts.Page{Title: i18n.T(r.Context(), i18n.KeyOrderNotFound)}, "404",
				i18n.T(r.Context(), i18n.KeyOrderNotFound),
				i18n.T(r.Context(), i18n.KeyOrderNotYours2)))
			return
		}
		h.log.ErrorContext(r.Context(), "read account order", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK,
		pages.AccountOrderPage(pages.OrderMeta(r.Context(), view.Number), &view))
}

// UpdateProfile serves POST /account/profile.
func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	u, ok := FromContext(r.Context())
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
		h.log.ErrorContext(r.Context(), "update profile", "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/account?saved=1", http.StatusSeeOther)
}

// startSession issues the session cookie and adopts any guest cart.
func (h *Handler) startSession(w http.ResponseWriter, r *http.Request, u User) {
	token, err := h.store.StartSession(r.Context(), u.ID, r.UserAgent(), clientIP(r))
	if err != nil {
		h.log.ErrorContext(r.Context(), "start session", "error", err)
		h.serverError(w, r)
		return
	}
	SetSessionCookie(w, token, h.secure)

	// A guest cart follows its owner in. A failure here must not take the
	// sign-in down with it: the visitor is signed in either way, and a lost
	// cart merge is recoverable where a failed sign-in is confusing.
	if h.carts != nil {
		if cartID, ok := h.carts.CartIDForRequest(r.Context(), r); ok {
			if err := h.store.AdoptCart(r.Context(), u.ID, cartID); err != nil {
				h.log.ErrorContext(r.Context(), "adopt cart", "error", err, "user_id", u.ID)
			}
		}
	}
}

// clientIP is the address a session is recorded against.
//
// It defers to [ratelimit.ClientIP] rather than reading RemoteAddr itself,
// because reading it here is a SECOND copy of one decision. "Only the direct
// peer is trusted: X-Forwarded-For is set by the client" is the whole truth only
// while nothing can tell a trusted proxy from a stranger, and GOEN_TRUSTED_PROXIES
// is exactly that. A copy left behind here stamps the load balancer's address
// onto every session row, so the one field that says WHERE somebody signed in
// from names the same host for every customer in the deployment.
//
// One definition, for the reason committed_orders and store_credit_balances are
// views: the rule would otherwise be restated wherever an address is needed, and
// the copy that forgot would be whichever was written next.
func clientIP(r *http.Request) string {
	return ratelimit.ClientIP(r)
}

// fieldMessages collapses field errors to one message each.
func fieldMessages(ctx context.Context, errs []FieldError) map[string]string {
	if len(errs) == 0 {
		return nil
	}
	out := make(map[string]string, len(errs))
	for _, e := range errs {
		if _, seen := out[e.Field]; !seen {
			// Rendered HERE: the validators return keys, and this is the first
			// place that knows which locale is reading. Sprintf carries the
			// password minimum; a message with no verb passes through.
			out[e.Field] = fmt.Sprintf(i18n.T(ctx, e.MessageKey), MinPasswordRunes)
		}
	}
	return out
}

// urlQueryEscape escapes a value for a query parameter.
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

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyBusyTitle)}, "",
		i18n.T(r.Context(), i18n.KeyBusyTitle),
		i18n.T(r.Context(), i18n.KeyBusyBody)))
}

// AddAddress serves POST /account/addresses.
func (h *Handler) AddAddress(w http.ResponseWriter, r *http.Request) {
	u, ok := FromContext(r.Context())
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
		// The address form lives on the account page, so a rejection returns
		// there with the reason rather than rendering a page of its own.
		http.Redirect(w, r, "/account?address=invalid", http.StatusSeeOther)
		return
	}
	if err := h.store.AddAddress(r.Context(), u.ID, a); err != nil {
		h.log.ErrorContext(r.Context(), "add address", "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/account?saved=1", http.StatusSeeOther)
}

// MakeDefaultAddress serves POST /account/addresses/default.
func (h *Handler) MakeDefaultAddress(w http.ResponseWriter, r *http.Request) {
	u, ok := FromContext(r.Context())
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
		// The same answer as an address that never existed. An id off a form
		// that belongs to somebody else must not be distinguishable.
		http.Redirect(w, r, "/account", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set default address", "error", err)
		h.serverError(w, r)
	}
}

// DeleteAddress serves POST /account/addresses/delete.
func (h *Handler) DeleteAddress(w http.ResponseWriter, r *http.Request) {
	u, ok := FromContext(r.Context())
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

// ChangePassword serves POST /account/password.
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	u, ok := FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	// The CURRENT password is required. Without it, a session someone left open
	// on a shared machine is enough to lock its owner out of their own account.
	current := r.PostFormValue("current")
	if _, err := h.store.Authenticate(r.Context(), u.Email, current); err != nil {
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
	// Every session ended, including this one. The cookie goes too, so the
	// browser is not left holding a token that will never work again.
	ClearSessionCookie(w, h.secure)
	http.Redirect(w, r, "/signin?changed=1", http.StatusSeeOther)
}

// Erase serves POST /account/erase.
//
// It runs the schema's erase_user, which is the only door: store holds no
// DELETE on users, so a direct delete is refused — and it would in any case
// leave the delivery details on this account's orders behind.
func (h *Handler) Erase(w http.ResponseWriter, r *http.Request) {
	u, ok := FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	// Typing the account's own email is the confirmation. Erasure is
	// irreversible and a stray click must not reach it.
	if r.PostFormValue("confirm") != u.Email {
		http.Redirect(w, r, "/account?erase=confirm", http.StatusSeeOther)
		return
	}
	if err := h.store.Erase(r.Context(), u.ID); err != nil {
		h.log.ErrorContext(r.Context(), "erase account", "error", err)
		h.serverError(w, r)
		return
	}
	ClearSessionCookie(w, h.secure)
	http.Redirect(w, r, "/?erased=1", http.StatusSeeOther)
}

// Wishlist serves GET /account/wishlist.
func (h *Handler) Wishlist(w http.ResponseWriter, r *http.Request) {
	u, ok := FromContext(r.Context())
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
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyWishlistTitle)}, pages.WishlistView{Products: tiles}))
}

// SaveWishlist serves POST /account/wishlist.
//
// A plain form with a hidden slug and an action, posted from the product page
// or from the wishlist itself. `hx-*` may change what is written BACK — the
// button swapping to its opposite — but this is what performs the write, and it
// works with scripting off.
//
// `return` carries where to send the customer afterwards. It is validated as a
// PATH on this site, never used as given: a redirect target from a form field
// is an open redirect waiting to be found.
func (h *Handler) SaveWishlist(w http.ResponseWriter, r *http.Request) {
	u, ok := FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin?next=/account/wishlist", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
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
	//nolint:gosec // G710: web.SitePathOr refuses anything but a same-site path;
	// the taint analyser cannot see through it, and internal/web's own tests
	// are what keep that claim true.
	http.Redirect(w, r, web.SitePathOr(r.PostFormValue("return"), "/account/wishlist"),
		http.StatusSeeOther)
}

// safeReturn is where to send a customer after a wishlist write.
//
// Only a same-site absolute path is honoured, and everything else falls back to
// the wishlist. A form field used as a redirect target is an open redirect, and
// "//evil.example" is a PATH as far as a naive prefix check is concerned —
// url.Parse is what tells the two apart.

// ChangeEmail serves POST /account/email.
//
// The CURRENT PASSWORD is required, and that is the important guard rather than a
// formality. A session left open on a shared machine could otherwise be pointed at
// the attacker's address and then run /forgot — which is the whole account, taken
// without ever knowing the password. The password change asks for the same thing
// for the same reason.
//
// The change does NOT take effect here. A letter goes to the new address and the
// account keeps the old one until the link is followed, so a mistyped change leaves
// the customer reachable and a malicious one leaves them in control.
func (h *Handler) ChangeEmail(w http.ResponseWriter, r *http.Request) {
	u, ok := FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}

	if _, err := h.store.Authenticate(r.Context(), u.Email, r.PostFormValue("current")); err != nil {
		http.Redirect(w, r, "/account?password=wrong", http.StatusSeeOther)
		return
	}

	addr := r.PostFormValue("email")
	if EmailError(addr) != "" {
		http.Redirect(w, r, "/account?email=invalid", http.StatusSeeOther)
		return
	}

	switch _, err := h.store.RequestVerification(r.Context(), u.ID, addr); {
	case err == nil:
		http.Redirect(w, r, "/account?email=sent", http.StatusSeeOther)
	case errors.Is(err, ErrEmailTaken):
		http.Redirect(w, r, "/account?email=taken", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "request email verification", "error", err)
		h.serverError(w, r)
	}
}

// ResendVerification serves POST /account/email/resend.
//
// No password: it re-sends to the address the account already has, so the worst it
// can do is post a letter to the customer. Asking for a password to be told
// something they already know would be friction with nothing behind it.
func (h *Handler) ResendVerification(w http.ResponseWriter, r *http.Request) {
	u, ok := FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if _, err := h.store.RequestVerification(r.Context(), u.ID, u.Email); err != nil {
		h.log.ErrorContext(r.Context(), "resend email verification", "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/account?email=sent", http.StatusSeeOther)
}

// VerifyPage serves GET /verify.
//
// The token is echoed into a form rather than acted on. Following a link must not
// write: mail clients and security gateways fetch the URLs in a message before a
// human sees it, and a verification a scanner can complete proves nothing about
// whoever owns the mailbox.
func (h *Handler) VerifyPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	web.Render(w, r, h.log, http.StatusOK, pages.NewsletterAction(
		pages.NewsletterMeta(i18n.T(ctx, i18n.KeyVerifyTitle)),
		pages.NewsletterActionView{
			Heading: i18n.T(ctx, i18n.KeyVerifyTitle),
			Body:    i18n.T(ctx, i18n.KeyVerifyBody),
			Action:  "/verify",
			Submit:  i18n.T(ctx, i18n.KeyVerifySubmit),
			Token:   r.URL.Query().Get("token"),
		}))
}

// Verify serves POST /verify.
//
// Deliberately open to a signed-OUT visitor: somebody who changed their address
// may well follow the link on the phone their mail is on rather than the browser
// they are signed in to. The token is the proof, not the session.
func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	ctx := r.Context()

	addr, err := h.store.ConfirmVerification(ctx, r.PostFormValue("token"))
	switch {
	case err == nil:
		web.Render(w, r, h.log, http.StatusOK, pages.NewsletterAction(
			pages.NewsletterMeta(i18n.T(ctx, i18n.KeyVerifyDone)),
			pages.NewsletterActionView{
				Heading: i18n.T(ctx, i18n.KeyVerifyDone),
				Body:    fmt.Sprintf(i18n.T(ctx, i18n.KeyVerifyDoneBody), addr),
			}))
	case errors.Is(err, ErrEmailTaken):
		h.verifyFailed(w, r, i18n.T(ctx, i18n.KeyVerifyTakenTitle), i18n.T(ctx, i18n.KeyVerifyTakenBody))
	case errors.Is(err, ErrVerifyInvalid):
		h.verifyFailed(w, r, i18n.T(ctx, i18n.KeyVerifyDeadTitle), i18n.T(ctx, i18n.KeyVerifyDeadBody))
	default:
		h.log.ErrorContext(ctx, "confirm email verification", "error", err)
		h.verifyFailed(w, r, i18n.T(ctx, i18n.KeyTryAgainTitle), i18n.T(ctx, i18n.KeyTryAgainBody))
	}
}

// verifyFailed answers a link that cannot be acted on. 422: the route exists and
// the request was understood — what failed is the token in it.
func (h *Handler) verifyFailed(w http.ResponseWriter, r *http.Request, heading, body string) {
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.NewsletterAction(
		pages.NewsletterMeta(heading),
		pages.NewsletterActionView{Heading: heading, Body: body}))
}

// oauthOutcome turns the callback's one-shot parameter into a form-level
// message. Errors rather than Notice, because every one of them is a sign-in
// that did not happen.
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
	default:
		return nil
	}
	return map[string]string{"form": i18n.T(ctx, key)}
}

// GoogleSignIn serves GET /auth/google.
//
// A GET, and that is deliberate rather than an exception to the write-face rule.
// It writes no application state: it mints a state and a PKCE verifier into a
// short-lived cookie and redirects. Nothing about the account changes until the
// callback, which is where the rule's "a mutation is a POST" applies — and the
// callback is a redirect FROM GOOGLE, whose method goen does not choose.
func (h *Handler) GoogleSignIn(w http.ResponseWriter, r *http.Request) {
	if !h.google.Enabled() {
		http.NotFound(w, r)
		return
	}
	// Bounded per IP. Each attempt costs a redirect to Google and a cookie, and
	// an unbounded one is a way to make somebody else's server answer requests.
	if retryAfter, ok := h.signinLimit.Allow("oauth:" + clientIP(r)); !ok {
		ratelimit.Refuse(w, retryAfter)
		return
	}

	target, state, err := h.google.AuthorizeURL(SafeNext(r.URL.Query().Get("next")))
	if err != nil {
		h.log.ErrorContext(r.Context(), "build the google authorisation url", "error", err)
		h.serverError(w, r)
		return
	}
	writeOAuthState(w, state, h.secure)
	// target is built entirely by AuthorizeURL from the googleAuthURL constant
	// and server-side values — the client id, the registered redirect URI, and
	// two random secrets. The one piece of visitor input, `next`, goes into the
	// STATE COOKIE and never into this URL; it is validated again by SafeNext
	// when the callback reads it back.
	//nolint:gosec // G710: no request value reaches target
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// GoogleCallback serves GET /auth/google/callback.
//
// Every failure ends at /signin with a message rather than an error page:
// somebody who has just been to Google and consented is mid-sign-in, and a 500
// there reads as goen having lost their account.
func (h *Handler) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	if !h.google.Enabled() {
		http.NotFound(w, r)
		return
	}
	state, ok := readOAuthState(r, h.secure)
	clearOAuthState(w, h.secure)

	// The state comparison is the CSRF defence, and it is the whole reason the
	// cookie carries the __Host- prefix: an attacker who could write it could
	// complete their own authorisation in the victim's browser and sign the
	// victim into the attacker's account, where the victim's next order would
	// land in a stranger's history.
	if !ok || state.Value == "" || r.URL.Query().Get("state") != state.Value {
		h.log.WarnContext(r.Context(), "google callback did not match this browser")
		http.Redirect(w, r, "/signin?oauth=state", http.StatusSeeOther)
		return
	}
	// Google reports a refused consent as error=access_denied rather than as an
	// absent code. Not a failure — somebody changed their mind.
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

	u, err := h.store.SignInWithGoogle(r.Context(), identity)
	switch {
	case err == nil:
	case errors.Is(err, ErrOAuthUnverified):
		http.Redirect(w, r, "/signin?oauth=unverified", http.StatusSeeOther)
		return
	case errors.Is(err, ErrOAuthCollision):
		// The address has an account nobody has proved. Sending them to /forgot
		// is the recovery: the mail goes to the mailbox they just demonstrated
		// they read, and the reset ends every session — which throws out anybody
		// who registered the address without owning it.
		http.Redirect(w, r, "/signin?oauth=collision", http.StatusSeeOther)
		return
	default:
		h.log.ErrorContext(r.Context(), "sign in with google", "error", err)
		http.Redirect(w, r, "/signin?oauth=failed", http.StatusSeeOther)
		return
	}

	h.startSession(w, r, u)
	http.Redirect(w, r, state.Next, http.StatusSeeOther)
}

// UnlinkGoogle serves POST /account/google/unlink.
func (h *Handler) UnlinkGoogle(w http.ResponseWriter, r *http.Request) {
	u, ok := FromContext(r.Context())
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

// writeOAuthState puts the state and the PKCE verifier where the callback can
// read them.
//
// One cookie rather than a server-side row: it is a browser's own scratch space
// for the next few minutes, and a table would need a sweeper, a retention
// decision and a second place for the flow to be wrong. It is HttpOnly so no
// script can read the verifier, and SameSite=Lax so it SURVIVES the redirect
// back from Google — Strict would drop it and every sign-in would fail the state
// check.
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

// readOAuthState reads it back.
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
	// SafeNext again on the way OUT, never trusting what came back: a cookie is
	// something the browser holds, and an open redirect on a sign-in flow is how
	// a phishing page borrows a real login.
	return OAuthState{Value: parts[0], Verifier: parts[1], Next: SafeNext(parts[2])}, true
}

// clearOAuthState removes it, whatever the outcome. A state left behind is a
// live one, and the next callback would match it.
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
