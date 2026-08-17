// Package newsletter is goen's mailing list: who has asked to be on it, who has
// asked to leave, and the two links that are the only way either happens.
//
// Both links open a page carrying a form rather than acting on the GET, because
// mail clients and security gateways fetch the URLs in a message before a human
// sees it.
package newsletter

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
)

// ConfirmTTL is how long a confirmation link works.
const ConfirmTTL = 48 * time.Hour

// ErrNotFound is a link that matches nothing: unknown, spent, or expired. One
// error for all three, so it cannot tell somebody walking tokens which of their
// guesses was real.
var ErrNotFound = errors.New("newsletter: that link is not usable")

// Outcome is what a submission did, which the caller must NOT show the visitor:
// it discloses whether an address is on the list.
type Outcome int

const (
	// Requested means a confirmation link is on its way.
	Requested Outcome = iota
	// AlreadyActive means the address is on the list and nothing was sent.
	AlreadyActive
)

const tokenBytes = 32

// NewToken returns a fresh opaque token for a link in an email.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("reading token entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken digests a token for storage and lookup.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Validate returns the KEY of the message to show for addr, or "" when it is
// acceptable.
func Validate(addr string) i18n.Key {
	switch {
	case addr == "":
		return i18n.KeyEmailRequired
	case len(addr) > email.Max:
		return i18n.KeyEmailTooLong
	case !email.Valid(addr):
		return i18n.KeyEmailMalformed
	}
	return ""
}
