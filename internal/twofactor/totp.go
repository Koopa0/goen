package twofactor

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: RFC 6238 specifies HMAC-SHA1; every authenticator app assumes it
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// NewSecret returns a fresh shared secret.
func NewSecret() ([]byte, error) {
	secret := make([]byte, SecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate totp secret: %w", err)
	}
	return secret, nil
}

// EncodeSecret renders a secret the way an authenticator app expects it.
//
// Unpadded base32, upper case. The padding is stripped because several apps
// refuse a secret containing "=", and the standard QR payload never carries it.
func EncodeSecret(secret []byte) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
}

// ProvisioningURI is the otpauth:// URI an authenticator app imports.
//
// goen renders this as text rather than a QR code, because drawing one needs a
// dependency for something the app will also accept typed. Every field is
// escaped: an account name is an email address, and one containing a "?" would
// otherwise start a new query parameter.
func ProvisioningURI(account string, secret []byte) string {
	label := url.PathEscape(Issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", EncodeSecret(secret))
	q.Set("issuer", Issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", strconv.Itoa(Digits))
	q.Set("period", strconv.Itoa(int(Step.Seconds())))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// StepAt is the time step a moment falls in.
func StepAt(t time.Time) int64 { return t.Unix() / int64(Step.Seconds()) }

// Code is the six digits for one secret and one step.
func Code(secret []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step)) //nolint:gosec // G115: a step is a positive Unix-derived value

	mac := hmac.New(sha1.New, secret)
	mac.Write(counter[:])
	sum := mac.Sum(nil)

	// RFC 4226 dynamic truncation: the low nibble of the last byte picks where
	// to read four bytes from, and the top bit is masked off so the result is
	// positive on every platform.
	offset := sum[len(sum)-1] & 0x0F
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7FFFFFFF

	mod := uint32(1)
	for range Digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", Digits, value%mod)
}

// Verify checks a submitted code and returns the step it matched.
//
// lastStep is the most recent step already accepted for this credential, or 0
// for one that has never been used. A match at or below it is REFUSED even
// though the code is arithmetically correct — that is the replay defence, and
// it is why this returns the step rather than a bool.
//
// The comparison is constant-time. A byte-wise == leaks how many leading digits
// matched through timing, which turns a million guesses into sixty.
func Verify(secret []byte, submitted string, lastStep int64, now time.Time) (int64, error) {
	submitted = strings.TrimSpace(submitted)
	// Spaces inside are tolerated because some apps display "123 456", and a
	// person reading one off a screen types what they see.
	submitted = strings.ReplaceAll(submitted, " ", "")
	if len(submitted) != Digits {
		return 0, ErrBadCode
	}

	current := StepAt(now)
	// Newest first, so a valid current code costs one HMAC rather than three.
	// The loop always runs to completion for a WRONG code, which is what keeps
	// a miss from being distinguishable by how early it failed.
	matched := int64(-1)
	for offset := int64(Skew); offset >= -Skew; offset-- {
		step := current - offset
		if subtle.ConstantTimeCompare([]byte(Code(secret, step)), []byte(submitted)) == 1 {
			matched = step
		}
	}
	if matched < 0 {
		return 0, ErrBadCode
	}
	// Strictly greater: a code already used cannot be used again, and an older
	// one cannot be used after a newer has been seen.
	if matched <= lastStep {
		return 0, ErrBadCode
	}
	return matched, nil
}
