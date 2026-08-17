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

// EncodeSecret renders a secret the way an authenticator app expects it:
// unpadded base32, because several apps refuse a secret containing "=".
func EncodeSecret(secret []byte) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
}

// ProvisioningURI is the otpauth:// URI an authenticator app imports.
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

	// RFC 4226 dynamic truncation.
	offset := sum[len(sum)-1] & 0x0F
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7FFFFFFF

	mod := uint32(1)
	for range Digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", Digits, value%mod)
}

// Verify checks a submitted code and returns the step it matched. A match at or
// below lastStep is refused though arithmetically correct, and the compare is
// constant-time: a byte-wise == turns a million guesses into 60.
func Verify(secret []byte, submitted string, lastStep int64, now time.Time) (int64, error) {
	submitted = strings.TrimSpace(submitted)
	// Some apps display "123 456" and a person types what they see.
	submitted = strings.ReplaceAll(submitted, " ", "")
	if len(submitted) != Digits {
		return 0, ErrBadCode
	}

	current := StepAt(now)
	// The loop always runs to completion, so a miss is not distinguishable by
	// how early it failed.
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
	if matched <= lastStep {
		return 0, ErrBadCode
	}
	return matched, nil
}
