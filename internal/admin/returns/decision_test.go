package returns

import (
	"errors"
	"testing"

	"github.com/koopa0/goen/internal/order/returns"
)

// TestRequireExceptionReason: the first exception requires a recorded reason.
func TestRequireExceptionReason(t *testing.T) {
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
