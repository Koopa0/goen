package cart

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestPaymentReturnNeverOverridesTerminalOrFundedState(t *testing.T) {
	for _, view := range []pages.OrderView{
		{Number: "ORD-1", Status: order.FulfillmentCancelled, OwedCents: 100},
		{Number: "ORD-1", Status: order.FulfillmentPending, Committed: true, OwedCents: 100},
		{Number: "ORD-1", Status: order.FulfillmentPending, OwedCents: 0},
		{Number: "ORD-1", Status: order.FulfillmentShipped, OwedCents: 100},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/orders/ORD-1?paid=1", http.NoBody)
		if got := paymentReturnRefresh(r, &view); got != "" {
			t.Errorf("view %+v refresh=%q", view, got)
		}
	}
}

func TestPaymentReturnChecksKeepTheReturnMarkerWhenTheyEnd(t *testing.T) {
	t.Parallel()
	path := "/orders/ORD-1?paid=1"
	for _, want := range []string{
		"/orders/ORD-1?paid=1&confirmation=1",
		"/orders/ORD-1?paid=1&confirmation=2",
		"/orders/ORD-1?paid=1&confirmation=done",
		"",
	} {
		view := pages.OrderView{Number: "ORD-1", Status: order.FulfillmentPending, OwedCents: 100}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
		got := paymentReturnRefresh(r, &view)
		if got != want {
			t.Fatalf("paymentReturnRefresh(%q) = %q, want %q", path, got, want)
		}
		if want == "" && !view.PaymentConfirmationPending {
			t.Error("ended checks discarded the payment return hint")
		}
		path = got
	}
}

func TestPaymentReturnEndingNeverOverridesTerminalOrFundedState(t *testing.T) {
	t.Parallel()
	for _, view := range []pages.OrderView{
		{Number: "ORD-1", Status: order.FulfillmentCancelled, OwedCents: 100},
		{Number: "ORD-1", Status: order.FulfillmentPending, Committed: true, OwedCents: 100},
		{Number: "ORD-1", Status: order.FulfillmentPending, OwedCents: 0},
		{Number: "ORD-1", Status: order.FulfillmentShipped, OwedCents: 100},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/orders/ORD-1?paid=1&confirmation=done", http.NoBody)
		if got := paymentReturnRefresh(r, &view); got != "" || view.PaymentConfirmationPending {
			t.Errorf("funded or terminal view %+v: refresh=%q", view, got)
		}
	}
}
