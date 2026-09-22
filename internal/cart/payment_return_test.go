package cart

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/ui/pages"
)

func TestPaymentReturnNeverOverridesTerminalOrFundedState(t *testing.T) {
	for _, view := range []pages.OrderView{
		{Number: "ORD-1", Status: pages.FulfillmentCancelled, OwedCents: 100},
		{Number: "ORD-1", Status: pages.FulfillmentPending, Committed: true, OwedCents: 100},
		{Number: "ORD-1", Status: pages.FulfillmentPending, OwedCents: 0},
		{Number: "ORD-1", Status: pages.FulfillmentShipped, OwedCents: 100},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/orders/ORD-1?paid=1", http.NoBody)
		if got := paymentReturnRefresh(r, &view); got != "" {
			t.Errorf("view %+v refresh=%q", view, got)
		}
	}
}
