// Package twofactor implements TOTP step-up authentication for goen's back
// office. A password gets a normal session; reaching /admin needs a code
// verified in that session inside [StepUpWindow].
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

// Digits is the code length.
const Digits = 6

// SecretBytes is the shared secret's length: 160 bits, RFC 4226's
// recommendation.
const SecretBytes = 20

// StepUpWindow is how long a verification counts for.
const StepUpWindow = 12 * time.Hour

// Issuer is what an authenticator app shows beside the account.
const Issuer = "goen"

// The errors a caller branches on.
var (
	// ErrDisabled is a deployment with no encryption key.
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
	// ErrNotEnrolled is a user with no confirmed credential.
	ErrNotEnrolled = errors.New("twofactor: not enrolled")
	// ErrEnrolled is a user whose factor is already proved, asking to enrol
	// again. Refused: this route is reached with a password alone.
	ErrEnrolled = errors.New("twofactor: already enrolled")
)
