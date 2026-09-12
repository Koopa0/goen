package admin

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestASplitRefundedEventWaitsUntilEverySourceSettled(t *testing.T) {
	t.Parallel()
	requestID := uuid.MustParse("018f0df6-57c0-7b31-9c13-b56a9e778f02")
	tests := []struct {
		name            string
		facts           returnPayoutFacts
		wantEvent       bool
		wantSettled     bool
		wantOutstanding bool
	}{
		{
			name: "credit posted while the card is still pending",
			facts: returnPayoutFacts{
				ID: requestID, RefundableCents: 200000,
				CardRefundCents: 140000, CreditRefundCents: 60000,
				CreditPaidCents: 60000, HasAccount: true,
			},
			wantOutstanding: true,
		},
		{
			name: "both sources settled and the timeline row is missing",
			facts: returnPayoutFacts{
				ID: requestID, RefundableCents: 200000,
				CardRefundCents: 140000, CreditRefundCents: 60000,
				CardPaidCents: 140000, CreditPaidCents: 60000, HasAccount: true,
			},
			wantEvent:       true,
			wantSettled:     true,
			wantOutstanding: true,
		},
		{
			name: "credit-only refund settled without a timeline row",
			facts: returnPayoutFacts{
				ID: requestID, RefundableCents: 200000,
				CreditRefundCents: 200000, CreditPaidCents: 200000, HasAccount: true,
			},
			wantEvent:       true,
			wantSettled:     true,
			wantOutstanding: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			position, err := tt.facts.position()
			if err != nil {
				t.Fatalf("position() = %v", err)
			}
			if position.EventOutstanding != tt.wantEvent {
				t.Errorf("EventOutstanding = %t, want %t", position.EventOutstanding, tt.wantEvent)
			}
			if position.MoneySettled != tt.wantSettled {
				t.Errorf("MoneySettled = %t, want %t", position.MoneySettled, tt.wantSettled)
			}
			item := pages.AdminReturn{}
			if fillErr := fillReturnPayoutState(returns.ReturnApproved, tt.facts, &item); fillErr != nil {
				t.Fatalf("fillReturnPayoutState() = %v", fillErr)
			}
			if item.PayoutOutstanding != tt.wantOutstanding {
				t.Errorf("PayoutOutstanding = %t, want %t", item.PayoutOutstanding, tt.wantOutstanding)
			}
		})
	}
}

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
			err := fillReturnPayoutState(returns.ReturnApproved, tt.facts, &item)
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

func TestAStatutoryBlankReasonCannotBeTheSoleRejection(t *testing.T) {
	t.Parallel()

	_, _, err := returnDecisionClaim("within", returns.ReturnRejected, "", "")
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("blank statutory rejection = %v, want ErrRefused", err)
	}

	window, entitlement, err := returnDecisionClaim(
		"within", returns.ReturnRejected, "", string(returns.RejectionGroundIneligible))
	if err != nil {
		t.Fatalf("statutory rejection with another ground = %v", err)
	}
	if window != returns.WindowStatutory || entitlement != "" {
		t.Errorf("claim = %q/%q, want statutory window and no approval entitlement",
			window, entitlement)
	}
}

func TestLateApprovalCannotClaimPolicyEntitlement(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		window string
		want   returns.Entitlement
	}{
		{"goodwill", ""},
		{"after", returns.EntitlementException},
		{"undelivered", returns.EntitlementException},
	} {
		gotWindow, entitlement, err := returnDecisionClaim(
			tc.window, returns.ReturnApproved, "", "")
		if err != nil {
			t.Fatalf("approve %s = %v", tc.window, err)
		}
		if gotWindow != returns.PolicyWindow(tc.window) {
			t.Errorf("window = %q, want %q", gotWindow, tc.window)
		}
		if entitlement != tc.want {
			t.Errorf("approve %s entitlement = %q, want %q", tc.window, entitlement, tc.want)
		}
	}

	window, entitlement, err := returnDecisionClaim(
		"within", returns.ReturnApproved, "", "")
	if err != nil {
		t.Fatalf("approve statutory = %v", err)
	}
	if window != returns.WindowStatutory || entitlement != returns.EntitlementStatutory {
		t.Errorf("statutory approval = %q/%q, want within/statutory", window, entitlement)
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
