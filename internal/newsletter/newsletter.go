// Package newsletter is goen's mailing list: who has asked to be on it, who has
// asked to leave, and the two links that are the only way either happens.
//
// # Why an address is not a subscriber
//
// The signup form lives in the footer of every page and anybody can type
// anybody's address into it. So a submission records a REQUEST — a row in
// newsletter_confirmations — and the address joins the list only when a link
// sent to that mailbox is followed. That is double opt-in, and it is not a
// nicety: without it the form is a way to sign a stranger up, and the shop earns
// its sending reputation from people who never asked.
//
// The two states live in two tables rather than two columns of one, so that a
// query which forgets to ask whether an address was confirmed still cannot
// reach an unconfirmed one. A predicate every future query must remember is a
// predicate one of them will not.
//
// # Why both links are POST
//
// A GET that mutates is against goen's write-face rule anyway, but here it also
// breaks the feature. Mail clients, link scanners and corporate security
// gateways fetch the URLs in a message before a human sees it: a GET confirm
// link would be followed by the scanner, confirming a subscription its owner
// never agreed to — the whole point of the confirmation gone. A GET unsubscribe
// link would take people off the list without their knowing.
//
// So each link opens a page carrying a form, and the button on it is what
// writes.
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
//
// Two days, where a password reset gets one hour. The reset link IS a
// credential — anybody holding it can take an account — so its lifetime is the
// whole exposure and an hour is the right kind of hostile. This link joins a
// mailing list. Expiring it overnight would refuse somebody who read their mail
// the next morning, and the worst a leaked one does is subscribe an address that
// then holds a working unsubscribe link.
const ConfirmTTL = 48 * time.Hour

// ErrNotFound is a link that matches nothing: unknown, spent, or expired.
//
// One error for all three. Telling them apart tells somebody walking tokens
// which of their guesses was ever real, and the reader's next step is the same
// in every case — subscribe again.
var ErrNotFound = errors.New("newsletter: that link is not usable")

// Outcome is what a submission did, which the caller must NOT show the visitor.
//
// Every outcome is answered with the same page, because the alternative is a
// form that reports whether a given address is on the list to anybody who cares
// to type one in. The value exists so the handler knows whether there is a
// message to enqueue, and so a test can tell the cases apart.
type Outcome int

const (
	// Requested means a confirmation link is on its way.
	Requested Outcome = iota
	// AlreadyActive means the address is on the list and nothing was sent.
	// Mailing it again on demand is how the form becomes a way to flood
	// somebody's inbox.
	AlreadyActive
)

// tokenBytes is the entropy behind a confirm or unsubscribe token. The same 32
// as a session token: these are guessed at over the open internet, and the
// unsubscribe one never expires.
const tokenBytes = 32

// NewToken returns a fresh opaque token for a link in an email.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("reading token entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken digests a token for storage and lookup. Only the digest is stored,
// so a read of the table does not hand somebody the links.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Validate returns the KEY of the message to show for addr, or "" when it is
// acceptable.
//
// A key rather than a string, because this runs where there is no request and
// therefore no locale: the caller renders it. Returning the sentence itself is
// what put Chinese into a package that has no way of knowing who is reading.
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
