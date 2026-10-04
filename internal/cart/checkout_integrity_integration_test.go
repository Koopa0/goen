//go:build integration

package cart_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/user"
)

// integrityCheckout is one signed-in customer's cart, ready to check out: two
// units of a fresh variant, NT$50 of store credit, and a coupon they could use.
type integrityCheckout struct {
	s       *cart.Store
	h       *cart.Handler
	userID  uuid.UUID
	cartID  uuid.UUID
	token   string
	variant uuid.UUID
	shipID  uuid.UUID
	coupon  string
	addr    *cart.Address
}

func newIntegrityCheckout(t *testing.T) *integrityCheckout {
	t.Helper()
	ctx := t.Context()
	s := cart.NewStore(pool)
	userID := creditedCustomer(t, 5000)
	token, err := cart.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	cartID, err := s.Create(ctx, token, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	variant := freshVariant(t, "integrity")
	if err := s.Add(ctx, cartID, variant, 2); err != nil {
		t.Fatalf("add: %v", err)
	}
	return &integrityCheckout{
		s: s,
		h: cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, ratelimit.New(ratelimit.Config{
			Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000,
		}), nil, nil),
		userID: userID, cartID: cartID, token: token, variant: variant,
		shipID: shipVersionFor(t, "home_delivery"),
		coupon: coupon(t, "INTEGRITY"+strings.ToUpper(uuid.NewString()[:8]), "amount", 10000, 0, 0, 0, 0),
		addr: &cart.Address{
			Email: "integrity@example.com", Name: "王小明", Phone: "0912345678",
			PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
		},
	}
}

func (c *integrityCheckout) owner() uuid.NullUUID {
	return uuid.NullUUID{UUID: c.userID, Valid: true}
}

// facts is what the checkout genuinely renders for this cart with couponCode.
func (c *integrityCheckout) facts(t *testing.T, couponCode string) cart.CheckoutQuote {
	t.Helper()
	return checkoutQuoteFacts(t, c.s, c.cartID, c.owner(), c.shipID, c.addr, couponCode)
}

// post submits the checkout form carrying quote and couponField, as this
// customer's own browser would, under a fresh attempt key.
func (c *integrityCheckout) post(
	t *testing.T, quote cart.CheckoutQuote, couponField string,
) (res *httptest.ResponseRecorder, key string) {
	t.Helper()
	id, err := quote.ID()
	if err != nil {
		t.Fatalf("hash the submitted quote: %v", err)
	}
	key = checkoutAttemptKey("integrity-" + uuid.NewString())
	form := url.Values{
		"email": {c.addr.Email}, "name": {c.addr.Name}, "phone": {c.addr.Phone},
		"postal_code": {c.addr.PostalCode}, "city": {c.addr.City},
		"district": {c.addr.District}, "street": {c.addr.Street},
		"shipping":       {c.shipID.String()},
		"coupon":         {couponField},
		"checkout_quote": {id.String()},
		"idempotency":    {key},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: c.token}) //nolint:gosec // G124: dev cart cookie under test
	req = req.WithContext(user.NewContext(req.Context(), user.User{
		ID: c.userID.String(), Email: c.addr.Email, Role: user.RoleCustomer,
	}))
	res = httptest.NewRecorder()
	c.h.PlaceOrder(res, req)
	return res, key
}

// wrote reports everything a placement writes for this customer: an attempt
// under key, an order, a stock hold on the variant, a coupon redemption, and
// a store-credit movement.
func (c *integrityCheckout) wrote(t *testing.T, key string) map[string]int {
	t.Helper()
	ctx := t.Context()
	var attempts, orders, holds, redemptions, creditEntries int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1),
		       (SELECT count(*) FROM orders WHERE user_id = $2),
		       (SELECT count(*) FROM inventory_reservations WHERE variant_id = $3),
		       (SELECT count(*) FROM coupon_redemptions r JOIN coupons k ON k.id = r.coupon_id
		         WHERE k.code = $4),
		       (SELECT count(*) FROM store_credit_entries e
		          JOIN store_credit_accounts a ON a.id = e.account_id WHERE a.user_id = $2)`,
		key, c.userID, c.variant, c.coupon).
		Scan(&attempts, &orders, &holds, &redemptions, &creditEntries); err != nil {
		t.Fatalf("count checkout writes: %v", err)
	}
	return map[string]int{
		"checkout attempts": attempts, "orders": orders, "stock holds": holds,
		"coupon redemptions": redemptions,
		// The grant that funded the account is the one entry that must exist.
		"credit movements beyond the grant": creditEntries - 1,
	}
}

// TestCheckoutPlacesNothingOnAQuoteItDidNotRender holds that every figure an
// order is placed at comes from the server: the price, the quantity, the
// shipping, the discount and the store credit are rebuilt from locked rows and
// the submission is accepted only when that rebuild is exactly the quote this
// checkout rendered. A quote naming anything else is answered 422 and writes no
// order, no hold, no redemption and no credit movement.
func TestCheckoutPlacesNothingOnAQuoteItDidNotRender(t *testing.T) {
	tests := []struct {
		name string
		// submit returns what the form carries, given what the checkout renders
		// without the coupon and with it.
		submit func(c *integrityCheckout, rendered, withCoupon cart.CheckoutQuote) (cart.CheckoutQuote, string)
	}{
		{
			name: "the quote another cart was shown",
			submit: func(_ *integrityCheckout, q, _ cart.CheckoutQuote) (cart.CheckoutQuote, string) {
				q.CartID = uuid.New()
				return q, ""
			},
		},
		{
			name: "a line priced below the catalogue",
			submit: func(_ *integrityCheckout, q, _ cart.CheckoutQuote) (cart.CheckoutQuote, string) {
				q.Lines[0].UnitCents -= 100000
				return q, ""
			},
		},
		{
			name: "a quantity the cart does not hold",
			submit: func(_ *integrityCheckout, q, _ cart.CheckoutQuote) (cart.CheckoutQuote, string) {
				q.Lines[0].Quantity = 1
				return q, ""
			},
		},
		{
			name: "a shipping fee the method does not charge",
			submit: func(_ *integrityCheckout, q, _ cart.CheckoutQuote) (cart.CheckoutQuote, string) {
				if q.ShippingCents == 0 {
					q.ShippingCents = 1
				} else {
					q.ShippingCents = 0
				}
				return q, ""
			},
		},
		{
			name: "more store credit than the account holds",
			submit: func(_ *integrityCheckout, q, _ cart.CheckoutQuote) (cart.CheckoutQuote, string) {
				q.CreditCents *= 20
				return q, ""
			},
		},
		{
			name: "a discount with no coupon on the form",
			submit: func(_ *integrityCheckout, _, withCoupon cart.CheckoutQuote) (cart.CheckoutQuote, string) {
				return withCoupon, ""
			},
		},
		{
			name: "a coupon on the form the rendered quote did not apply",
			submit: func(c *integrityCheckout, q, _ cart.CheckoutQuote) (cart.CheckoutQuote, string) {
				return q, c.coupon
			},
		},
		{
			name: "a larger discount than the coupon gives",
			submit: func(c *integrityCheckout, _, withCoupon cart.CheckoutQuote) (cart.CheckoutQuote, string) {
				q := withCoupon
				q.DiscountCents *= 5
				return q, c.coupon
			},
		},
		{
			name: "a coupon code that names no coupon",
			submit: func(_ *integrityCheckout, q, _ cart.CheckoutQuote) (cart.CheckoutQuote, string) {
				return q, "NOSUCHCOUPON" + strings.ToUpper(uuid.NewString()[:6])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newIntegrityCheckout(t)
			rendered := c.facts(t, "")
			if rendered.CreditCents <= 0 {
				t.Fatalf("the fixture renders %d cents of credit; the credit case would prove nothing",
					rendered.CreditCents)
			}

			submitted, couponField := tt.submit(c, rendered, c.facts(t, c.coupon))
			res, key := c.post(t, submitted, couponField)
			if res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("checkout answered %d (Location %q), want 422",
					res.Code, res.Header().Get("Location"))
			}
			for what, n := range c.wrote(t, key) {
				if n != 0 {
					t.Errorf("a refused checkout wrote %d %s, want 0", n, what)
				}
			}
			view, err := c.s.View(t.Context(), c.cartID)
			if err != nil {
				t.Fatalf("read the cart: %v", err)
			}
			if len(view.Lines) != 1 || view.Lines[0].Quantity != 2 {
				t.Errorf("a refused checkout changed the cart to %+v", view.Lines)
			}

			// The same customer, cart and form place the order with the quote
			// the checkout rendered: the refusal was about the quote alone.
			placed, placedKey := c.post(t, c.facts(t, ""), "")
			if placed.Code != http.StatusSeeOther ||
				!strings.HasSuffix(placed.Header().Get("Location"), "/pay") {
				t.Fatalf("the rendered quote answered %d (Location %q), want 303 to payment",
					placed.Code, placed.Header().Get("Location"))
			}
			if got := c.wrote(t, placedKey); got["orders"] != 1 || got["stock holds"] != 1 {
				t.Errorf("the rendered quote wrote %v, want one order and one hold", got)
			}
		})
	}
}

// TestNoPostedQuantityReachesACartOutsideItsBounds holds the cart's quantity
// at the HTTP boundary: whatever a POST carries — nothing, zero, a negative,
// a number past int32, or text — a line holds between one and the cart's
// ceiling and never more than can be supplied, which is the only quantity a
// checkout quote can then name.
func TestNoPostedQuantityReachesACartOutsideItsBounds(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, ratelimit.New(ratelimit.Config{
		Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000,
	}), nil, nil)

	for _, quantity := range []string{
		"", "0", "-1", "-2147483648", "2147483647", "2147483648",
		"99999999999999999999", "1e3", "0x10", " 3 ", "１",
	} {
		t.Run(strconv.Quote(quantity), func(t *testing.T) {
			cartID, token := newCartSession(t, s)
			variant := freshVariant(t, "quantity-bounds")

			for _, route := range []struct {
				path  string
				serve func(http.ResponseWriter, *http.Request)
			}{
				{"/cart/items", h.AddItem},
				{"/cart/items/update", h.UpdateItem},
			} {
				form := url.Values{"variant": {variant.String()}, "quantity": {quantity}}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, route.path,
					strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test
				res := httptest.NewRecorder()
				route.serve(res, req)
				if res.Code != http.StatusSeeOther {
					t.Fatalf("POST %s quantity=%q answered %d, want 303", route.path, quantity, res.Code)
				}

				var held, stock int
				err := pool.QueryRow(ctx, `
					SELECT coalesce((SELECT quantity FROM cart_items
					                 WHERE cart_id = $1 AND variant_id = $2), 0),
					       (SELECT stock_quantity - safety_stock FROM product_variants WHERE id = $2)`,
					cartID, variant).Scan(&held, &stock)
				if err != nil {
					t.Fatalf("read the cart line: %v", err)
				}
				if held < 0 || held > cart.MaxLineQuantity || held > stock {
					t.Errorf("after POST %s quantity=%q the line holds %d (supply %d, ceiling %d)",
						route.path, quantity, held, stock, cart.MaxLineQuantity)
				}
			}
		})
	}
}
