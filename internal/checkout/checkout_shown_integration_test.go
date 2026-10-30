//go:build integration

package checkout_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
)

var shownNames = []string{"shown_subtotal", "shown_shipping", "shown_discount", "shown_credit", "shown_total"}

// shownCart is a cart of one NT$1,500 line, below the seeded 滿 NT$3,000 免運.
func shownCart(t *testing.T, s *checkout.Store, label string) (token string) {
	t.Helper()
	token = bearer.New()
	cartID, err := s.Carts().Create(t.Context(), token, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := s.Carts().Add(t.Context(), cartID, pricedVariant(t, label, 150000), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	return token
}

func shownForm(t *testing.T, postalCode, key string) url.Values {
	t.Helper()
	return url.Values{
		"email": {"shown@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
		"postal_code": {postalCode}, "city": {"台北市"}, "district": {"信義區"},
		"street":      {"松高路 1 號"},
		"shipping":    {shipVersionFor(t, "home_delivery").String()},
		"idempotency": {checkouttest.AttemptKey(key)},
	}
}

// withShown copies a page's quote and the figures it posted back into form.
func withShown(t *testing.T, body string, form url.Values) {
	t.Helper()
	quote, ok := hiddenInputValue(body, "checkout_quote")
	if !ok {
		t.Fatal("the page carries no checkout quote")
	}
	form.Set("checkout_quote", quote)
	for _, name := range shownNames {
		v, ok := hiddenInputValue(body, name)
		if !ok {
			t.Fatalf("the page carries no %s", name)
		}
		form.Set(name, v)
	}
}

// TestHandler_PlaceOrderRepricedRows: PlaceOrder refuses a quote that moved
// with the rows that moved, each with the figure the page showed and the figure
// now, and accepts the page it answers with. It is the shopper who typed a
// mainland postcode, saw the page, and changed it to an island's before placing.
func TestHandler_PlaceOrderRepricedRows(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	h := couponTestHandler(s)
	token := shownCart(t, s, "repriced-rows")
	key := "repriced-rows-" + uuid.NewString()

	form := shownForm(t, "110", key)
	form.Set("update", "shipping")
	page := checkoutPost(t, h, token, form)
	if page.Code != http.StatusOK {
		t.Fatalf("the first page answered %d, want 200", page.Code)
	}
	form.Del("update")
	withShown(t, page.Body.String(), form)
	wasCents, err := strconv.ParseInt(form.Get("shown_shipping"), 10, 64)
	if err != nil {
		t.Fatalf("the page posted back no delivery figure: %v", err)
	}

	form.Set("postal_code", "890")
	refused := checkoutPost(t, h, token, form)
	if refused.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an address in a surcharge zone answered %d, want 422; body=%s", refused.Code, refused.Body.String())
	}
	body := refused.Body.String()
	quoteNow, quoteErr := s.QuoteShipping(ctx, shipVersionFor(t, "home_delivery"), 150000, "890")
	if quoteErr != nil {
		t.Fatalf("quote the island address: %v", quoteErr)
	}
	if quoteNow.Surcharge == 0 {
		t.Fatal("the fixture priced no surcharge, so this test proved nothing")
	}
	nowCents := checkouttest.QuoteTotal(t, quoteNow)
	wantRow := "<li>" + i18n.T(ctx, i18n.KeyShippingFee) + "：" + money.TWD(wasCents) + " → " + money.TWD(nowCents) + "</li>"
	if !strings.Contains(body, wantRow) {
		t.Errorf("the refusal does not say %s", wantRow)
	}
	if strings.Contains(body, i18n.T(ctx, i18n.KeyCheckoutChanged)) {
		t.Error("the general sentence is on a refusal that can name its rows")
	}
	if !strings.Contains(body, `<span class="goen-summary__flag">`+i18n.T(ctx, i18n.KeyCheckoutMovedMark)+`</span>`) {
		t.Error("the row that moved is not marked")
	}
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, checkouttest.AttemptKey(key)).
		Scan(&attempts); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if attempts != 0 {
		t.Errorf("a refused quote wrote %d checkout attempt(s)", attempts)
	}

	// The answer carries the new figures and quote, so pressing the button again places the order.
	withShown(t, body, form)
	placed := checkoutPost(t, h, token, form)
	if placed.Code != http.StatusSeeOther || !strings.HasSuffix(placed.Header().Get("Location"), "/pay") {
		t.Fatalf("the confirmed page answered %d (Location %q), want the payment page",
			placed.Code, placed.Header().Get("Location"))
	}
}

// TestHandler_PlaceOrderShownFields: the figures a page posts back change the
// words of a refusal and nothing else. The quote alone decides acceptance, so a
// field edited by hand neither lets a stale quote through nor stops a good
// one, and no swap echoes a figure it was sent.
func TestHandler_PlaceOrderShownFields(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	h := couponTestHandler(s)
	absurd := map[string]string{
		"shown_subtotal": "1", "shown_shipping": "1", "shown_discount": "999999999999",
		"shown_credit": "999999999999", "shown_total": "1",
	}

	placed := func(t *testing.T, label string, tamper bool) (shipping, discount int64) {
		t.Helper()
		token := shownCart(t, s, label)
		form := shownForm(t, "110", label+"-"+uuid.NewString())
		form.Set("update", "shipping")
		page := checkoutPost(t, h, token, form)
		form.Del("update")
		withShown(t, page.Body.String(), form)
		if tamper {
			for name, v := range absurd {
				form.Set(name, v)
			}
		}
		res := checkoutPost(t, h, token, form)
		if res.Code != http.StatusSeeOther || !strings.HasSuffix(res.Header().Get("Location"), "/pay") {
			t.Fatalf("tampered=%v: the honest quote answered %d (Location %q), want the payment page; body=%s",
				tamper, res.Code, res.Header().Get("Location"), res.Body.String())
		}
		number := strings.Split(strings.TrimPrefix(res.Header().Get("Location"), "/orders/"), "/")[0]
		if err := pool.QueryRow(ctx,
			`SELECT shipping_cents, discount_cents FROM orders WHERE order_number = $1`, number).
			Scan(&shipping, &discount); err != nil {
			t.Fatalf("read order %s: %v", number, err)
		}
		return shipping, discount
	}

	t.Run("an order costs what the quote says", func(t *testing.T) {
		honestShip, honestDiscount := placed(t, "shown-honest", false)
		tamperedShip, tamperedDiscount := placed(t, "shown-tampered", true)
		if honestShip != tamperedShip || honestDiscount != tamperedDiscount {
			t.Errorf("tampered figures priced the order at shipping %d discount %d, honest ones at %d and %d",
				tamperedShip, tamperedDiscount, honestShip, honestDiscount)
		}
	})

	t.Run("a stale quote is refused whatever the figures say", func(t *testing.T) {
		token := shownCart(t, s, "shown-stale")
		key := "shown-stale-" + uuid.NewString()
		form := shownForm(t, "110", key)
		form.Set("update", "shipping")
		page := checkoutPost(t, h, token, form)
		form.Del("update")
		withShown(t, page.Body.String(), form)
		// The figures are exactly the ones this page shows; only the quote is another cart's.
		other := shownCart(t, s, "shown-stale-other")
		otherForm := shownForm(t, "110", "shown-stale-other-"+uuid.NewString())
		otherForm.Set("update", "shipping")
		quote, _ := hiddenInputValue(checkoutPost(t, h, other, otherForm).Body.String(), "checkout_quote")
		form.Set("checkout_quote", quote)
		res := checkoutPost(t, h, token, form)
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("a stale quote with matching figures answered %d, want 422", res.Code)
		}
		if !strings.Contains(res.Body.String(), i18n.T(ctx, i18n.KeyCheckoutChanged)) {
			t.Error("with no row moved the refusal does not fall back to the general sentence")
		}
		var attempts int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, checkouttest.AttemptKey(key)).
			Scan(&attempts); err != nil {
			t.Fatalf("count attempts: %v", err)
		}
		if attempts != 0 {
			t.Errorf("a stale quote wrote %d checkout attempt(s)", attempts)
		}
	})

	t.Run("a swap answers with its own figures", func(t *testing.T) {
		token := shownCart(t, s, "shown-swap")
		form := shownForm(t, "890", "shown-swap-"+uuid.NewString())
		form.Set("update", "coupon")
		honest := checkoutPost(t, h, token, form).Body.String()
		for name, v := range absurd {
			form.Set(name, v)
		}
		tampered := checkoutPost(t, h, token, form)
		if tampered.Code != http.StatusOK {
			t.Fatalf("a swap answered %d, want 200", tampered.Code)
		}
		for _, name := range shownNames {
			want, _ := hiddenInputValue(honest, name)
			if got, _ := hiddenInputValue(tampered.Body.String(), name); got != want {
				t.Errorf("the swap echoes %s = %q, want the server's %q", name, got, want)
			}
		}
		if !strings.Contains(tampered.Body.String(), `<div id="summary-moved" hidden>`) {
			t.Error("a swap names rows that moved, though nothing was refused")
		}
	})
}
