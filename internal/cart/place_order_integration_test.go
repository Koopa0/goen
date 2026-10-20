//go:build integration

package cart_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/user"
)

func TestPlaceOrderEmptiesTheCart(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	if err := s.Add(ctx, id, freshVariant(t, "empties-cart"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &delivery.Details{
		Email: "empty@example.com", RecipientName: "李大華", Phone: "0987654321",
		PostalCode: "220", City: "新北市", District: "板橋區", Street: "文化路一段 1 號",
	}
	if _, err := placeOrder(t, s, ctx, id, uuid.NullUUID{}, shipID, addr, "", "empties-cart-1"); err != nil {
		t.Fatalf("place: %v", err)
	}

	view, err := s.View(ctx, id)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if len(view.Lines) != 0 {
		t.Errorf("the cart still holds %d lines after the order was placed", len(view.Lines))
	}
}

func TestPlaceOrderRefusesAFabricatedShippingVersion(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	if err := s.Add(ctx, id, freshVariant(t, "fabricated-shipping-version"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	addr := &delivery.Details{
		Email: "x@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	if _, err := s.PlaceOrder(
		ctx, id, uuid.NullUUID{}, uuid.New(), addr, nil, "", cart.CheckoutQuoteID{},
		checkoutAttemptKey("fabricated-1"),
	); err == nil {
		t.Error("an order was placed against a shipping version that does not exist")
	}
}

func TestPlaceOrderRefusesAnEmptyCart(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &delivery.Details{
		Email: "x@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	if _, err := s.PlaceOrder(
		ctx, id, uuid.NullUUID{}, shipID, addr, nil, "", cart.CheckoutQuoteID{},
		checkoutAttemptKey("empty-cart-1"),
	); err == nil {
		t.Error("an order was placed from an empty cart")
	}
}

func TestOrderIsAttachedToASignedInCustomer(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"owner-"+uuid.NewString()+"@example.com").Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}

	id := newCart(t, s)
	if err := s.Add(ctx, id, freshVariant(t, "attached-order"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &delivery.Details{
		Email: "owned@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	number, err := placeOrder(t, s, ctx, id, uuid.NullUUID{UUID: userID, Valid: true},
		shipID, addr, "", "owned-test-1")
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	grants := access.NewStore(pool, false)
	asUser := func(id uuid.UUID) *http.Request {
		return httptest.NewRequestWithContext(
			user.NewContext(ctx, user.User{ID: id.String()}), http.MethodGet, "/", http.NoBody)
	}
	owns, err := grants.OwnedBySignedInUser(asUser(userID), number)
	if err != nil {
		t.Fatalf("check ownership: %v", err)
	}
	if !owns {
		t.Error("a signed-in customer's order is not attached to their account")
	}

	var otherID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"other-"+uuid.NewString()+"@example.com").Scan(&otherID); err != nil {
		t.Fatalf("create other user: %v", err)
	}
	otherOwns, otherErr := grants.OwnedBySignedInUser(asUser(otherID), number)
	if otherErr != nil {
		t.Fatalf("check other ownership: %v", otherErr)
	}
	if otherOwns {
		t.Error("the order is reported as belonging to a different account")
	}

	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), testLimiter(), false)
	open := func(who uuid.UUID, cookie *http.Cookie) int {
		reqCtx := ctx
		if who != uuid.Nil {
			reqCtx = user.NewContext(ctx, user.User{ID: who.String()})
		}
		r := httptest.NewRequestWithContext(reqCtx, http.MethodGet, "/orders/"+number, http.NoBody)
		r.SetPathValue("number", number)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.OrderPage(w, r)
		return w.Code
	}
	if got := open(uuid.Nil, placedCookie(t, number)); got != http.StatusOK {
		t.Errorf("a grant holder who is not signed in got %d, want 200", got)
	}
	if got := open(userID, nil); got != http.StatusOK {
		t.Errorf("the owner without the cookie got %d, want 200", got)
	}
	if got := open(otherID, nil); got != http.StatusNotFound {
		t.Errorf("a signed-in stranger got %d, want 404", got)
	}
	if got := open(uuid.Nil, nil); got != http.StatusNotFound {
		t.Errorf("a visitor with neither grant nor account got %d, want 404", got)
	}
}

// TestASpentCouponComesBackAsAFieldErrorNotA500 drives the checkout handler,
// because the branch that maps coupon.ErrUsedUp to a 422 lives there. CouponByCode
// reads no limit — they are counted under redeem_coupon's lock — so a spent code
// passes form validation every time and is refused inside the transaction every
// time, which makes this an ordinary outcome on the buying mainline.
func TestASpentCouponComesBackAsAFieldErrorNotA500(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	code := "SPENT" + strings.ToUpper(uuid.NewString()[:6])
	if _, err := pool.Exec(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents, max_redemptions)
		VALUES ($1, '已用完', 'amount', 10000, 1)`, code); err != nil {
		t.Fatalf("create coupon: %v", err)
	}

	// Spend the only slot through the door a checkout uses, so the state under
	// test is one the application can actually produce.
	spender := newCart(t, s)
	variant := variantOf(t, "pixelight-9-pro", true)
	if err := s.Add(ctx, spender, variant, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	_, err := s.CouponByCode(ctx, code)
	if err != nil {
		t.Fatalf("find the coupon: %v", err)
	}
	addr := &delivery.Details{
		Email: "spender@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	if _, placeErr := placeOrder(t, s, ctx, spender, uuid.NullUUID{}, shipID, addr, code,
		"coupon-spend-"+code); placeErr != nil {
		t.Fatalf("spend the slot: %v", placeErr)
	}

	// A second customer types the same code.
	token := bearer.New()
	second, err := s.Create(ctx, token, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := s.Add(ctx, second, variant, 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	form := url.Values{
		"email": {"late@example.com"}, "name": {"李大華"}, "phone": {"0987654321"},
		"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"},
		"street":         {"松仁路 100 號"},
		"shipping":       {shipID.String()},
		"coupon":         {code},
		"checkout_quote": {checkoutQuote(t, s, second, uuid.NullUUID{}, shipID, &delivery.Details{PostalCode: "110"}, code).String()},
		"idempotency":    {checkoutAttemptKey("late-" + uuid.NewString())},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test

	h := cart.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil, nil)

	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)

	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a spent coupon answered %d, want 422 — a 500 discards everything "+
			"the customer typed at the moment of paying", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, "松仁路 100 號") {
		t.Error("the re-rendered form lost the street the customer had typed")
	}
	if !strings.Contains(body, "late@example.com") {
		t.Error("the re-rendered form lost the email the customer had typed")
	}
	if !strings.Contains(body, i18n.T(ctx, i18n.KeyCouponUsedUp)) {
		t.Error("the page does not say the coupon is spent, so the customer is " +
			"left to guess which field was refused")
	}
}
