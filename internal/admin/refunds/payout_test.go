package refunds

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/order/returns"
	"github.com/koopa0/goen/internal/payment/refund"
)

// TestReturnPayoutSplitEvent: a split refunded event waits until every source
// has settled.
func TestReturnPayoutSplitEvent(t *testing.T) {
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
			p, payoutErr := returnPayout(returns.StatusApproved, tt.facts)
			if payoutErr != nil || p.Unfit != nil {
				t.Fatalf("returnPayout() = %+v, %v", p, payoutErr)
			}
			if p.Owed != tt.wantOutstanding {
				t.Errorf("Owed = %t, want %t", p.Owed, tt.wantOutstanding)
			}
			if p.MoneySettled != tt.wantSettled {
				t.Errorf("Payout.MoneySettled = %t, want %t", p.MoneySettled, tt.wantSettled)
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
			p, err := returnPayout(returns.StatusApproved, tt.facts)
			if err != nil {
				t.Fatalf("returnPayout() error = %v, want an unfit payout reported in the result", err)
			}
			if got := errors.Is(p.Unfit, refund.ErrRefused); got != tt.wantErr {
				t.Fatalf("returnPayout() Unfit is refund.ErrRefused = %t, want %t; Unfit = %v",
					got, tt.wantErr, p.Unfit)
			}
			// A payout that does not fit its record is the mismatch notice, not
			// the generic refusal: the cause has to stay ErrPayoutUnfit.
			if got := errors.Is(p.Unfit, ErrPayoutUnfit); got != tt.wantErr {
				t.Fatalf("returnPayout() Unfit is ErrPayoutUnfit = %t, want %t; Unfit = %v",
					got, tt.wantErr, p.Unfit)
			}
			if !p.Owed || (p.Unfit != nil) != tt.wantBlocked {
				t.Errorf("returnPayout() = Owed %t, Unfit %v; want Owed, blocked %t", p.Owed, p.Unfit, tt.wantBlocked)
			}
			for _, figure := range tt.wantFigures {
				if !strings.Contains(strings.ToLower(p.Unfit.Error()), figure) {
					t.Errorf("returnPayout() Unfit = %q, want figure %q", p.Unfit, figure)
				}
			}
		})
	}
}

var errBrokenDB = errors.New("connection reset")

// brokenDB is a database on which every statement fails.
type brokenDB struct{}

func (brokenDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errBrokenDB
}

func (brokenDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errBrokenDB
}

func (brokenDB) QueryRow(context.Context, string, ...any) pgx.Row { return brokenRow{} }

type brokenRow struct{}

func (brokenRow) Scan(...any) error { return errBrokenDB }

// TestStore_PayOutstandingFollowUp: a step that fails once every source has
// settled is a closing step of a refund that went through; a source that has not
// settled stays a payout that did not complete.
func TestStore_PayOutstandingFollowUp(t *testing.T) {
	t.Parallel()
	s := &Store{q: db.New(brokenDB{})}
	row := &db.ReturnForDecisionRow{ID: uuid.New(), RefundableCents: 50000}
	for _, tt := range []struct {
		name         string
		position     returnPayoutPosition
		wantFollowUp bool
	}{
		{"refunded event missing", returnPayoutPosition{MoneySettled: true, EventOutstanding: true}, true},
		{"points clawback missing", returnPayoutPosition{MoneySettled: true, PointsOutstanding: true}, true},
		{"credit half not posted", returnPayoutPosition{Outstanding: refundSplit{Credit: 50000}}, false},
		{"card half not refunded", returnPayoutPosition{Outstanding: refundSplit{Card: 50000}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			worked, err := s.payOutstanding(t.Context(), row, tt.position, uuid.NullUUID{})
			if !worked || !errors.Is(err, refund.ErrIncomplete) {
				t.Fatalf("payOutstanding() = %t, %v, want work done and refund.ErrIncomplete", worked, err)
			}
			if got := errors.Is(err, refund.ErrFollowUpIncomplete); got != tt.wantFollowUp {
				t.Errorf("payOutstanding() error is ErrFollowUpIncomplete = %t, want %t: %v", got, tt.wantFollowUp, err)
			}
		})
	}
}
