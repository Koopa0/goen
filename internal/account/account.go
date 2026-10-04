// Package account holds goen's authentication and the customer's own pages.
package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

var (
	ErrNotFound         = errors.New("account: not found")
	ErrLastSignInMethod = errors.New("account: that is the only way to sign in")
	// ErrBadCredentials is one error for a wrong email or a wrong password, or
	// the form enumerates accounts.
	ErrBadCredentials = errors.New("account: bad credentials")
	ErrEmailTaken     = errors.New("account: email taken")
	ErrInvalidInput   = errors.New("account: invalid input")
	ErrOpenReturn     = errors.New("account: finish the open return before erasure")
	ErrLastAdmin      = errors.New("account: the last administrator cannot be erased")
	// ErrQuantityAdjusted means adoption or merge succeeded but a line was
	// capped to what the shelf can supply.
	ErrQuantityAdjusted = errors.New("account: quantity adjusted to available stock")
	// ErrCartMergeRefused means a guest line cannot be adopted because the
	// catalogue no longer honours it; the transaction rolls back with both
	// carts unchanged.
	ErrCartMergeRefused = errors.New("account: guest cart contains unavailable merchandise")
)

// SessionCookieName carries __Host-, which refuses a subdomain's forgery.
const SessionCookieName = "__Host-goen_session"

// EraseSignInWindow exists because retyping the address shows intent, not
// identity, so a stolen or unattended session must not do something
// irreversible.
const EraseSignInWindow = 15 * time.Minute

const SessionTTL = 14 * 24 * 60 * 60

// sessionCookieMaxAge outlives the session so the browser can still present
// proof of an order placed signed in, which cart keeps 30 days from the last
// order. The cookie of an ended session grants nothing, and presenting it is
// the only way Authenticate learns to take that proof away.
const sessionCookieMaxAge = SessionTTL + 30*24*60*60

const ResetTTL = 60 * 60

const (
	MinPasswordRunes = 10
	MaxPasswordBytes = 512
)

// These mirror the account form's maxlength attributes; the server owns the
// invariant because a raw HTTP client never sees those hints.
const (
	maxNameRunes         = 60
	maxPhoneRunes        = 30
	maxAddressLabelRunes = 30
	maxPostalCodeRunes   = 6
	maxCityRunes         = 20
	maxDistrictRunes     = 20
	maxStreetRunes       = 200
	maxOAuthSubjectRunes = 255
	maxUserAgentRunes    = 512
)

// argon2id parameters: OWASP's recommended second option (64 MiB, 3 passes, 4
// lanes).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

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
	var timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false
	}
	// HashPassword is the only producer and uses the constants above. Leave
	// room for parameter upgrades, but do not let a corrupt row panic Argon2 or
	// make one sign-in compute at an attacker-chosen scale.
	if timeCost < 1 || timeCost > 8*argonTime ||
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
	got := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, keyLen)
	return subtle.ConstantTimeCompare(got, want) == 1
}

const tokenBytes = 32

func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func SetSessionCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: dev-only opt-out, secure by default
		Name:     sessionCookieName(secure),
		Value:    token,
		Path:     "/",
		MaxAge:   sessionCookieMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

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

// FieldMessages keeps the first message per field so a control shows one reason
// rather than a pile.
func FieldMessages(ctx context.Context, errs []web.FieldRefusal) map[string]string {
	if len(errs) == 0 {
		return nil
	}
	out := make(map[string]string, len(errs))
	for _, e := range errs {
		if _, seen := out[e.Field]; seen {
			continue
		}
		msg := i18n.T(ctx, e.MessageKey)
		// Only the too-short message carries a verb; formatting the others
		// appends %!(EXTRA int=10) beside the field.
		if e.MessageKey == i18n.KeyPasswordTooShort {
			msg = fmt.Sprintf(msg, MinPasswordRunes)
		}
		out[e.Field] = msg
	}
	return out
}

type Credentials struct {
	Email    string
	Password string
	Confirm  string
	Name     string
}

// Trim leaves the password alone: a leading space is a character the visitor
// chose.
func (c *Credentials) Trim() {
	c.Email = strings.TrimSpace(c.Email)
	c.Name = strings.TrimSpace(c.Name)
}

func (c *Credentials) ValidateRegistration() []web.FieldRefusal {
	var errs []web.FieldRefusal
	if k := EmailError(c.Email); k != "" {
		errs = append(errs, web.FieldRefusal{Field: "email", MessageKey: k})
	}
	if k := PasswordError(c.Password); k != "" {
		errs = append(errs, web.FieldRefusal{Field: "password", MessageKey: k})
	} else if c.Confirm != c.Password {
		errs = append(errs, web.FieldRefusal{Field: "confirm", MessageKey: i18n.KeyPasswordsDiffer})
	}
	if utf8.RuneCountInString(c.Name) > maxNameRunes {
		errs = append(errs, web.FieldRefusal{Field: "name", MessageKey: i18n.KeyNameTooLong})
	}
	if hasControl(c.Name) {
		errs = append(errs, web.FieldRefusal{Field: "name", MessageKey: i18n.KeyFieldHasControlChars})
	}
	if hasControl(c.Email) {
		errs = append(errs, web.FieldRefusal{Field: "email", MessageKey: i18n.KeyFieldHasControlChars})
	}
	return errs
}

func EmailError(s string) i18n.Key {
	switch {
	case strings.TrimSpace(s) == "":
		return i18n.KeyCheckoutEmailRequired
	case utf8.RuneCountInString(s) > 254:
		return i18n.KeyCheckoutEmailTooLong
	case !email.Valid(s):
		return i18n.KeyCheckoutEmailMalformed
	}
	return ""
}

func PasswordError(s string) i18n.Key {
	switch {
	case s == "":
		return i18n.KeyPasswordRequired
	case utf8.RuneCountInString(s) < MinPasswordRunes:
		return i18n.KeyPasswordTooShort
	case len(s) > MaxPasswordBytes:
		return i18n.KeyPasswordTooLong
	}
	return ""
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}

func profileInputValid(name, phone string) bool {
	return utf8.RuneCountInString(name) <= maxNameRunes &&
		utf8.RuneCountInString(phone) <= maxPhoneRunes &&
		!hasControl(name) && !hasControl(phone)
}

// A user agent is optional decoration: refuse to persist an unbounded or
// control-bearing value, but never refuse the sign-in. HTTP lets a header carry
// bytes that are not UTF-8, and PostgreSQL refuses the whole session row over
// one of them.
func normaliseUserAgent(s string) string {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxUserAgentRunes || hasControl(s) {
		return ""
	}
	return s
}

// MembershipWindowDays is loyalty.MembershipWindow in days; window_test.go
// keeps the two equal.
const MembershipWindowDays int32 = 365
