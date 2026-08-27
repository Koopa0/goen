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

// CartFinder finds the cart a request's cookie names, so sign-in can adopt it.
type CartFinder interface {
	CartIDForRequest(ctx context.Context, r *http.Request) (uuid.UUID, bool)
}

// Handler serves sign-in, registration and the customer's own pages.
type Handler struct {
	signinLimit *ratelimit.Limiter
	resetLimit  *ratelimit.Limiter
	store       *Store
	carts       CartFinder
	log         *slog.Logger
	secure      bool
	google      *Google
}

// NewHandler returns a Handler over store.
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
			Every: 15 * time.Second, Burst: 6, TTL: time.Hour,
		}),
		resetLimit: ratelimit.New(ratelimit.Config{
			Every: time.Minute, Burst: 3, TTL: time.Hour,
		}),
	}
}

type contextKey struct{}

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
			// and clearing on any error signs every customer and every staff
			// member out at once during a blip — unrecoverably, because the row
			// survives and the browser no longer holds the token for it.
			if errors.Is(err, ErrNotFound) {
				ClearSessionCookie(w, h.secure)
			} else {
				h.log.ErrorContext(r.Context(), "read session", "error", err)
			}
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
	})
}

// RequireUser wraps a handler that needs an account, sending a visitor to sign in.
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
		Next:         web.SitePathOr(r.URL.Query().Get("next"), "/account"),
		GoogleSignIn: h.google.Enabled(),
	}
	switch {
	case r.URL.Query().Get("registered") == "1":
		view.Notice = i18n.T(r.Context(), i18n.KeyAccountCreated)
	case r.URL.Query().Get("reset") == "1":
		view.Notice = i18n.T(r.Context(), i18n.KeyPasswordReset)
	default:
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
	next := web.SitePathOr(r.PostFormValue("next"), "/account")

	// Before Authenticate: argon2 at 64 MiB is the cost this limit protects.
	if retryAfter, ok := h.signinLimit.Allow("account:" + strings.ToLower(strings.TrimSpace(email))); !ok {
		h.log.WarnContext(r.Context(), "sign-in throttled by account")
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}

	u, err := h.store.Authenticate(r.Context(), email, password)
	if err != nil {
		if !errors.Is(err, ErrBadCredentials) {
			h.log.ErrorContext(r.Context(), "authenticate", "error", err)
			h.serverError(w, r)
			return
		}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.SignIn(pages.SignInMeta(r.Context()),
			pages.AuthView{
				Email: email, Next: next,
				Errors: map[string]string{"form": i18n.T(r.Context(), i18n.KeyBadCredentials)},
			}))
		return
	}

	h.startSession(w, r, u)
	http.Redirect(w, r, next, http.StatusSeeOther) //nolint:gosec // G710: bounded by web.SitePathOr
}

// RegisterPage serves GET /register.
func (h *Handler) RegisterPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := FromContext(r.Context()); ok {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Register(pages.RegisterMeta(r.Context()),
		pages.AuthView{Next: web.SitePathOr(r.URL.Query().Get("next"), "/account")}))
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
	next := web.SitePathOr(r.PostFormValue("next"), "/account")

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

	// Swallowed: a mail problem must not become a lost registration.
	if _, err := h.store.RequestVerification(r.Context(), u.ID, u.Email); err != nil {
		h.log.WarnContext(r.Context(), "request verification at registration", "error", err)
	}

	h.startSession(w, r, u)
	http.Redirect(w, r, next, http.StatusSeeOther) //nolint:gosec // G710: bounded by web.SitePathOr
}

// SignOut serves POST /signout.
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
	// A failure here loses the badge and not the page, which is the safe direction.
	if state, vErr := h.store.EmailVerification(r.Context(), u.ID); vErr != nil {
		h.log.WarnContext(r.Context(), "read verification state", "error", vErr)
	} else {
		view.EmailVerified, view.PendingEmail = state.Verified, state.PendingEmail
	}
	view.Notice = accountNotice(r)
	web.Render(w, r, h.log, http.StatusOK, pages.Account(pages.AccountMeta(r.Context()), &view))
}

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

func (h *Handler) startSession(w http.ResponseWriter, r *http.Request, u User) {
	token, err := h.store.StartSession(r.Context(), u.ID, r.UserAgent(), clientIP(r))
	if err != nil {
		h.log.ErrorContext(r.Context(), "start session", "error", err)
		h.serverError(w, r)
		return
	}
	SetSessionCookie(w, token, h.secure)

	// A failed cart merge must not take the sign-in down with it.
	if h.carts != nil {
		if cartID, ok := h.carts.CartIDForRequest(r.Context(), r); ok {
			if err := h.store.AdoptCart(r.Context(), u.ID, cartID); err != nil {
				h.log.ErrorContext(r.Context(), "adopt cart", "error", err, "user_id", u.ID)
			}
		}
	}
}

func clientIP(r *http.Request) string {
	return ratelimit.ClientIP(r)
}

func fieldMessages(ctx context.Context, errs []FieldError) map[string]string {
	if len(errs) == 0 {
		return nil
	}
	out := make(map[string]string, len(errs))
	for _, e := range errs {
		if _, seen := out[e.Field]; seen {
			continue
		}
		msg := i18n.T(ctx, e.MessageKey)
		// Only the too-short message carries a verb. Formatting every message
		// with the length appended %!(EXTRA int=10) to the six that do not — on
		// the registration form, which is where somebody decides whether to
		// trust this site with a password.
		if e.MessageKey == i18n.KeyPasswordTooShort {
			msg = fmt.Sprintf(msg, MinPasswordRunes)
		}
		out[e.Field] = msg
	}
	return out
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
	ClearSessionCookie(w, h.secure)
	http.Redirect(w, r, "/signin?changed=1", http.StatusSeeOther)
}

// Erase serves POST /account/erase.
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
func (h *Handler) SaveWishlist(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	// web.SitePathOr refuses anything but a same-site path, which is what makes
	// a redirect target read off a form safe to use.
	back := web.SitePathOr(r.PostFormValue("return"), "/account/wishlist")

	u, ok := FromContext(r.Context())
	if !ok {
		// Back to the product, not to an empty wishlist: somebody who pressed
		// save was looking at something, and sending them to a list of nothing
		// after signing in loses both the item and the page they were on. The
		// form is read BEFORE the check for exactly this.
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

// ChangeEmail serves POST /account/email.
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

func (h *Handler) verifyFailed(w http.ResponseWriter, r *http.Request, heading, body string) {
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.NewsletterAction(
		pages.NewsletterMeta(heading),
		pages.NewsletterActionView{Heading: heading, Body: body}))
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
	default:
		return nil
	}
	return map[string]string{"form": i18n.T(ctx, key)}
}

// GoogleSignIn serves GET /auth/google.
func (h *Handler) GoogleSignIn(w http.ResponseWriter, r *http.Request) {
	if !h.google.Enabled() {
		http.NotFound(w, r)
		return
	}
	if retryAfter, ok := h.signinLimit.Allow("oauth:" + clientIP(r)); !ok {
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

// GoogleCallback serves GET /auth/google/callback. Every failure ends at /signin
// with a message rather than an error page: the customer is mid-sign-in.
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

// clearOAuthState removes it; a state left behind would match the next callback.
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
