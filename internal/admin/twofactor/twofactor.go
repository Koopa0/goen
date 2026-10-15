// Package twofactor implements TOTP step-up authentication for the back office:
// enrolment, verification and the session check. Who counts as staff is
// internal/admin/staff.
package twofactor

import (
	"errors"
	"time"
)

// Step is the TOTP time step. RFC 6238 recommends 30 seconds and every
// authenticator app assumes it.
const Step = 30 * time.Second

// Skew is how many steps either side of now are accepted. Each extra step
// multiplies both the replay window and the guessing surface.
const Skew = 1

const Digits = 6

// SecretBytes is the shared secret's length: 160 bits, RFC 4226's
// recommendation.
const SecretBytes = 20

const StepUpWindow = 12 * time.Hour

// MailedCodeTTL is how long the code mailed when enrolment begins can confirm
// it. The letter states it in words.
const MailedCodeTTL = 15 * time.Minute

const Issuer = "goen"

// MaxFailures is how many wrong codes in a row lock a confirmed factor until
// another admin removes it and its holder enrols again. NIST SP 800-63B-4
// §3.2.2 sets 100 as the most a verifier may allow and requires a rebind once
// it is reached. Ten is the per-minute throttle's burst, which a restart
// refills and this does not, and the chance of guessing before the lock is 10
// guesses × 3 accepted codes (the skew window) in a million. An accepted code
// clears the count.
const MaxFailures = 10

var (
	ErrDisabled = errors.New("twofactor: no encryption key is configured")
	// ErrBadCode covers a wrong code, a replayed one and a malformed one — one
	// error, because distinguishing them tells an attacker whether a code was
	// ever valid.
	ErrBadCode = errors.New("twofactor: the code is not valid")
	// ErrSecretUnreadable is a stored credential that does not decrypt: the key
	// changed, or the row was tampered with. It is never a wrong code — nothing
	// the staff member types can fix it, so it must not be reported as one.
	// Another admin must remove the factor.
	ErrSecretUnreadable = errors.New("twofactor: the stored secret does not open under the configured key")
	ErrNotEnrolled      = errors.New("twofactor: not enrolled")
	// ErrLocked is a factor that has refused [MaxFailures] codes in a row. It
	// accepts none, right or wrong, until another admin removes it.
	ErrLocked = errors.New("twofactor: locked after too many wrong codes")
	// ErrEnrolled is a user whose factor is already proved, asking to enrol
	// again. Refused: this route is reached with a password alone.
	ErrEnrolled = errors.New("twofactor: already enrolled")
)
