// Package account holds goen's authentication and the customer's own pages.
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

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
)

var (
	// ErrNotFound is an account, order or token that does not exist.
	ErrNotFound = errors.New("account: not found")
	// ErrLastSignInMethod is unlinking the only way into an account.
	ErrLastSignInMethod = errors.New("account: that is the only way to sign in")
	// ErrBadCredentials is a wrong email or password: one error for both, or the form enumerates accounts.
	ErrBadCredentials = errors.New("account: bad credentials")
	// ErrEmailTaken is a registration for an address that already has an account.
	ErrEmailTaken = errors.New("account: email taken")
)

// SessionCookieName is the session cookie; __Host- refuses a subdomain's forgery.
const SessionCookieName = "__Host-goen_session"

// SessionTTL is how long a session lives.
const SessionTTL = 14 * 24 * 60 * 60

// ResetTTL is how long a password-reset link is good for.
const ResetTTL = 60 * 60

// Password bounds.
const (
	MinPasswordRunes = 10
	MaxPasswordBytes = 512
)

// argon2id parameters: OWASP's recommended second option (64 MiB, 3 passes, 4 lanes).
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

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password produced encoded.
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
	// HashPassword is the only producer and uses the constants above. Keep some
	// room for parameter upgrades, but do not let a corrupt row panic Argon2 or
	// make one sign-in allocate or compute at an attacker-chosen scale.
	if time < 1 || time > 8*argonTime ||
		threads < 1 || threads > 8*argonThreads ||
		memory > 8*argonMemory {
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
	if len(want) == 0 || len(want) > 1024 {
		return false
	}
	keyLen := uint32(len(want)) //nolint:gosec // G115: bounded to [1, 1024] just above
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, keyLen)
	return subtle.ConstantTimeCompare(got, want) == 1
}

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

// IsAdmin reports whether this account may change who works here.
func (u User) IsAdmin() bool { return u.Role == "admin" }

// FieldError names one rejected field and why.
type FieldError struct {
	Field      string
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
// character the visitor chose.
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
	case !email.Valid(s):
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

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// MembershipWindowDays mirrors loyalty.MembershipWindow; importing it would be a cycle.
const MembershipWindowDays int32 = 365
