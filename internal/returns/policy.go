package returns

import "strings"

// PolicyWindow is the advertised return window a request fell in when it was
// filed. It is counted from the request clock against delivery on the shop
// calendar, never from now(): a later staff decision must not move a filed
// request into a different window.
type PolicyWindow string

// The four windows ReturnQueue and ReturnForDecision name. "within" keeps the
// existing column value so the queue's statutory label does not drift.
const (
	WindowUndelivered PolicyWindow = "undelivered"
	WindowStatutory   PolicyWindow = "within"
	WindowGoodwill    PolicyWindow = "goodwill"
	WindowLate        PolicyWindow = "after"
)

var knownPolicyWindows = [...]PolicyWindow{
	WindowUndelivered,
	WindowStatutory,
	WindowGoodwill,
	WindowLate,
}

// ParsePolicyWindow reads a window the decision queries named. Unknown values
// fail closed: inventing a window would let a decision claim an entitlement
// the clocks did not earn.
func ParsePolicyWindow(s string) (PolicyWindow, bool) {
	w := PolicyWindow(s)
	switch w {
	case WindowUndelivered, WindowStatutory, WindowGoodwill, WindowLate:
		return w, true
	default:
		return "", false
	}
}

// Entitlement is what an approval may claim. It is a closed set; days 8–14
// are a window, not an entitlement, until unused-and-complete is a fact the
// decision can read.
type Entitlement string

// The two claims an approval can currently make. Goodwill is omitted on
// purpose: production has no unused / complete observation at decision time.
const (
	EntitlementStatutory Entitlement = "statutory"
	EntitlementException Entitlement = "exception"
)

// EntitlementFor is what approving this window may claim. The statutory
// seven days are a right. Past the advertised 14 days, or before delivery,
// approval is a staff exception. Days 8–14 are the shop's conditional offer;
// without unused-and-complete facts the decision records no entitlement.
func EntitlementFor(window PolicyWindow) Entitlement {
	switch window {
	case WindowStatutory:
		return EntitlementStatutory
	case WindowLate, WindowUndelivered:
		return EntitlementException
	default:
		return ""
	}
}

// RejectionGround is the staff-declared reason for declining a return. It is
// a closed set: free-form resolution prose is audit copy, not a ground.
type RejectionGround string

// The two grounds a rejection may name today. MissingReason is the one
// Consumer Protection Act §19 I forbids inside the statutory seven days when
// the customer filed without stating why.
const (
	RejectionGroundMissingReason RejectionGround = "missing_reason"
	RejectionGroundIneligible    RejectionGround = "ineligible"
)

// ParseRejectionGround reads the ground a reject form named. Unknown values
// fail closed so an invented ground cannot bypass the statutory rule.
func ParseRejectionGround(s string) (RejectionGround, bool) {
	g := RejectionGround(s)
	switch g {
	case RejectionGroundMissingReason, RejectionGroundIneligible:
		return g, true
	default:
		return "", false
	}
}

// RefuseRejection reports the one rejection Consumer Protection Act §19 I
// forbids: inside the seven days, refusing solely because no reason was given.
// The ground must be declared explicitly; resolution prose is not proof of a
// different ground, and an absent or unknown ground is treated as missing_reason.
func RefuseRejection(window PolicyWindow, customerReason string, ground RejectionGround) bool {
	if window != WindowStatutory {
		return false
	}
	if strings.TrimSpace(customerReason) != "" {
		return false
	}
	if ground == RejectionGroundIneligible {
		return false
	}
	return true
}
