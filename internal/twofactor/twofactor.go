// Package twofactor implements TOTP for goen's back office.
//
// # What it guards, and why there
//
// The second factor guards the BACK OFFICE, not the sign-in. A password gets a
// normal session; reaching /admin needs a code verified in that session and
// recent. This is step-up authentication, and it is deliberately not a second
// step in the login flow: that would need a half-authenticated state to live
// somewhere, and inventing one is how a session that "does not count yet" ends
// up counting.
//
// The property it buys is narrow and worth stating exactly: no back-office
// action — no refund, no store credit, no stock adjustment — happens without a
// second factor proved in the last [StepUpWindow].
//
// # The three things a TOTP implementation gets wrong
//
//  1. **Replay.** A code is valid for its whole 30-second step, and the skew
//     window either side makes that 90 seconds. Every implementation that
//     checks "is this code valid" and stops has a 90-second replay window for a
//     shoulder-surfed code. goen records the accepted step and requires the
//     next one to be strictly greater.
//  2. **A non-constant-time compare.** Comparing the code with == leaks how
//     many leading digits matched, which turns 10^6 guesses into 60.
//  3. **An unbounded skew window.** Accepting ±5 steps to be forgiving about
//     clocks multiplies the replay surface by five and the guessing surface
//     with it. One step either side covers a phone that is 30 seconds out,
//     which is every phone.
package twofactor

import (
	"errors"
	"time"
)

// Step is the TOTP time step. Thirty seconds is RFC 6238's recommendation and
// what every authenticator app assumes; it is not a knob.
const Step = 30 * time.Second

// Skew is how many steps either side of now are accepted.
//
// One. It covers a device whose clock is up to 30 seconds out, which is the
// realistic case. Each extra step widens both the replay window and the number
// of codes valid at any moment, so this is the smallest value that works rather
// than the most forgiving one.
const Skew = 1

// Digits is the code length. Six, because that is what authenticator apps
// generate and a person can read off a screen.
const Digits = 6

// SecretBytes is the shared secret's length.
//
// Twenty bytes — 160 bits, RFC 4226's recommendation and what fits a standard
// base32 QR payload. Longer buys nothing against an attacker who must guess a
// six-digit code, and some authenticators refuse it.
const SecretBytes = 20

// StepUpWindow is how long a verification counts for.
//
// Twelve hours: a working day, so a staff member proves a factor once per shift
// rather than per action. Short enough that a session stolen tomorrow does not
// carry yesterday's proof.
const StepUpWindow = 12 * time.Hour

// Issuer is what an authenticator app shows beside the account.
const Issuer = "goen"

// The errors a caller branches on.
var (
	// ErrDisabled is a deployment with no encryption key. Enrolment is refused
	// rather than storing a secret in the clear.
	ErrDisabled = errors.New("twofactor: no encryption key is configured")
	// ErrBadCode covers a wrong code, a replayed one, and a malformed one. One
	// error on purpose: the caller's next step is identical, and distinguishing
	// them tells an attacker whether a code was ever valid.
	ErrBadCode = errors.New("twofactor: the code is not valid")
	// ErrNotEnrolled is a user with no confirmed credential.
	ErrNotEnrolled = errors.New("twofactor: not enrolled")
	// ErrEnrolled is a user whose factor is already PROVED, asking to enrol
	// again. Refused, because this route is reached with a password and a
	// session: re-enrolling would let somebody holding those replace the factor
	// with their own device and step straight into the back office. Recovery is
	// another admin removing the credential, which is what /admin/staff is for.
	ErrEnrolled = errors.New("twofactor: already enrolled")
)
