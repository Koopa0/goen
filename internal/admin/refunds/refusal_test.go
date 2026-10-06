package refunds

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/admin/refundstate"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/pgerr"
)

func refusedBy(constraint string) error {
	cause := &pgconn.PgError{Code: "23514", ConstraintName: constraint, Message: "an order is not what a shop assistant can read"}
	return pgerr.WrapRefusal(fmt.Errorf("a statement: %w", cause), refundstate.ErrRefused)
}

func TestRefundNoticeNamesTheCauseOfTheRefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want string
		ok   bool
	}{
		{"?refunded=1", nil, "?refunded=1", true},
		{"unsettled", ErrUnsettled, "?refundpending=1", true},
		{"shipped", fmt.Errorf("order G1: %w", ErrShipped), "?refundshipped=1", true},
		{"has a return", fmt.Errorf("order G1: %w", ErrHasReturn), "?refundhasreturn=1", true},
		{"cancelled", fmt.Errorf("order G1: %w", ErrOrderCancelled), "?refundcancelled=1", true},
		{"unpaid", fmt.Errorf("order G1: %w", ErrNotPaid), "?refundunpaid=1", true},
		{"cancelled between preview and cancel", refusedBy("orders_history_frozen"), "?refundcancelled=1", true},
		{"packing started between preview and cancel", refusedBy("orders_paid_cancel_needs_refund"), "?refundpicking=1", true},
		{"changed between preview and open", refusedBy("return_before_shipment_eligible"), "?refundchanged=1", true},
		{"illegal transition", refusedBy("orders_legal_transition"), "?refundchanged=1", true},
		{"frozen split does not cover the return", refusedBy("refunds_sources_cover_return"), "?refundmismatch=1", true},
		{"credit attribution", refusedBy("refunds_credit_attribution"), "?refundmismatch=1", true},
		{"no card amount", refusedBy("refunds_card_amount_positive"), "?refundmismatch=1", true},
		{"no captured payment", refusedBy("refunds_return_captured"), "?refundmismatch=1", true},
		{"payout does not fit", fmt.Errorf("return R1: %w", ErrPayoutUnfit), "?refundmismatch=1", true},
		{"reason", fmt.Errorf("%w: a reason", ErrInvalid), "?refundreason=1", true},
		{"a refusal no sentence names", refusedBy("refunds_request_attribution"), "?refundunsure=1", true},
		{"an unknown error", errors.New("connection reset"), "", false},
	} {
		got, ok := refundNotice(tc.err)
		if got != tc.want || ok != tc.ok {
			t.Errorf("refundNotice(%s) = %q, %t; want %q, %t", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// A refusal retained as the cause of an approved refund must not read as
// "nothing moved": recovery keeps its own sentence.
func TestRefundNoticeKeepsRecoveryAheadOfARefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"cancellation incomplete after the order was cancelled", errors.Join(
			fmt.Errorf("%w: %w", ErrCancellationIncomplete, refusedBy("orders_history_frozen")), errors.New("correct the invoice")), "?cancelretry=1"},
		{"payout incomplete with a mismatch as its cause", fmt.Errorf("%w: %w", refundstate.ErrIncomplete, ErrPayoutUnfit), "?refundretry=1"},
		{"cancellation waits on the invoice", refusedBy("orders_cancel_invoice_resolved"), "?cancelinvoice=1"},
	} {
		if got, _ := refundNotice(tc.err); got != tc.want {
			t.Errorf("refundNotice(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestWhyNotRefundableNamesWhatTheOrderIs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		row  db.BeforeShipmentRefundRow
		want error
	}{
		{"cancelled", db.BeforeShipmentRefundRow{FulfillmentStatus: string(order.FulfillmentCancelled), HasReturn: true}, ErrOrderCancelled},
		{"shipped by status", db.BeforeShipmentRefundRow{FulfillmentStatus: string(order.FulfillmentShipped), Committed: true}, ErrShipped},
		{"a parcel left", db.BeforeShipmentRefundRow{FulfillmentStatus: string(order.FulfillmentPicking), Committed: true, Shipped: true}, ErrShipped},
		{"a customer return", db.BeforeShipmentRefundRow{FulfillmentStatus: string(order.FulfillmentPicking), Committed: true, HasReturn: true}, ErrHasReturn},
		{"unpaid", db.BeforeShipmentRefundRow{FulfillmentStatus: string(order.FulfillmentPending)}, ErrNotPaid},
	} {
		if got := whyNotRefundable(&tc.row); !errors.Is(got, tc.want) || !errors.Is(got, refundstate.ErrRefused) {
			t.Errorf("whyNotRefundable(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
