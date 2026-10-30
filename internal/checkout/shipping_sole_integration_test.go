//go:build integration

package checkout_test

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestHandler_PlaceOrderSoleDelivery: PlaceOrder answers a refusal with the only
// delivery method on offer already chosen, whether the form named no method or one
// the shop no longer offers (its rates changed since the page loaded), and does not
// ask the shopper to choose among one. With two methods on offer it still asks.
// Pickup is offered only where the store map is configured, so a shop without one
// has a single method.
func TestHandler_PlaceOrderSoleDelivery(t *testing.T) {
	s := checkout.NewStore(pool)
	cartID, token := checkouttest.NewCartSession(t, s)
	if err := s.Carts().Add(t.Context(), cartID, ordertest.FreshVariant(t, pool, "sole-delivery"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	log := slog.New(slog.DiscardHandler)

	// The phone is left blank so that every answer below is a refusal, whatever it does about delivery.
	refused := func(t *testing.T, h *checkout.Handler, shipping *string) (page, body string) {
		t.Helper()
		page, status := openTheCheckout(t, h, token, "")
		if status != http.StatusOK {
			t.Fatalf("GET /checkout answered %d, want 200", status)
		}
		quote, _ := hiddenInputValue(page, "checkout_quote")
		attempt, _ := hiddenInputValue(page, "idempotency")
		form := url.Values{
			"email": {"sole@example.com"}, "name": {"王小明"}, "phone": {""},
			"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"}, "street": {"松高路 1 號"},
			"checkout_quote": {quote}, "idempotency": {attempt},
		}
		if shipping != nil {
			form.Set("shipping", *shipping)
		}
		res := checkoutPost(t, h, token, form)
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("POST /checkout answered %d, want 422; body=%s", res.Code, res.Body.String())
		}
		return page, res.Body.String()
	}

	t.Run("one", func(t *testing.T) {
		h := newHandler(s, access.NewStore(pool, false), log, nil, nil)
		retired := uuid.NewString()
		for name, shipping := range map[string]*string{"omitted": nil, "retired": &retired} {
			t.Run(name, func(t *testing.T) {
				page, body := refused(t, h, shipping)
				sole := checkedShipping.FindStringSubmatch(page)
				if sole == nil || strings.Count(page, `name="shipping"`) != 1 {
					t.Fatalf("GET /checkout offered %d methods and checked %v, want one, chosen",
						strings.Count(page, `name="shipping"`), sole)
				}
				if got := checkedShipping.FindStringSubmatch(body); len(got) != 2 || got[1] != sole[1] {
					t.Errorf("the refusal checks %v, want the only method %s", got, sole[1])
				}
				if strings.Contains(body, `id="shipping-error"`) {
					t.Error("the refusal asks the shopper to choose a delivery method among one")
				}
			})
		}
	})

	t.Run("two", func(t *testing.T) {
		h := newHandler(s, access.NewStore(pool, false), log, configuredMap(t), nil)
		page, body := refused(t, h, nil)
		if n := strings.Count(page, `name="shipping"`); n != 2 {
			t.Fatalf("GET /checkout offered %d methods, want two", n)
		}
		if got := checkedShipping.FindStringSubmatch(body); got != nil {
			t.Errorf("the refusal checks %q among two methods, want the shopper to choose", got[1])
		}
		if !strings.Contains(body, `id="shipping-error"`) {
			t.Error("the refusal does not ask the shopper to choose a delivery method")
		}
	})
}
