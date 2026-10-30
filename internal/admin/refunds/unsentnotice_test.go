package refunds

import (
	"errors"
	"fmt"
	"testing"

	"github.com/koopa0/goen/internal/payment/refund"
)

// TestUnsentNotice: unsentNotice keeps a refund whose money went back apart
// from one whose payout did not complete.
func TestUnsentNotice(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want string
		ok   bool
	}{
		{"refunded", nil, "?unsentrefunded=1", true},
		{"payout incomplete", fmt.Errorf("a source: %w", refund.ErrIncomplete), "?refundretry=1", true},
		{"money back and a closing step failed", fmt.Errorf("a step: %w", refund.ErrFollowUpIncomplete), "?refundfollowup=1", true},
		{"closing step failed with a refusal as its cause", fmt.Errorf("%w: %w", refund.ErrFollowUpIncomplete, refusedBy("refunds_return_captured")), "?refundfollowup=1", true},
		{"unsettled", ErrUnsettled, "?refundpending=1", true},
		{"an unknown error", errors.New("connection reset"), "", false},
	} {
		outcome, ok := unsentNotice(tc.err)
		if outcome.Query != tc.want || ok != tc.ok {
			t.Errorf("unsentNotice(%s) = %q, %t; want %q, %t", tc.name, outcome.Query, ok, tc.want, tc.ok)
		}
	}
}
