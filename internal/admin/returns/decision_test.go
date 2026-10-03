package returns

import (
	"errors"
	"testing"

	returnrules "github.com/koopa0/goen/internal/returns"
)

func TestAStatutoryRequestCannotBeRejectedOnTheDecisionPath(t *testing.T) {
	t.Parallel()

	_, err := returnrules.Evaluate([]returnrules.LineAssessment{{
		OrderLineID: "1", Window: returnrules.WindowStatutory,
	}}, returnrules.DecisionReject)
	if !errors.Is(err, returnrules.ErrPolicy) {
		t.Fatalf("statutory rejection = %v, want ErrPolicy", err)
	}
}

func TestLateApprovalCannotClaimPolicyEntitlement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		window returnrules.PolicyWindow
		kind   returnrules.DecisionKind
		want   returnrules.Entitlement
		refuse bool
	}{
		{returnrules.WindowGoodwill, returnrules.DecisionApprove, "", true},
		{returnrules.WindowLate, returnrules.DecisionApprove, "", true},
		{returnrules.WindowLate, returnrules.DecisionException, returnrules.EntitlementException, false},
		{returnrules.WindowUndelivered, returnrules.DecisionApprove, returnrules.EntitlementException, false},
		{returnrules.WindowStatutory, returnrules.DecisionApprove, returnrules.EntitlementStatutory, false},
	}
	for _, tc := range tests {
		got, err := returnrules.Evaluate([]returnrules.LineAssessment{{
			OrderLineID: "1", Window: tc.window,
		}}, tc.kind)
		if tc.refuse {
			if !errors.Is(err, returnrules.ErrPolicy) {
				t.Fatalf("Evaluate(%s, %s) = %v, want ErrPolicy", tc.window, tc.kind, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("Evaluate(%s, %s) = %v", tc.window, tc.kind, err)
		}
		if got.Entitlement != tc.want {
			t.Errorf("Evaluate(%s, %s) entitlement = %q, want %q",
				tc.window, tc.kind, got.Entitlement, tc.want)
		}
	}
}

func TestFirstExceptionRequiresARecordedReason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		kind       returnrules.DecisionKind
		resolution string
		wantRefuse bool
	}{
		{name: "approve stays optional", kind: returnrules.DecisionApprove},
		{name: "reject stays optional", kind: returnrules.DecisionReject, resolution: "   "},
		{name: "blank exception is refused", kind: returnrules.DecisionException, wantRefuse: true},
		{name: "whitespace exception is refused", kind: returnrules.DecisionException, resolution: " \t\n", wantRefuse: true},
		{name: "recorded exception is accepted", kind: returnrules.DecisionException, resolution: " beyond the advertised window "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := requireExceptionReason(tt.kind, tt.resolution)
			if tt.wantRefuse {
				refused, ok := errors.AsType[*FormRefusalError](err)
				if !ok || refused.Field != "resolution" ||
					refused.Kind != returnrules.RefuseExceptionReason {
					t.Fatalf("requireExceptionReason() = %v, want resolution/exception_reason", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("requireExceptionReason() = %v, want nil", err)
			}
		})
	}
}
