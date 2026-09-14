package returns

import (
	"errors"
	"testing"
)

func TestParsePolicyWindowIsAClosedSet(t *testing.T) {
	t.Parallel()

	for _, window := range knownPolicyWindows {
		got, ok := ParsePolicyWindow(string(window))
		if !ok || got != window {
			t.Errorf("ParsePolicyWindow(%q) = %q/%t, want %q/true", window, got, ok, window)
		}
	}
	for _, typo := range []string{"", "statutory", "trial", "within ", "AFTER"} {
		if _, ok := ParsePolicyWindow(typo); ok {
			t.Errorf("ParsePolicyWindow(%q) accepted an unknown window", typo)
		}
	}
}

func TestParseFactIsAClosedSet(t *testing.T) {
	t.Parallel()

	for _, fact := range []Fact{FactUnknown, FactMet, FactUnmet} {
		got, ok := ParseFact(string(fact))
		if !ok || got != fact {
			t.Errorf("ParseFact(%q) = %q/%t, want %q/true", fact, got, ok, fact)
		}
	}
	for _, typo := range []string{"", "true", "used", "met "} {
		if _, ok := ParseFact(typo); ok {
			t.Errorf("ParseFact(%q) accepted an unknown fact", typo)
		}
	}
}

func TestParseDecisionKindIsAClosedSet(t *testing.T) {
	t.Parallel()

	for _, kind := range []DecisionKind{DecisionApprove, DecisionReject, DecisionException} {
		got, ok := ParseDecisionKind(string(kind))
		if !ok || got != kind {
			t.Errorf("ParseDecisionKind(%q) = %q/%t, want %q/true", kind, got, ok, kind)
		}
	}
	for _, typo := range []string{"", "requested", "completed", "approved "} {
		if _, ok := ParseDecisionKind(typo); ok {
			t.Errorf("ParseDecisionKind(%q) accepted an unknown kind", typo)
		}
	}
}

func TestEvaluateEnforcesAdvertisedWindows(t *testing.T) {
	t.Parallel()

	statutory := LineAssessment{OrderLineID: "s", Window: WindowStatutory}
	goodwillUnknown := LineAssessment{OrderLineID: "g", Window: WindowGoodwill}
	goodwillMet := LineAssessment{
		OrderLineID: "g", Window: WindowGoodwill,
		Unused: FactMet, Packaging: FactMet, Accessories: FactMet,
	}
	goodwillUnmet := LineAssessment{
		OrderLineID: "g", Window: WindowGoodwill,
		Unused: FactUnmet, Packaging: FactMet, Accessories: FactMet,
	}
	late := LineAssessment{OrderLineID: "l", Window: WindowLate}
	undelivered := LineAssessment{OrderLineID: "u", Window: WindowUndelivered}

	tests := []struct {
		name        string
		lines       []LineAssessment
		kind        DecisionKind
		wantWindow  PolicyWindow
		wantEnt     Entitlement
		wantRefuse  RefusalKind
	}{
		{
			name:       "statutory approve is a right",
			lines:      []LineAssessment{statutory},
			kind:       DecisionApprove,
			wantWindow: WindowStatutory,
			wantEnt:    EntitlementStatutory,
		},
		{
			name:       "statutory reject is refused",
			lines:      []LineAssessment{statutory},
			kind:       DecisionReject,
			wantRefuse: RefuseStatutoryReject,
		},
		{
			name:       "statutory exception is the wrong verb",
			lines:      []LineAssessment{statutory},
			kind:       DecisionException,
			wantRefuse: RefuseUseApprove,
		},
		{
			name:       "goodwill unknown cannot approve",
			lines:      []LineAssessment{goodwillUnknown},
			kind:       DecisionApprove,
			wantRefuse: RefuseIncomplete,
		},
		{
			name:       "goodwill unknown cannot reject",
			lines:      []LineAssessment{goodwillUnknown},
			kind:       DecisionReject,
			wantRefuse: RefuseIncomplete,
		},
		{
			name:       "goodwill unknown cannot become an exception",
			lines:      []LineAssessment{goodwillUnknown},
			kind:       DecisionException,
			wantRefuse: RefuseIncomplete,
		},
		{
			name:       "goodwill all met approves as goodwill",
			lines:      []LineAssessment{goodwillMet},
			kind:       DecisionApprove,
			wantWindow: WindowGoodwill,
			wantEnt:    EntitlementGoodwill,
		},
		{
			name:       "goodwill all met cannot reject",
			lines:      []LineAssessment{goodwillMet},
			kind:       DecisionReject,
			wantRefuse: RefuseNoUnmet,
		},
		{
			name:       "goodwill all met cannot be labelled exception",
			lines:      []LineAssessment{goodwillMet},
			kind:       DecisionException,
			wantRefuse: RefuseUseApprove,
		},
		{
			name:       "goodwill unmet cannot claim goodwill",
			lines:      []LineAssessment{goodwillUnmet},
			kind:       DecisionApprove,
			wantRefuse: RefuseUnmetApprove,
		},
		{
			name:       "goodwill unmet may be rejected",
			lines:      []LineAssessment{goodwillUnmet},
			kind:       DecisionReject,
			wantWindow: WindowGoodwill,
		},
		{
			name:       "goodwill unmet may be an exception",
			lines:      []LineAssessment{goodwillUnmet},
			kind:       DecisionException,
			wantWindow: WindowGoodwill,
			wantEnt:    EntitlementException,
		},
		{
			name:       "late approve cannot claim a policy right",
			lines:      []LineAssessment{late},
			kind:       DecisionApprove,
			wantRefuse: RefuseNeedException,
		},
		{
			name:       "late exception pays as exception",
			lines:      []LineAssessment{late},
			kind:       DecisionException,
			wantWindow: WindowLate,
			wantEnt:    EntitlementException,
		},
		{
			name:       "late may be rejected",
			lines:      []LineAssessment{late},
			kind:       DecisionReject,
			wantWindow: WindowLate,
		},
		{
			name:       "undelivered approve is an exception",
			lines:      []LineAssessment{undelivered},
			kind:       DecisionApprove,
			wantWindow: WindowUndelivered,
			wantEnt:    EntitlementException,
		},
		{
			name:       "undelivered may be rejected",
			lines:      []LineAssessment{undelivered},
			kind:       DecisionReject,
			wantWindow: WindowUndelivered,
		},
		{
			name:       "mixed statutory and unknown goodwill stays open",
			lines:      []LineAssessment{statutory, goodwillUnknown},
			kind:       DecisionApprove,
			wantRefuse: RefuseIncomplete,
		},
		{
			name:       "mixed statutory cannot be rejected",
			lines:      []LineAssessment{statutory, goodwillUnmet},
			kind:       DecisionReject,
			wantRefuse: RefuseStatutoryReject,
		},
		{
			name:       "mixed statutory and unmet goodwill is an exception",
			lines:      []LineAssessment{statutory, goodwillUnmet},
			kind:       DecisionException,
			wantWindow: WindowMixed,
			wantEnt:    EntitlementException,
		},
		{
			name:       "mixed statutory and met goodwill is policy goodwill",
			lines:      []LineAssessment{statutory, goodwillMet},
			kind:       DecisionApprove,
			wantWindow: WindowMixed,
			wantEnt:    EntitlementGoodwill,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Evaluate(tt.lines, tt.kind)
			if tt.wantRefuse != "" {
				var refused *Refusal
				if !errors.As(err, &refused) || refused.Kind != tt.wantRefuse {
					t.Fatalf("Evaluate() = %+v / %v, want refusal %q", got, err, tt.wantRefuse)
				}
				return
			}
			if err != nil {
				t.Fatalf("Evaluate() = %v", err)
			}
			if got.Window != tt.wantWindow || got.Entitlement != tt.wantEnt {
				t.Errorf("claim = %q/%q, want %q/%q",
					got.Window, got.Entitlement, tt.wantWindow, tt.wantEnt)
			}
		})
	}
}

func TestRequestWindowKeepsMixedLinesMixed(t *testing.T) {
	t.Parallel()

	if got := RequestWindow([]LineAssessment{
		{Window: WindowStatutory},
		{Window: WindowGoodwill},
	}); got != WindowMixed {
		t.Errorf("RequestWindow(statutory, goodwill) = %q, want mixed", got)
	}
	if got := RequestWindow([]LineAssessment{
		{Window: WindowStatutory},
		{Window: WindowStatutory},
	}); got != WindowStatutory {
		t.Errorf("RequestWindow(two statutory) = %q, want within", got)
	}
}
