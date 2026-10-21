package checkout

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/shipping"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
)

// TestRefusedCouponLeavesTheQuoteOfThePageWithoutIt locks that a typed code the
// shop did not apply does not change the quote: the shopper who clears it must
// be submitting the page they were shown.
func TestRefusedCouponLeavesTheQuoteOfThePageWithoutIt(t *testing.T) {
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

func TestUnavailableCheckoutDoesNotRenderAnObsoletePickupDestination(t *testing.T) {
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
