package auth

import (
	"encoding/hex"
	"net/http"
	"time"

	"github.com/koopa0/goen/internal/ratelimit"
)

// passwordGuess is what one password attempt has set aside, and holds nothing
// for the demo account, whose password is printed on the sign-in page.
type passwordGuess []*ratelimit.Reservation

// reservePasswordGuess sets aside one guess against address, before the hash.
// Two budgets, both charged only by a miss. The first is the account as seen
// from the caller's network, so a stranger's wrong passwords spend only the
// allowance of the stranger's own network and never the owner's. The second
// is the account from every network at once, the bound on a guess spread over
// many networks; it is larger than the first because anyone can spend it.
//
// The first is taken first, and a refusal by either gives the other back: a
// request that did no work has guessed nothing.
//
// A browser that presents a remembered row for this account (device, the row's
// digest) spends neither: it is charged to a budget of its own, as large as
// one network's, which nobody else can reach. That is what keeps the owner in
// while a stranger holds the account's ceiling empty; countDeviceMiss bounds
// what a stolen cookie gets from it.
func (h *Handler) reservePasswordGuess(r *http.Request, address string, device []byte) (passwordGuess, time.Duration, bool) {
	if device != nil {
		own, retryAfter, ok := h.signinLimit.Reserve("device:" + hex.EncodeToString(device) + "|" + address)
		if !ok {
			return nil, retryAfter, false
		}
		return passwordGuess{own}, 0, true
	}
	network, retryAfter, ok := h.signinLimit.Reserve("account:" + ratelimit.ClientNetwork(r) + "|" + address)
	if !ok {
		return nil, retryAfter, false
	}
	ceiling, retryAfter, ok := h.guessCeiling.Reserve("account:" + address)
	if !ok {
		network.Refund()
		return nil, retryAfter, false
	}
	return passwordGuess{network, ceiling}, 0, true
}

// settle keeps the tokens when the password was checked and wrong, and gives
// them back otherwise: the right password is never charged, nor is a lookup
// that failed. Only the first call counts.
func (g passwordGuess) settle(missed bool) {
	for _, reservation := range g {
		if missed {
			reservation.Keep()
		} else {
			reservation.Refund()
		}
	}
}
