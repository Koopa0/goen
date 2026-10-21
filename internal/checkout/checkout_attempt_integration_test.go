//go:build integration

package checkout_test

import (
	"encoding/base64"
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
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/delivery"
)

func TestPlaceOrderIsIdempotent(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	id := newCart(t, s)
	if err := s.Carts().Add(ctx, id, variantOf(t, "pixelight-9-pro"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}

	addr := &delivery.Details{
		Email: "idem@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	key := checkoutAttemptKey("idem-key-test-1")
	shown := checkoutQuote(t, s, id, uuid.NullUUID{}, shipID, addr, "")

	first, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, "", shown, key)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	second, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, "", shown, key)
	if err != nil {
		t.Fatalf("replace with the same key: %v", err)
	}
	if first != second {
		t.Errorf("the same idempotency key produced two orders: %q then %q", first, second)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if n != 1 {
		t.Errorf("checkout_attempts holds %d rows for one key, want 1", n)
	}
}

func TestCheckoutKeyCannotBeReusedByAnotherCart(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	shippingID := shipVersionFor(t, "home_delivery")
	key := checkoutAttemptKey("cross-cart-idempotency-key")

	firstCart := newCart(t, s)
	if err := s.Carts().Add(ctx, firstCart, freshVariant(t, "key-owner"), 1); err != nil {
		t.Fatalf("add first cart: %v", err)
	}
	firstAddress := &delivery.Details{
		Email: "key-owner@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	firstQuote := checkoutQuote(
		t, s, firstCart, uuid.NullUUID{}, shippingID, firstAddress, "",
	)
	if _, err := s.PlaceOrder(
		ctx, firstCart, uuid.NullUUID{}, shippingID, firstAddress, nil, "", firstQuote, key,
	); err != nil {
		t.Fatalf("place first cart: %v", err)
	}

	secondCart := newCart(t, s)
	if err := s.Carts().Add(ctx, secondCart, freshVariant(t, "key-collision"), 1); err != nil {
		t.Fatalf("add second cart: %v", err)
	}
	secondAddress := &delivery.Details{
		Email: "key-collision@example.com", RecipientName: "李大華", Phone: "0987654321",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松仁路 2 號",
	}
	secondQuote := checkoutQuote(
		t, s, secondCart, uuid.NullUUID{}, shippingID, secondAddress, "",
	)
	if _, err := s.PlaceOrder(
		ctx, secondCart, uuid.NullUUID{}, shippingID, secondAddress, nil, "", secondQuote, key,
	); err == nil {
		t.Fatal("another cart reused an owned checkout key")
	}

	var attempts, secondOrders int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, key).Scan(&attempts); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_private_data WHERE email = $1`, secondAddress.Email).
		Scan(&secondOrders); err != nil {
		t.Fatalf("count second cart orders: %v", err)
	}
	if attempts != 1 || secondOrders != 0 {
		t.Fatalf("key collision left %d attempt(s), %d second-cart order(s)", attempts, secondOrders)
	}
	view, err := s.Carts().View(ctx, secondCart)
	if err != nil || len(view.Lines) != 1 {
		t.Fatalf("key collision changed second cart: lines=%d err=%v", len(view.Lines), err)
	}
}

func TestCheckoutReplacesMalformedAttemptIdentityBeforeWriting(t *testing.T) {
	ctx := t.Context()
	canonical := checkoutAttemptKey("noncanonical-attempt")
	for _, tt := range []struct {
		name  string
		value string
	}{
		{name: "missing"},
		{name: "whitespace", value: "   "},
		{name: "overlong", value: strings.Repeat("A", 4096)},
		{name: "noncanonical", value: canonical + "="},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := checkout.NewStore(pool)
			token := bearer.New()
			cartID, err := s.Carts().Create(ctx, token, uuid.NullUUID{})
			if err != nil {
				t.Fatalf("create cart: %v", err)
			}
			if err := s.Carts().Add(ctx, cartID, freshVariant(t, "bad-attempt-"+tt.name), 1); err != nil {
				t.Fatalf("add: %v", err)
			}
			shippingID := shipVersionFor(t, "home_delivery")
			addr := &delivery.Details{
				Email:         "attempt-" + tt.name + "@example.com",
				RecipientName: "王小明", Phone: "0912345678", PostalCode: "110",
				City: "台北市", District: "信義區", Street: "松高路 1 號",
			}
			form := url.Values{
				"email": {addr.Email}, "name": {addr.RecipientName}, "phone": {addr.Phone},
				"postal_code": {addr.PostalCode}, "city": {addr.City},
				"district": {addr.District}, "street": {addr.Street},
				"shipping":       {shippingID.String()},
				"checkout_quote": {checkoutQuote(t, s, cartID, uuid.NullUUID{}, shippingID, addr, "").String()},
				"idempotency":    {tt.value},
			}
			req := httptest.NewRequestWithContext(
				ctx, http.MethodPost, "/checkout", strings.NewReader(form.Encode()),
			)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			//nolint:gosec // G124: the browser's own cart cookie
			req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
			h := newHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), nil)

			res := httptest.NewRecorder()
			h.PlaceOrder(res, req)

			if res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("response = %d, want 422; body=%s", res.Code, res.Body.String())
			}
			fresh, found := hiddenInputValue(res.Body.String(), "idempotency")
			decoded, decodeErr := base64.RawURLEncoding.DecodeString(fresh)
			if !found || decodeErr != nil || len(decoded) != 16 ||
				base64.RawURLEncoding.EncodeToString(decoded) != fresh || fresh == tt.value {
				t.Fatalf("replacement attempt ID = %q (found=%v, decode=%v), want fresh canonical ID",
					fresh, found, decodeErr)
			}

			var orders, lines int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM order_private_data WHERE email = $1`, addr.Email).Scan(&orders); err != nil {
				t.Fatalf("count orders: %v", err)
			}
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM cart_items WHERE cart_id = $1`, cartID).Scan(&lines); err != nil {
				t.Fatalf("count cart lines: %v", err)
			}
			if orders != 0 || lines != 1 {
				t.Fatalf("malformed attempt wrote %d order(s) and left %d cart line(s)", orders, lines)
			}
			// Other tests share this database; the only relevant durable assertion is
			// that neither the refused text nor its fresh replacement was claimed.
			var refusedAttempts int
			if err := pool.QueryRow(ctx, `
				SELECT count(*) FROM checkout_attempts
				WHERE idempotency_key = $1 OR idempotency_key = $2`, tt.value, fresh).
				Scan(&refusedAttempts); err != nil {
				t.Fatalf("count refused attempts: %v", err)
			}
			if refusedAttempts != 0 {
				t.Fatalf("malformed attempt or replacement wrote %d checkout attempt(s)", refusedAttempts)
			}
		})
	}
}

func TestCheckoutHTTPReplayFindsTheSameOrderAfterTheCartIsEmpty(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	token := bearer.New()
	cartID, err := s.Carts().Create(ctx, token, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := s.Carts().Add(ctx, cartID, freshVariant(t, "http-replay"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	shippingID := shipVersionFor(t, "home_delivery")
	addr := &delivery.Details{
		Email: "replay@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	key := checkoutAttemptKey("http-replay-" + uuid.NewString())
	form := url.Values{
		"email": {addr.Email}, "name": {addr.RecipientName}, "phone": {addr.Phone},
		"postal_code": {addr.PostalCode}, "city": {addr.City}, "district": {addr.District},
		"street":         {addr.Street},
		"shipping":       {shippingID.String()},
		"checkout_quote": {checkoutQuote(t, s, cartID, uuid.NullUUID{}, shippingID, addr, "").String()},
		"idempotency":    {key},
	}
	request := func() *http.Request {
		req := httptest.NewRequestWithContext(
			ctx, http.MethodPost, "/checkout", strings.NewReader(form.Encode()),
		)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test
		return req
	}
	h := newHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), nil)

	first := httptest.NewRecorder()
	h.PlaceOrder(first, request())
	second := httptest.NewRecorder()
	h.PlaceOrder(second, request())

	if first.Code != http.StatusSeeOther || second.Code != http.StatusSeeOther {
		t.Fatalf("responses = %d, %d; want two 303s", first.Code, second.Code)
	}
	if first.Header().Get("Location") != second.Header().Get("Location") {
		t.Fatalf("replay locations differ: %q != %q",
			first.Header().Get("Location"), second.Header().Get("Location"))
	}
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, key).Scan(&attempts); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("replay wrote %d checkout attempts, want one", attempts)
	}
}

// TestADoubleClickedCheckoutPlacesOneOrder covers two requests IN FLIGHT AT
// ONCE, which the advisory lock on the key serialises. T1's transaction is held
// OPEN while T2 runs, rather than raced from two goroutines that never overlap.
func TestADoubleClickedCheckoutPlacesOneOrder(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	key := checkoutAttemptKey("double-" + uuid.NewString())
	vid := freshVariant(t, "doubleclick")

	// T1 takes the lock on the key by hand and HOLDS it, which is the state a first
	// checkout is in between its lock and its commit.
	t1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin T1: %v", err)
	}
	defer func() { _ = t1.Rollback(ctx) }()
	if _, err := t1.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, key); err != nil {
		t.Fatalf("T1 take the lock: %v", err)
	}

	id := newCart(t, s)
	if err := s.Carts().Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	placed := make(chan error, 1)
	go func() {
		_, placeErr := placeOrder(t, s, ctx, id, uuid.NullUUID{},
			shipVersionFor(t, "home_delivery"), &delivery.Details{
				Email: "double@example.com", RecipientName: "王小明", Phone: "0912345678",
				PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
			}, "", key)
		placed <- placeErr
	}()

	select {
	case err := <-placed:
		t.Fatalf("the second checkout completed (%v) while the key was held by "+
			"another transaction — it is not taking the lock", err)
	case <-time.After(750 * time.Millisecond):
	}

	if err := t1.Rollback(ctx); err != nil {
		t.Fatalf("release T1: %v", err)
	}
	select {
	case err := <-placed:
		if err != nil {
			t.Fatalf("the second checkout failed after the lock was released: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second checkout never completed after the lock was released")
	}

	var orders int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, key).
		Scan(&orders); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if orders != 1 {
		t.Errorf("the key produced %d checkout attempts, want 1", orders)
	}
}
