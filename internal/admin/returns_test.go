package admin

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/ui/pages"
)

func TestBlockedReturnDiagnosticRouting(t *testing.T) {
	t.Parallel()
	requestID := uuid.MustParse("018f0df6-57c0-7b31-9c13-b56a9e778f01")
	tests := []struct {
		name        string
		facts       returnPayoutFacts
		wantErr     bool
		wantFigures []string
	}{
		{
			name: "source mismatch keeps its figures",
			facts: returnPayoutFacts{
				ID:                  requestID,
				RefundableCents:     50,
				CapturedCents:       100,
				RefundedCents:       80,
				CreditSpentCents:    10,
				CreditReturnedCents: 0,
				HasAccount:          true,
			},
			wantErr: true,
			wantFigures: []string{
				"captured 100", "80 is already refunded", "20 remains",
				"10 of store credit was spent", "0 returned", "refunding 50",
			},
		},
		{
			name: "terminal provider state is not a source diagnostic",
			facts: returnPayoutFacts{
				ID:              requestID,
				RefundableCents: 50,
				CapturedCents:   100,
				HasAccount:      true,
				CardTerminal:    true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			item := pages.AdminReturn{}
			err := fillReturnPayoutState("approved", tt.facts, &item)
			if got := errors.Is(err, ErrRefused); got != tt.wantErr {
				t.Fatalf("fillReturnPayoutState() ErrRefused = %t, want %t; error = %v",
					got, tt.wantErr, err)
			}
			wantItem := pages.AdminReturn{PayoutOutstanding: true, PayoutBlocked: true}
			if diff := cmp.Diff(wantItem, item); diff != "" {
				t.Errorf("fillReturnPayoutState() item mismatch (-want +got):\n%s", diff)
			}
			for _, figure := range tt.wantFigures {
				if !strings.Contains(strings.ToLower(err.Error()), figure) {
					t.Errorf("fillReturnPayoutState() error = %q, want figure %q", err, figure)
				}
			}
		})
	}
}
