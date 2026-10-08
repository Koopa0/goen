package customers

import (
	"testing"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/components"
)

func TestRecentOrderRowCarriesTheColourOfItsWord(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		row  db.AdminCustomerOrdersRow
		want components.Intent
	}{
		{"picking", db.AdminCustomerOrdersRow{FulfillmentStatus: string(order.FulfillmentPicking), Committed: true}, components.IntentProgress},
		{"delivered", db.AdminCustomerOrdersRow{FulfillmentStatus: string(order.FulfillmentDelivered), Committed: true}, components.IntentDone},
		{"waiting for payment", db.AdminCustomerOrdersRow{FulfillmentStatus: string(order.FulfillmentPending), OwedCents: 100}, components.IntentNeutral},
	} {
		if got := recentOrderRow(t.Context(), &tt.row).StatusIntent; got != tt.want {
			t.Errorf("%s: intent %q, want %q", tt.name, got, tt.want)
		}
	}
}
