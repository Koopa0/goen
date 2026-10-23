//go:build integration

package checkout_test

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/discount/coupon"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// overTheThreshold is above the seeded home-delivery 滿 NT$3,000 免運, so the base
// rate is already nothing; freshVariant's NT$1,999 is below it.
const overTheThreshold = 350000

func freeShippingCode(t *testing.T, label string) string {
	t.Helper()
	return insertCoupon(t, label+strings.ToUpper(uuid.NewString()[:6]), "free_shipping", 0, 0, 0, 1)
}

func redemptionsOf(t *testing.T, code string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM coupon_redemptions cr
		JOIN coupons c ON c.id = cr.coupon_id WHERE c.code = $1`, code).Scan(&n); err != nil {
		t.Fatalf("count redemptions of %s: %v", code, err)
	}
	return n
}

func pricedVariant(t *testing.T, label string, cents int64) uuid.UUID {
	t.Helper()
	vid := ordertest.FreshVariant(t, pool, label)
	if _, err := pool.Exec(t.Context(),
		`UPDATE product_variants SET price_cents = $2 WHERE id = $1`, vid, cents); err != nil {
		t.Fatalf("price %s: %v", label, err)
	}
	return vid
}

func mainlandAddress(email string) *delivery.Details {
	return &delivery.Details{
		Email: email, RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
}

func checkoutPost(
	t *testing.T, h *checkout.Handler, token string, form url.Values,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)
	return res
}

func couponTestHandler(s *checkout.Store) *checkout.Handler {
	return newHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), nil)
}

// TestHandler_PlaceOrderFreeShippingNotNeeded: A free-shipping code on an order
// that already ships free is refused as not needed, both on 套用 and when
// placing, and writes nothing and spends no redemption. It is #1967 as the
// shopper meets it.
func TestHandler_PlaceOrderFreeShippingNotNeeded(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	h := couponTestHandler(s)
	code := freeShippingCode(t, "ALREADYFREE")

	token := bearer.New()
	cartID, err := s.Carts().Create(ctx, token, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := s.Carts().Add(ctx, cartID, pricedVariant(t, "already-free", overTheThreshold), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	ship := shipVersionFor(t, "home_delivery")
	addr := mainlandAddress("already-free@example.com")
	form := url.Values{
		"email": {addr.Email}, "name": {addr.RecipientName}, "phone": {addr.Phone},
		"postal_code": {addr.PostalCode}, "city": {addr.City}, "district": {addr.District},
		"street":      {addr.Street},
		"shipping":    {ship.String()},
		"coupon":      {code},
		"idempotency": {checkouttest.AttemptKey("already-free-" + uuid.NewString())},
		"update":      {"coupon"},
	}
	notNeeded := i18n.T(ctx, i18n.KeyCouponNotNeeded)

	applied := checkoutPost(t, h, token, form)
	if applied.Code != http.StatusOK {
		t.Fatalf("套用 answered %d, want 200", applied.Code)
	}
	body := applied.Body.String()
	if !strings.Contains(body, notNeeded) {
		t.Error("套用 does not tell the shopper the code is not needed on an order that already ships free")
	}
	if strings.Contains(body, fmt.Sprintf(i18n.T(ctx, i18n.KeyCouponApplied), "測試折扣")) {
		t.Error("the code is shown as applied to an order it changes nothing on")
	}
	shown, ok := hiddenInputValue(body, "checkout_quote")
	if !ok {
		t.Fatal("the page after 套用 carries no checkout quote")
	}

	// 送出訂單 with the code still in the field and the quote that page rendered.
	form.Del("update")
	form.Set("checkout_quote", shown)
	placed := checkoutPost(t, h, token, form)
	if placed.Code != http.StatusUnprocessableEntity {
		t.Fatalf("placing with a code the order does not need answered %d (Location %q), want 422",
			placed.Code, placed.Header().Get("Location"))
	}
	if !strings.Contains(placed.Body.String(), notNeeded) {
		t.Error("the refused placement does not say why the code was refused")
	}
	if n := redemptionsOf(t, code); n != 0 {
		t.Errorf("the code was redeemed %d times by an order it took nothing off, want 0", n)
	}
	var lines int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM cart_items WHERE cart_id = $1`, cartID).Scan(&lines); err != nil {
		t.Fatalf("count cart lines: %v", err)
	}
	if lines != 1 {
		t.Errorf("the refused placement left %d cart lines, want the original one", lines)
	}
}

// TestStore_PlaceOrderFreeCodeStillUsable: PlaceOrder refuses a free-shipping
// code on an order that already ships free with coupon.ErrNotNeeded and spends
// nothing, whatever the form claimed, so the single-use code still frees the
// base rate of an order below the threshold, redeemed once for 0.
func TestStore_PlaceOrderFreeCodeStillUsable(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	code := freeShippingCode(t, "STILLTHERE")
	ship := shipVersionFor(t, "home_delivery")

	free := checkouttest.NewCart(t, s)
	if err := s.Carts().Add(ctx, free, pricedVariant(t, "still-there-free", overTheThreshold), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	// What a page that accepted the code would have quoted: the code named and
	// nothing changed by it.
	facts := checkouttest.QuoteFacts(t, s, free, uuid.NullUUID{}, ship, mainlandAddress(""), "")
	if facts.ShippingCents != 0 {
		t.Fatalf("the fixture order pays %d delivery — it is not over the threshold", facts.ShippingCents)
	}
	facts.CouponCode = code
	quote, err := facts.ID()
	if err != nil {
		t.Fatalf("build quote: %v", err)
	}
	_, err = s.PlaceOrder(ctx, free, uuid.NullUUID{}, ship, mainlandAddress("free@example.com"),
		nil, code, quote, checkouttest.AttemptKey("still-there-free-"+code))
	if !errors.Is(err, coupon.ErrNotNeeded) {
		t.Fatalf("placing a free-delivery order with a free-shipping code = %v, want coupon.ErrNotNeeded", err)
	}
	if n := redemptionsOf(t, code); n != 0 {
		t.Fatalf("the refused order left %d redemptions, want 0", n)
	}

	paying := checkouttest.NewCart(t, s)
	if addErr := s.Carts().Add(ctx, paying, ordertest.FreshVariant(t, pool, "still-there-paying"), 1); addErr != nil {
		t.Fatalf("add: %v", addErr)
	}
	number, err := checkouttest.PlaceOrder(t, s, ctx, paying, uuid.NullUUID{}, ship,
		mainlandAddress("paying@example.com"), code, "still-there-paying-"+code)
	if err != nil {
		t.Fatalf("the single-use code is no longer usable on an order that needs it: %v", err)
	}
	var shipping, discount, redeemed int64
	if err := pool.QueryRow(ctx, `
		SELECT o.shipping_cents, o.discount_cents, cr.amount_cents
		FROM orders o JOIN coupon_redemptions cr ON cr.order_id = o.id
		WHERE o.order_number = $1`, number).Scan(&shipping, &discount, &redeemed); err != nil {
		t.Fatalf("read the order and its redemption: %v", err)
	}
	if shipping != 0 || discount != 0 || redeemed != 0 {
		t.Errorf("below the threshold the code gave shipping %d, discount %d, redemption %d; want 0, 0, 0",
			shipping, discount, redeemed)
	}
	if n := redemptionsOf(t, code); n != 1 {
		t.Errorf("the code has %d redemptions, want the one order that needed it", n)
	}
}

// TestHandler_PlaceOrderFreeShippingRecheck: PlaceOrder rechecks a
// free-shipping code against the threshold when placing, so a price raised past
// it after the handler accepted the code refuses the code as not needed, as a
// 422 with the form kept rather than a 500. The test holds the order
// transaction after the handler accepted the code on a cart below the
// threshold, then raises the price.
func TestHandler_PlaceOrderFreeShippingRecheck(t *testing.T) {
	ctx := t.Context()
	appName := "free-shipping-recheck-" + uuid.NewString()
	s := checkout.NewStore(applicationPool(t, appName))
	h := couponTestHandler(s)
	code := freeShippingCode(t, "RECHECK")

	vid := ordertest.FreshVariant(t, pool, "free-shipping-recheck")
	token := bearer.New()
	cartID, err := s.Carts().Create(ctx, token, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if addErr := s.Carts().Add(ctx, cartID, vid, 1); addErr != nil {
		t.Fatalf("add: %v", addErr)
	}
	ship := shipVersionFor(t, "home_delivery")
	key := checkouttest.AttemptKey("free-shipping-recheck-" + uuid.NewString())
	addr := mainlandAddress("recheck@example.com")
	form := url.Values{
		"email": {addr.Email}, "name": {addr.RecipientName}, "phone": {addr.Phone},
		"postal_code": {addr.PostalCode}, "city": {addr.City}, "district": {addr.District},
		"street":         {"松仁路 300 號"},
		"shipping":       {ship.String()},
		"coupon":         {code},
		"checkout_quote": {checkouttest.Quote(t, s, cartID, uuid.NullUUID{}, ship, &delivery.Details{PostalCode: "110"}, code).String()},
		"idempotency":    {key},
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin checkout blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	if _, err := blocker.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, key); err != nil {
		t.Fatalf("hold checkout key: %v", err)
	}

	var res *httptest.ResponseRecorder
	served := make(chan error, 1)
	go func() {
		res = checkoutPost(t, h, token, form)
		served <- nil
	}()
	waitForApplicationLock(t, appName, served)

	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET price_cents = $2 WHERE id = $1`, vid, overTheThreshold); err != nil {
		t.Fatalf("raise the price past the threshold: %v", err)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatalf("release checkout: %v", err)
	}
	select {
	case <-served:
	case <-time.After(10 * time.Second):
		t.Fatal("checkout did not finish after its key was released")
	}

	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a code the order stopped needing answered %d (Location %q), want 422",
			res.Code, res.Header().Get("Location"))
	}
	body := res.Body.String()
	for _, want := range []string{i18n.T(ctx, i18n.KeyCouponNotNeeded), "松仁路 300 號", addr.Email} {
		if !strings.Contains(body, want) {
			t.Errorf("the re-rendered checkout does not contain %q", want)
		}
	}
	if n := redemptionsOf(t, code); n != 0 {
		t.Errorf("the refused order left %d redemptions, want 0", n)
	}
}
