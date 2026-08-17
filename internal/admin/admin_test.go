package admin

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestAReceiptIsAlwaysPositive proves a receipt cannot take stock away, which is
// what keeps a delivery distinguishable from a correction.
func TestAReceiptIsAlwaysPositive(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  int32
		wantY bool
	}{
		{name: "an ordinary delivery", in: "12", want: 12, wantY: true},
		{name: "one unit", in: "1", want: 1, wantY: true},
		{name: "surrounding space is not a typo worth refusing", in: "  8 ", want: 8, wantY: true},
		{name: "the ceiling", in: "10000", want: 10000, wantY: true},
		{name: "a correction typed into the receipt box", in: "-3"},
		{name: "nothing arrived is not a delivery", in: "0"},
		{name: "a warehouse invented by a typo", in: "10001"},
		{name: "empty", in: ""},
		{name: "not a number", in: "十二"},
		{name: "a fraction of a unit", in: "1.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseReceipt(tt.in)
			if ok != tt.wantY || got != tt.want {
				t.Errorf("ParseReceipt(%q) = %d, %v, want %d, %v",
					tt.in, got, ok, tt.want, tt.wantY)
			}
		})
	}
}

// TestAFundedOrderIsNotBadgedUnpaid holds the two halves of 'pending' apart.
//
// An order stays pending from the moment money arrives until a human picks it,
// and one paid entirely from store credit has no payment row at all — so it sits
// there for good. Reading the status alone badged it 待付款 on the queue somebody
// works, beside the customer's own page saying 付款完成, and nothing would ever
// move it because no payment is coming.
//
// CLAUDE.md states the rule for exactly this caller: Committed alone means the
// shop has taken it on, owed == 0 alone means nothing is due, and either is
// enough to say it is not awaiting payment.
func TestAFundedOrderIsNotBadgedUnpaid(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	unpaid := i18n.T(ctx, i18n.KeyAdminStatusPending)
	ready := i18n.T(ctx, i18n.KeyAdminStatusReadyToPick)

	for _, tt := range []struct {
		name      string
		status    string
		committed bool
		owed      int64
		want      string
	}{
		{name: "nobody has paid", status: "pending", owed: 65000, want: unpaid},
		{name: "the card cleared", status: "pending", committed: true, owed: 65000, want: ready},
		{name: "store credit covered it", status: "pending", owed: 0, want: ready},
		// Every other status answers from itself: only pending is two states
		// wearing one name.
		{name: "picking", status: "picking", committed: true, want: i18n.T(ctx, i18n.KeyAdminStatusPicking)},
		{name: "cancelled and unpaid", status: "cancelled", owed: 65000, want: i18n.T(ctx, i18n.KeyAdminStatusCancelled)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := FundedStatusLabel(ctx, tt.status, tt.committed, tt.owed); got != tt.want {
				t.Errorf("FundedStatusLabel(%q, committed=%v, owed=%d) = %q, want %q",
					tt.status, tt.committed, tt.owed, got, tt.want)
			}
		})
	}
}
