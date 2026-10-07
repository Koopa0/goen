package cart

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestPaymentReturnCancellationIsIndependentOfTheCheckCounter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name    string
		query   string
		until   time.Time
		want    bool
		refresh string
	}{
		{name: "first return", query: "?paid=1", until: now.Add(time.Minute), refresh: "/orders/ORD-1?paid=1&confirmation=1"},
		{name: "checking", query: "?paid=1&confirmation=1", until: now.Add(time.Minute), refresh: "/orders/ORD-1?paid=1&confirmation=2"},
		{name: "ended", query: "?paid=1&confirmation=done", until: now.Add(time.Minute)},
		{name: "malformed counter", query: "?paid=1&confirmation=wrong", until: now.Add(time.Minute)},
		{name: "negative counter", query: "?paid=1&confirmation=-1", until: now.Add(time.Minute)},
		{name: "overflow counter", query: "?paid=1&confirmation=999999999999999999999", until: now.Add(time.Minute)},
		{name: "plain", until: now.Add(time.Minute), want: true},
		{name: "no return marker", query: "?confirmation=done", until: now.Add(time.Minute), want: true},
		{name: "invalid marker", query: "?paid=0", until: now.Add(time.Minute), want: true},
		{name: "noncanonical marker", query: "?paid=true", until: now.Add(time.Minute), want: true},
		{name: "expired while checking", query: "?paid=1", until: now.Add(-time.Nanosecond), refresh: "/orders/ORD-1?paid=1&confirmation=1"},
		{name: "no hold while checking", query: "?paid=1", refresh: "/orders/ORD-1?paid=1&confirmation=1"},
		{name: "expiry boundary while checking", query: "?paid=1", until: now, refresh: "/orders/ORD-1?paid=1&confirmation=1"},
		{name: "expired ended", query: "?paid=1&confirmation=done", until: now.Add(-time.Nanosecond), want: true},
		{name: "expiry boundary", query: "?paid=1&confirmation=done", until: now, want: true},
		{name: "no hold", query: "?paid=1&confirmation=done", want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			view := pages.OrderView{Number: "ORD-1", Status: order.FulfillmentPending, OwedCents: 100, Now: now, HoldUntil: tt.until}
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/orders/ORD-1"+tt.query, http.NoBody)
			view.PaymentRefreshURL = paymentReturnRefresh(r, &view)
			if got := view.PaymentRefreshURL; got != tt.refresh {
				t.Errorf("paymentReturnRefresh(%q) = %q, want %q", tt.query, got, tt.refresh)
			}
			if got := view.ShowCancel(); got != tt.want {
				t.Errorf("ShowCancel(%q) = %v, want %v", tt.query, got, tt.want)
			}
			if !view.CanCancel() || !view.AwaitingPayment() {
				t.Error("return hint changed the order's funding or cancellation eligibility")
			}
		})
	}
}
