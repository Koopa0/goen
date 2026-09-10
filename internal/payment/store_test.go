package payment

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestDurableCaptureRefusalSetIsExact(t *testing.T) {
	t.Parallel()
	want := []string{
		"payments_capture_matches_order",
		"payments_capture_refuses_released_stock",
		"payments_last4_format",
		"payments_no_regression",
		"payments_one_capture_per_order",
		"payments_require_complete_order",
	}
	if diff := cmp.Diff(want, durableCaptureRefusals[:]); diff != "" {
		t.Fatalf("durable capture refusals (-want +got):\n%s", diff)
	}
	for _, constraint := range []string{
		"", "payments_refuse_cancelled_order", "payments_captured_in_range",
		"some_future_constraint",
	} {
		if isDurableCaptureRefusal(constraint) {
			t.Errorf("isDurableCaptureRefusal(%q) = true, want false", constraint)
		}
	}
}

func TestPaidAttributionRequiresItsTransactionCapability(t *testing.T) {
	t.Parallel()
	attributed, err := AttributeCompleteCapture(t.Context(), nil, "cs_test")
	if attributed || !errors.Is(err, ErrNoTransaction) {
		t.Fatalf("AttributeCompleteCapture(nil) = %v, %v; want false, ErrNoTransaction",
			attributed, err)
	}
}
