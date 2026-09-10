package admin

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/ui/pages"
)

func TestReturnPayoutDiagnosticRouting(t *testing.T) {
	t.Parallel()
	requestID := uuid.MustParse("018f0df6-57c0-7b31-9c13-b56a9e778f01")
	tests := []struct {
		name        string
		facts       returnPayoutFacts
		wantErr     bool
		wantBlocked bool
		wantFigures []string
	}{
		{
			name: "frozen source mismatch keeps its figures",
			facts: returnPayoutFacts{
				ID: requestID, RefundableCents: 50,
				CardRefundCents: 20, CreditRefundCents: 10,
				HasAccount: true,
			},
			wantErr:     true,
			wantBlocked: true,
			wantFigures: []string{
				"card/credit 20/10", "50 refund",
			},
		},
		{
			name: "terminal provider state offers a successor retry",
			facts: returnPayoutFacts{
				ID: requestID, RefundableCents: 50, CardRefundCents: 50,
				HasAccount: true,
			},
		},
		{
			name: "posted credit and terminal card remain retryable after erasure",
			facts: returnPayoutFacts{
				ID: requestID, RefundableCents: 50,
				CardRefundCents: 40, CreditRefundCents: 10,
				CreditPaidCents: 10,
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
			wantItem := pages.AdminReturn{
				PayoutOutstanding: true,
				PayoutBlocked:     tt.wantBlocked,
			}
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

func TestReturnResolutionUsesTheDurableCharacterBound(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "empty is optional", want: true},
		{name: "three hundred multibyte characters", text: strings.Repeat("界", 300), want: true},
		{name: "three hundred and one characters", text: strings.Repeat("界", 301)},
		{name: "invalid UTF-8 is not PostgreSQL text", text: string([]byte{0xff})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := validReturnResolution(tt.text); got != tt.want {
				t.Errorf("validReturnResolution() = %v, want %v", got, tt.want)
			}
		})
	}
}
