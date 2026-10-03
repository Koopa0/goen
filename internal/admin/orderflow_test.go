package admin

import (
	"testing"

	"github.com/koopa0/goen/internal/ui/pages"
)

func TestOnlyAnOrderStillGoingSomewhereCanHaveItsDeliveryCorrected(t *testing.T) {
	t.Parallel()
	for status, want := range map[pages.FulfillmentStatus]bool{
		pages.FulfillmentPending:   true,
		pages.FulfillmentPicking:   true,
		pages.FulfillmentShipped:   false,
		pages.FulfillmentDelivered: false,
		pages.FulfillmentCompleted: false,
		pages.FulfillmentCancelled: false,
	} {
		if got := correctable(status); got != want {
			t.Errorf("correctable(%s) = %t, want %t", status, got, want)
		}
	}
}
