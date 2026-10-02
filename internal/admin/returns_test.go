package admin

import (
	"errors"
	"testing"

	"github.com/koopa0/goen/internal/returns"
)

func TestAStatutoryRequestCannotBeRejectedOnTheDecisionPath(t *testing.T) {
	t.Parallel()

	_, err := returns.Evaluate([]returns.LineAssessment{{
		OrderLineID: "1", Window: returns.WindowStatutory,
	}}, returns.DecisionReject)
	if !errors.Is(err, returns.ErrPolicy) {
		t.Fatalf("statutory rejection = %v, want ErrPolicy", err)
	}
}

func TestLateApprovalCannotClaimPolicyEntitlement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		window returns.PolicyWindow
		kind   returns.DecisionKind
		want   returns.Entitlement
		refuse bool
	}{
		{returns.WindowGoodwill, returns.DecisionApprove, "", true},
		{returns.WindowLate, returns.DecisionApprove, "", true},
		{returns.WindowLate, returns.DecisionException, returns.EntitlementException, false},
		{returns.WindowUndelivered, returns.DecisionApprove, returns.EntitlementException, false},
		{returns.WindowStatutory, returns.DecisionApprove, returns.EntitlementStatutory, false},
	}
	for _, tc := range tests {
		got, err := returns.Evaluate([]returns.LineAssessment{{
			OrderLineID: "1", Window: tc.window,
		}}, tc.kind)
		if tc.refuse {
			if !errors.Is(err, returns.ErrPolicy) {
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
		kind       returns.DecisionKind
		resolution string
		wantRefuse bool
	}{
		{name: "approve stays optional", kind: returns.DecisionApprove},
		{name: "reject stays optional", kind: returns.DecisionReject, resolution: "   "},
		{name: "blank exception is refused", kind: returns.DecisionException, wantRefuse: true},
		{name: "whitespace exception is refused", kind: returns.DecisionException, resolution: " \t\n", wantRefuse: true},
		{name: "recorded exception is accepted", kind: returns.DecisionException, resolution: " beyond the advertised window "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := requireExceptionReason(tt.kind, tt.resolution)
			if tt.wantRefuse {
				refused, ok := errors.AsType[*FormRefusalError](err)
				if !ok || refused.Field != "resolution" ||
					refused.Kind != returns.RefuseExceptionReason {
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
