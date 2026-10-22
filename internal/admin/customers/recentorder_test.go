package customers

import (
	"testing"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/widget"
)

func TestRecentOrderRowCarriesTheColourOfItsWord(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		row  db.AdminCustomerOrdersRow
		want widget.Intent
	}{
		{"picking", db.AdminCustomerOrdersRow{FulfillmentStatus: string(order.FulfillmentPicking), Committed: true}, widget.IntentProgress},
		{"delivered", db.AdminCustomerOrdersRow{FulfillmentStatus: string(order.FulfillmentDelivered), Committed: true}, widget.IntentDone},
		{"waiting for payment", db.AdminCustomerOrdersRow{FulfillmentStatus: string(order.FulfillmentPending), OwedCents: 100}, widget.IntentNeutral},
	} {
		if got := recentOrderRow(t.Context(), &tt.row, false).StatusIntent; got != tt.want {
			t.Errorf("%s: intent %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestRecentOrderRowOfAReturnedOrderSaysRefunded(t *testing.T) {
	t.Parallel()
	row := db.AdminCustomerOrdersRow{FulfillmentStatus: string(order.FulfillmentDelivered), Committed: true}
	got := recentOrderRow(t.Context(), &row, true)
	if want := i18n.T(t.Context(), i18n.KeyStatusRefunded); got.StatusText != want || got.StatusIntent != widget.IntentNeutral {
		t.Errorf("status %q intent %q, want %q %q", got.StatusText, got.StatusIntent, want, widget.IntentNeutral)
	}
	if got.Status != order.FulfillmentDelivered {
		t.Errorf("stored fulfillment %s, want delivered", got.Status)
	}
}
