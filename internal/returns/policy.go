package returns

import (
	"errors"
	"fmt"
)

// ErrPolicy is a decision the advertised return policy refuses. The request
// stays open; nothing is paid.
var ErrPolicy = errors.New("returns: advertised policy refuses this decision")

// PolicyWindow is the advertised return window a request — or one of its
// lines — fell in when it was filed. It is counted from the request clock
// against that line's delivery on the shop calendar, never from now(): a later
// staff decision must not move a filed request into a different window.
type PolicyWindow string

// The five windows ReturnQueue and ReturnForDecision name. "within" keeps the
// existing column value so the queue's statutory label does not drift. "mixed"
// is a request that spans more than one line window.
const (
	WindowUndelivered PolicyWindow = "undelivered"
	WindowStatutory   PolicyWindow = "within"
	WindowGoodwill    PolicyWindow = "goodwill"
	WindowLate        PolicyWindow = "after"
	WindowMixed       PolicyWindow = "mixed"
)

var knownPolicyWindows = [...]PolicyWindow{
	WindowUndelivered,
	WindowStatutory,
	WindowGoodwill,
	WindowLate,
	WindowMixed,
}

// ParsePolicyWindow reads a window the decision queries named. Unknown values
// fail closed: inventing a window would let a decision claim an entitlement
// the clocks did not earn.
func ParsePolicyWindow(s string) (PolicyWindow, bool) {
	w := PolicyWindow(s)
	switch w {
	case WindowUndelivered, WindowStatutory, WindowGoodwill, WindowLate, WindowMixed:
		return w, true
	default:
		return "", false
	}
}

// Entitlement is what an approval may claim. It is a closed set.
type Entitlement string

// The three claims an approval can make. Goodwill is recorded only when every
// returned line in days 8–14 is unused and complete as observed facts.
const (
	EntitlementStatutory Entitlement = "statutory"
	EntitlementGoodwill  Entitlement = "goodwill"
	EntitlementException Entitlement = "exception"
)

// Fact is one observed eligibility predicate. Unknown is the honest default:
// an unobserved parcel is not "does not meet", and it is not "meets".
type Fact string

const (
	FactUnknown Fact = "unknown"
	FactMet     Fact = "met"
	FactUnmet   Fact = "unmet"
)

// ParseFact reads one radio. Typos fail closed so a crafted form cannot
// smuggle a fourth state past the CHECK.
func ParseFact(s string) (Fact, bool) {
	f := Fact(s)
	switch f {
	case FactUnknown, FactMet, FactUnmet:
		return f, true
	default:
		return "", false
	}
}

// DecisionKind is the staff verb on the decide form. Exception is a distinct
// approval: it pays, but it cannot read as a statutory or goodwill right.
type DecisionKind string

const (
	DecisionApprove   DecisionKind = "approved"
	DecisionReject    DecisionKind = "rejected"
	DecisionException DecisionKind = "exception"
)

// ParseDecisionKind reads the form's decision. Lifecycle states and typos
// are refused so a crafted POST cannot invent a fourth verb.
func ParseDecisionKind(s string) (DecisionKind, bool) {
	k := DecisionKind(s)
	switch k {
	case DecisionApprove, DecisionReject, DecisionException:
		return k, true
	default:
		return "", false
	}
}

// Status is the return_requests.status this kind writes.
func (k DecisionKind) Status() ReturnStatus {
	if k == DecisionReject {
		return ReturnRejected
	}
	return ReturnApproved
}

// LineAssessment is one returned line's frozen window and the three
// eligibility facts. Missing facts stay unknown; the caller must not invent
// met from a customer reason or a blank form.
type LineAssessment struct {
	OrderLineID string
	Window      PolicyWindow
	Unused      Fact
	Packaging   Fact
	Accessories Fact
}

func (l LineAssessment) anyUnknown() bool {
	return unknownOrEmpty(l.Unused) || unknownOrEmpty(l.Packaging) || unknownOrEmpty(l.Accessories)
}

func (l LineAssessment) anyUnmet() bool {
	return l.Unused == FactUnmet || l.Packaging == FactUnmet || l.Accessories == FactUnmet
}

func (l LineAssessment) allMet() bool {
	return l.Unused == FactMet && l.Packaging == FactMet && l.Accessories == FactMet
}

func unknownOrEmpty(f Fact) bool {
	return f == "" || f == FactUnknown
}

// RequestWindow is the request-level union of its lines. Mixed stays mixed
// so a later parcel on one line cannot reclassify another.
func RequestWindow(lines []LineAssessment) PolicyWindow {
	if len(lines) == 0 {
		return WindowUndelivered
	}
	w := lines[0].Window
	for _, l := range lines[1:] {
		if l.Window != w {
			return WindowMixed
		}
	}
	return w
}

// Claim is what a permitted decision may persist.
type Claim struct {
	Window      PolicyWindow
	Entitlement Entitlement
}

// RefusalKind names why Evaluate refused, so the form can mark a field
// without matching free-form prose.
type RefusalKind string

const (
	RefuseStatutoryReject RefusalKind = "statutory_reject"
	RefuseIncomplete      RefusalKind = "incomplete"
	RefuseNeedException   RefusalKind = "need_exception"
	RefuseUnmetApprove    RefusalKind = "unmet_approve"
	RefuseNoUnmet         RefusalKind = "no_unmet"
	RefuseUseApprove      RefusalKind = "use_approve"
	RefuseEmpty           RefusalKind = "empty"
	RefuseStale           RefusalKind = "stale"
	RefuseExceptionReason RefusalKind = "exception_reason"
	RefuseRejectionReason RefusalKind = "rejection_reason"
)

// RefusalError is a policy refusal with a stable kind.
type RefusalError struct {
	Kind RefusalKind
}

func (r *RefusalError) Error() string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", ErrPolicy.Error(), r.Kind)
}

func (r *RefusalError) Unwrap() error { return ErrPolicy }

func refuse(kind RefusalKind) error {
	return &RefusalError{Kind: kind}
}

type linePolicyState struct {
	hasStatutory, hasGoodwill, hasLate, hasUndelivered bool
	goodwillUnknown, goodwillUnmet, goodwillAllMet     bool
}

func normalizeAssessmentLines(lines []LineAssessment) error {
	for i := range lines {
		if _, ok := ParsePolicyWindow(string(lines[i].Window)); !ok || lines[i].Window == WindowMixed {
			return fmt.Errorf("%w: line window %q is not a known line window",
				ErrPolicy, lines[i].Window)
		}
		if lines[i].Unused == "" {
			lines[i].Unused = FactUnknown
		}
		if lines[i].Packaging == "" {
			lines[i].Packaging = FactUnknown
		}
		if lines[i].Accessories == "" {
			lines[i].Accessories = FactUnknown
		}
	}
	return nil
}

func linePolicyStateFrom(lines []LineAssessment) linePolicyState {
	var state linePolicyState
	state.goodwillAllMet = true
	goodwillCount := 0
	for _, l := range lines {
		switch l.Window {
		case WindowStatutory:
			state.hasStatutory = true
		case WindowGoodwill:
			state.hasGoodwill = true
			goodwillCount++
			if l.anyUnknown() {
				state.goodwillUnknown = true
			}
			if l.anyUnmet() {
				state.goodwillUnmet = true
			}
			if !l.allMet() {
				state.goodwillAllMet = false
			}
		case WindowLate:
			state.hasLate = true
		case WindowUndelivered:
			state.hasUndelivered = true
		case WindowMixed:
			// Request-level only; line windows are validated in normalizeAssessmentLines.
		}
	}
	if goodwillCount == 0 {
		state.goodwillAllMet = false
	}
	return state
}

// Evaluate is the advertised policy at the decision door. A retry has
// already been decided and must not call this: re-reading the clocks or
// the facts would rewrite a claim that already paid.
func Evaluate(lines []LineAssessment, kind DecisionKind) (Claim, error) {
	if len(lines) == 0 {
		return Claim{}, refuse(RefuseEmpty)
	}
	if err := normalizeAssessmentLines(lines); err != nil {
		return Claim{}, err
	}
	window := RequestWindow(lines)
	state := linePolicyStateFrom(lines)

	switch kind {
	case DecisionApprove:
		return evaluateApprove(window, state.hasStatutory, state.hasGoodwill, state.hasLate, state.hasUndelivered,
			state.goodwillUnknown, state.goodwillUnmet, state.goodwillAllMet)
	case DecisionReject:
		return evaluateReject(window, state.hasStatutory, state.hasGoodwill, state.hasLate, state.hasUndelivered,
			state.goodwillUnknown, state.goodwillUnmet)
	case DecisionException:
		return evaluateException(window, state.hasStatutory, state.hasGoodwill, state.hasLate, state.hasUndelivered,
			state.goodwillUnknown, state.goodwillUnmet, state.goodwillAllMet)
	default:
		return Claim{}, fmt.Errorf("%w: decision %q is not a known kind", ErrPolicy, kind)
	}
}

func evaluateApprove(
	window PolicyWindow,
	hasStatutory, hasGoodwill, hasLate, hasUndelivered bool,
	goodwillUnknown, goodwillUnmet, goodwillAllMet bool,
) (Claim, error) {
	if window == WindowStatutory {
		return Claim{Window: WindowStatutory, Entitlement: EntitlementStatutory}, nil
	}
	if window == WindowGoodwill {
		if goodwillUnknown {
			return Claim{}, refuse(RefuseIncomplete)
		}
		if goodwillUnmet || !goodwillAllMet {
			return Claim{}, refuse(RefuseUnmetApprove)
		}
		return Claim{Window: WindowGoodwill, Entitlement: EntitlementGoodwill}, nil
	}
	if window == WindowLate {
		return Claim{}, refuse(RefuseNeedException)
	}
	// Undelivered is not late. The existing decide path still pays; the
	// claim is an exception so the window is not read as a policy right.
	if window == WindowUndelivered {
		return Claim{Window: WindowUndelivered, Entitlement: EntitlementException}, nil
	}
	if hasStatutory && hasGoodwill && !hasLate && !hasUndelivered {
		if goodwillUnknown {
			return Claim{}, refuse(RefuseIncomplete)
		}
		if !goodwillAllMet {
			return Claim{}, refuse(RefuseNeedException)
		}
		return Claim{Window: WindowMixed, Entitlement: EntitlementGoodwill}, nil
	}
	return Claim{}, refuse(RefuseNeedException)
}

func evaluateReject(
	window PolicyWindow,
	hasStatutory, hasGoodwill, hasLate, hasUndelivered bool,
	goodwillUnknown, goodwillUnmet bool,
) (Claim, error) {
	// A request that still contains a statutory line cannot be closed against
	// that line: missing reason, unpacking, and the days 8–14 conditions are
	// not grounds Consumer Protection Act §19 I allows.
	if hasStatutory {
		return Claim{}, refuse(RefuseStatutoryReject)
	}
	if hasGoodwill {
		if goodwillUnknown {
			return Claim{}, refuse(RefuseIncomplete)
		}
		if !goodwillUnmet {
			return Claim{}, refuse(RefuseNoUnmet)
		}
		return Claim{Window: window}, nil
	}
	if hasLate || hasUndelivered {
		return Claim{Window: window}, nil
	}
	return Claim{}, refuse(RefuseNoUnmet)
}

func exceptionGround(goodwillUnmet, hasLate, hasUndelivered bool) bool {
	return goodwillUnmet || hasLate || hasUndelivered
}

func evaluateGoodwillException(goodwillUnmet, goodwillAllMet bool) (Claim, error) {
	if goodwillAllMet {
		return Claim{}, refuse(RefuseUseApprove)
	}
	if goodwillUnmet {
		return Claim{Window: WindowGoodwill, Entitlement: EntitlementException}, nil
	}
	return Claim{}, refuse(RefuseNeedException)
}

func evaluateException(
	window PolicyWindow,
	hasStatutory, hasGoodwill, hasLate, hasUndelivered bool,
	goodwillUnknown, goodwillUnmet, goodwillAllMet bool,
) (Claim, error) {
	if window == WindowStatutory {
		return Claim{}, refuse(RefuseUseApprove)
	}
	// A late or undelivered line cannot stand in for missing days 8–14
	// observations. Unmet may justify an exception only after every
	// required goodwill fact is assessed.
	if hasGoodwill && goodwillUnknown {
		return Claim{}, refuse(RefuseIncomplete)
	}
	if window == WindowGoodwill {
		return evaluateGoodwillException(goodwillUnmet, goodwillAllMet)
	}
	if window == WindowLate || window == WindowUndelivered {
		return Claim{Window: window, Entitlement: EntitlementException}, nil
	}
	ground := exceptionGround(goodwillUnmet, hasLate, hasUndelivered)
	if hasStatutory && ground {
		return Claim{Window: WindowMixed, Entitlement: EntitlementException}, nil
	}
	if ground {
		return Claim{Window: window, Entitlement: EntitlementException}, nil
	}
	return Claim{}, refuse(RefuseNeedException)
}
