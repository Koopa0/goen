package checkout

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/shipping"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
)

// TestCheckoutQuoteIDForViewRefusedCoupon: checkoutQuoteIDForView leaves the
// quote of the page unchanged by a typed code the shop did not apply, while an
// applied coupon changes it. It locks that a typed code the shop did not apply
// does not change the quote: the shopper who clears it must be submitting the
// page they were shown.
func TestCheckoutQuoteIDForViewRefusedCoupon(t *testing.T) {
	t.Parallel()
	cartID := uuid.MustParse("018f0000-0000-7000-8000-000000000001")
	view := func() *View {
		return &View{
			Chosen: "018f0000-0000-7000-8000-000000000021",
			Cart: cart.View{
				SubtotalCents: 12000,
				Lines: []cart.Line{{
					VariantID: "018f0000-0000-7000-8000-000000000011",
					Quantity:  1, UnitCents: 12000,
				}},
			},
		}
	}
	quote := func(v *View) checkoutQuoteID {
		t.Helper()
		id, err := checkoutQuoteIDForView(cartID, v)
		if err != nil {
			t.Fatalf("quote: %v", err)
		}
		return id
	}

	plain := quote(view())
	refused := view()
	refused.CouponCode = "NOPE"
	if got := quote(refused); got != plain {
		t.Errorf("refused code changed the quote: got %s, want %s", got, plain)
	}

	applied := view()
	applied.CouponCode = "SAVE100"
	applied.CouponApplied = "save"
	applied.CouponDiscountCents = 10000
	if got := quote(applied); got == plain {
		t.Error("applied coupon left the quote unchanged")
	}
}

// TestHandler_RenderCheckoutUnavailable: renderCheckout of a view whose chosen
// method has been withdrawn renders no pickup destination and none of the
// obsolete quote, falls back to the address fields and answers 200.
func TestHandler_RenderCheckoutUnavailable(t *testing.T) {
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	view := &View{Chosen: "withdrawn-method", QuoteID: "obsolete-quote", Destination: shipping.DestinationPickupPoint}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/checkout", http.NoBody)
	res := httptest.NewRecorder()
	h.renderCheckout(res, req, http.StatusOK, view)
	type renderedDelivery struct {
		Status                        int
		Postal, Pickup, ObsoleteQuote bool
	}
	body := res.Body.String()
	got := renderedDelivery{Status: res.Code, Postal: strings.Contains(body, `name="postal_code"`),
		Pickup: strings.Contains(body, `name="pickup_store_code"`), ObsoleteQuote: strings.Contains(body, "obsolete-quote")}
	want := renderedDelivery{Status: http.StatusOK, Postal: true}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("unavailable checkout delivery (-want +got):\n%s", diff)
	}
}

// TestHandler_TakesPayment: The checkout takes payment only where Stripe is
// configured; a missing or keyless gateway is a shop that sells without paying.
func TestHandler_TakesPayment(t *testing.T) {
	keyless, err := payment.NewGateway("", "", "https://goen.example")
	if err != nil {
		t.Fatalf("NewGateway() without a key: %v", err)
	}
	keyed, err := payment.NewGateway("sk_test_notarealkey", "whsec_notused", "https://goen.example")
	if err != nil {
		t.Fatalf("NewGateway() with a key: %v", err)
	}

	for name, tt := range map[string]struct {
		gateway *payment.Gateway
		want    bool
	}{
		"no gateway":      {nil, false},
		"gateway no key":  {keyless, false},
		"gateway has key": {keyed, true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := (&Handler{payments: tt.gateway}).TakesPayment(); got != tt.want {
				t.Errorf("TakesPayment() = %v, want %v", got, tt.want)
			}
		})
	}
}
