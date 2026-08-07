// Package account holds goen's authentication and the customer's own pages.
//
// Two things here are security-critical and neither is left to a comment:
// passwords are hashed with argon2id, and both session and reset tokens are
// stored as digests so a leaked table hands over nothing live.
package account

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"golang.org/x/crypto/argon2"

	"github.com/koopa0/goen/internal/i18n"
)

// Errors a handler branches on.
var (
	// ErrNotFound is an account, order or token that does not exist.
	ErrNotFound = errors.New("account: not found")
	// ErrLastSignInMethod is unlinking the only way into an account. The same
	// shape as /admin/staff refusing to revoke the last admin: leaving nobody
	// able to get in is not a state a form should be able to reach.
	ErrLastSignInMethod = errors.New("account: that is the only way to sign in")
	// ErrBadCredentials is a wrong email or a wrong password. Deliberately ONE
	// error for both: telling them apart tells an attacker which emails are
	// registered.
	ErrBadCredentials = errors.New("account: bad credentials")
	// ErrEmailTaken is a registration for an address that already has an
	// account.
	ErrEmailTaken = errors.New("account: email taken")
)

// SessionCookieName is the session cookie. __Host- binds it to this exact
// origin with no Domain attribute and requires Secure, which is what stops a
// sibling subdomain from writing a session cookie the site would then trust.
const SessionCookieName = "__Host-goen_session"

// SessionTTL is how long a session lives. Long enough not to be a nuisance,
// short enough that a forgotten sign-in on a shared machine expires.
const SessionTTL = 14 * 24 * 60 * 60 // 14 days, in seconds

// ResetTTL is how long a password-reset link is good for. Short: it is sent by
// email, which is not a secure channel, and a link that works for a day is a
// day of exposure.
const ResetTTL = 60 * 60 // 1 hour, in seconds

// Password rules. A length floor rather than a character-class rule: length is
// what actually resists guessing, and forcing a symbol produces "Password1!"
// on every account.
const (
	MinPasswordRunes = 10
	MaxPasswordBytes = 512 // argon2 will hash anything; this bounds the work
)

// argon2id parameters.
//
// These are the OWASP-recommended second option (64 MiB, 3 passes, 4 lanes),
// chosen over bcrypt because argon2id resists GPU cracking that bcrypt's small
// memory footprint does not. They are encoded into every hash, so raising them
// later re-hashes on next sign-in rather than invalidating existing passwords.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword returns an encoded argon2id hash, salt and parameters included.
func HashPassword(password string) (string, error) {
	if len(password) > MaxPasswordBytes {
		return "", errors.New("account: password too long to hash")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	// The parameters travel with the hash so a future change can be detected
	// per-account rather than forcing every password to be reset at once.
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password produced encoded.
//
// The comparison is constant-time. A byte-by-byte compare leaks, through timing,
// how much of a guess was right — which turns an offline problem into an online
// one.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	// The key length comes from the stored hash. Bound it before the conversion:
	// a corrupted column must not be able to ask argon2 for an absurd key.
	if len(want) == 0 || len(want) > 1024 {
		return false
	}
	keyLen := uint32(len(want)) //nolint:gosec // G115: bounded to [1, 1024] just above
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, keyLen)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// tokenBytes is the entropy behind a session or reset token.
const tokenBytes = 32

// NewToken returns a fresh opaque token.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken digests a token for storage and lookup.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// SetSessionCookie writes the session cookie.
func SetSessionCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: dev-only opt-out, secure by default
		Name:     sessionCookieName(secure),
		Value:    token,
		Path:     "/",
		MaxAge:   SessionTTL,
		HttpOnly: true,
		Secure:   secure,
		// Strict, not Lax: a session cookie has no reason to ride a cross-site
		// navigation, and Strict is the cheapest CSRF defence there is. The cart
		// cookie is Lax because a cart must survive arriving from a link.
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearSessionCookie expires the session cookie.
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: dev-only opt-out, secure by default
		Name:     sessionCookieName(secure),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// ReadSessionCookie returns the token a request carries, or "".
func ReadSessionCookie(r *http.Request, secure bool) string {
	c, err := r.Cookie(sessionCookieName(secure))
	if err != nil {
		return ""
	}
	return c.Value
}

func sessionCookieName(secure bool) string {
	if secure {
		return SessionCookieName
	}
	return "goen_session"
}

// User is a signed-in customer, as the rest of the application sees them.
type User struct {
	ID    string
	Email string
	Name  string
	Role  string
}

// IsStaff reports whether this account may reach the back office.
func (u User) IsStaff() bool { return u.Role == "staff" || u.Role == "admin" }

// IsAdmin reports whether this account may change WHO WORKS HERE.
//
// The schema has had three roles since the users table was written, and this was
// the missing half: [User.IsStaff] accepts both, and it was the ONLY role
// predicate in the tree, so `staff` and `admin` were the same thing everywhere a
// request was decided. Every /admin/staff route was gated on IsStaff, and
// AddStaff takes the role off the form — so any staff member could POST their
// own address with role=admin and be promoted, revoke a colleague's access, or
// strip an admin's second factor.
//
// It did not even need self-promotion: POST a new address with role=admin, then
// use /forgot, which is the flow the staff query documents as intended. The
// distinction the schema drew was real and nothing in Go had ever read it.
func (u User) IsAdmin() bool { return u.Role == "admin" }

// FieldError names one rejected field and why.
type FieldError struct {
	Field string
	// MessageKey names the reason. A key rather than a sentence: validation runs
	// from a handler, a store and a test, and none of them is where the words
	// belong. The caller renders it.
	MessageKey i18n.Key
}

// Credentials is a sign-in or registration submission.
type Credentials struct {
	Email    string
	Password string
	Confirm  string
	Name     string
}

// Trim normalises whitespace. The password is NOT trimmed: a leading space is a
// character the visitor chose, and silently removing it makes a password that
// works here fail in a password manager.
func (c *Credentials) Trim() {
	c.Email = strings.TrimSpace(c.Email)
	c.Name = strings.TrimSpace(c.Name)
}

// ValidateRegistration checks a new account's details.
func (c *Credentials) ValidateRegistration() []FieldError {
	var errs []FieldError
	if k := EmailError(c.Email); k != "" {
		errs = append(errs, FieldError{Field: "email", MessageKey: k})
	}
	if k := PasswordError(c.Password); k != "" {
		errs = append(errs, FieldError{Field: "password", MessageKey: k})
	} else if c.Confirm != c.Password {
		errs = append(errs, FieldError{Field: "confirm", MessageKey: i18n.KeyPasswordsDiffer})
	}
	if n := len([]rune(c.Name)); n > 60 {
		errs = append(errs, FieldError{Field: "name", MessageKey: i18n.KeyNameTooLong})
	}
	if hasControl(c.Name) || hasControl(c.Email) {
		errs = append(errs, FieldError{Field: "name", MessageKey: i18n.KeyFieldHasControlChars})
	}
	return errs
}

// EmailError returns why an address is unusable, or "".
func EmailError(s string) i18n.Key {
	switch {
	case strings.TrimSpace(s) == "":
		return i18n.KeyCheckoutEmailRequired
	case len([]rune(s)) > 254:
		return i18n.KeyCheckoutEmailTooLong
	case !looksLikeEmail(s):
		return i18n.KeyCheckoutEmailMalformed
	}
	return ""
}

// PasswordError returns why a password is unusable, or "".
func PasswordError(s string) i18n.Key {
	switch {
	case s == "":
		return i18n.KeyPasswordRequired
	case len([]rune(s)) < MinPasswordRunes:
		return i18n.KeyPasswordTooShort
	case len(s) > MaxPasswordBytes:
		return i18n.KeyPasswordTooLong
	}
	return ""
}

func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 || strings.Count(s, "@") != 1 {
		return false
	}
	domain := s[at+1:]
	dot := strings.IndexByte(domain, '.')
	return dot > 0 && dot < len(domain)-1 && !strings.ContainsAny(s, " \t")
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// SafeNext bounds a post-sign-in redirect to a path within this site.
//
// This is what stops ?next= from becoming an open redirect. A value must start
// with a single slash and no second one: "//evil.example" is a protocol-relative
// URL the browser reads as another origin, and "/\evil" is treated the same way
// by some browsers.
func SafeNext(next string) string {
	const fallback = "/account"
	if next == "" || !strings.HasPrefix(next, "/") {
		return fallback
	}
	if strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return fallback
	}
	if strings.ContainsAny(next, "\r\n") || hasControl(next) {
		return fallback
	}
	return next
}

// MembershipWindowDays mirrors loyalty.MembershipWindow.
//
// Not imported: internal/loyalty imports internal/account, so the dependency
// only runs one way — and one number is a poor reason to invert it.
// TestTheMembershipWindowMatchesTheProgramme keeps them equal, the same
// arrangement payment.LoyaltyValidityDays has.
const MembershipWindowDays int32 = 365
