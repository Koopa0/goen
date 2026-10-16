// Package newsletter is goen's mailing list: who has asked to be on it, who has
// asked to leave, and the two links that are the only way either happens.
//
// Both links open a page carrying a form rather than acting on the GET, because
// mail clients and security gateways fetch the URLs in a message before a human
// sees it.
package newsletter

import (
	"errors"
	"time"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
)

const ConfirmTTL = 48 * time.Hour

// ConfirmationRetain is how long an address nobody confirmed stays in
// newsletter_confirmations, counted from the request. The link is dead after
// [ConfirmTTL]; the rest is a grace for a late click or a support question, and
// no longer, because the address belongs to somebody who never said yes.
const ConfirmationRetain = 14 * 24 * time.Hour

const ConfirmationSweepInterval = 24 * time.Hour

// ErrNotFound is one error for unknown, spent and expired, so it cannot tell
// somebody walking tokens which guess was real.
var ErrNotFound = errors.New("newsletter: that link is not usable")

// Outcome is what a submission did, which the caller must NOT show the visitor:
// it discloses whether an address is on the list.
type Outcome int

const (
	Requested Outcome = iota
	AlreadyActive
)

func Validate(addr string) i18n.Key {
	switch {
	case addr == "":
		return i18n.KeyEmailRequired
	case len(addr) > email.MaxBytes:
		return i18n.KeyEmailTooLong
	case !email.Valid(addr):
		return i18n.KeyEmailMalformed
	}
	return ""
}
