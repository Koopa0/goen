//go:build integration

package buyer_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/order/ordertest"

	"github.com/google/uuid"
)

// TestHandler_OrderPageEnumeration: Handler.OrderPage answers a
// stranger 404 and leaks neither the email nor the address of a placed order,
// while the browser that placed it gets 200.
func TestHandler_OrderPageEnumeration(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	id := checkouttest.NewCart(t, s)
	if err := s.Carts().Add(ctx, id, ordertest.FreshVariant(t, pool, "confirmation-enumeration"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &delivery.Details{
		Email: "enumerate@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	number, err := checkouttest.PlaceOrder(t, s, ctx, id, uuid.NullUUID{}, shipID, addr, "", "enum-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), testLimiter(), false)

	stranger := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
	stranger.SetPathValue("number", number)
	res := httptest.NewRecorder()
	h.OrderPage(res, stranger)

	if res.Code != http.StatusNotFound {
		t.Errorf("a stranger reached the confirmation page: status %d, want 404", res.Code)
	}
	if strings.Contains(res.Body.String(), "enumerate@example.com") {
		t.Error("the confirmation page leaked the customer's email to a stranger")
	}
	if strings.Contains(res.Body.String(), "松高路") {
		t.Error("the confirmation page leaked the delivery address to a stranger")
	}

	placer := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
	placer.SetPathValue("number", number)
	placer.AddCookie(placedCookie(t, number))
	ok := httptest.NewRecorder()
	h.OrderPage(ok, placer)

	if ok.Code != http.StatusOK {
		t.Fatalf("the browser that placed the order got %d, want 200", ok.Code)
	}
	if !strings.Contains(ok.Body.String(), number) {
		t.Error("the confirmation page does not name the order it confirms")
	}
}

// TestHandler_OrderPageForgedCookie: Handler.OrderPage answers 404
// to a forged goen_placed cookie, which leaks no email, while the browser
// holding a real token gets 200.
func TestHandler_OrderPageForgedCookie(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), testLimiter(), false)
	number := checkouttest.PlaceUnpaidOrderFor(t, pool, s, "forged@example.com")

	// The VICTIM's own browser holds a REAL grant before the attack starts: without
	// it the access query's EXISTS is false whatever the digest comparison does, and
	// the case stays green with `g.digest = ANY(...)` replaced by `OR true`.
	victim := placedCookie(t, number)

	for _, value := range []string{
		number,
		anotherOrderNumber(number),
		number + "." + number,
	} {
		t.Run(value, func(t *testing.T) {
			r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
			r.SetPathValue("number", number)
			r.AddCookie(&http.Cookie{Name: "goen_placed", Value: value}) //nolint:gosec // G124: the forgery under test
			w := httptest.NewRecorder()
			h.OrderPage(w, r)

			if w.Code != http.StatusNotFound {
				t.Errorf("a forged cookie reached the order page: status %d, want 404", w.Code)
			}
			if strings.Contains(w.Body.String(), "forged@example.com") {
				t.Error("a forged cookie leaked the customer's email")
			}
		})
	}

	held := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
	held.SetPathValue("number", number)
	held.AddCookie(victim)
	ok := httptest.NewRecorder()
	h.OrderPage(ok, held)
	if ok.Code != http.StatusOK {
		t.Fatalf("the browser holding a real token got %d, want 200", ok.Code)
	}
}

// TestHandler_OrderPageOtherOrder: Handler.OrderPage answers
// 404 to a token granted for another order and leaks nothing of it.
func TestHandler_OrderPageOtherOrder(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), testLimiter(), false)

	mine := checkouttest.PlaceUnpaidOrderFor(t, pool, s, "mine@example.com")
	theirs := checkouttest.PlaceUnpaidOrderFor(t, pool, s, "theirs@example.com")

	placedCookie(t, theirs)

	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+theirs, http.NoBody)
	r.SetPathValue("number", theirs)
	r.AddCookie(placedCookie(t, mine))
	w := httptest.NewRecorder()
	h.OrderPage(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("a token for %s reached %s: status %d", mine, theirs, w.Code)
	}
	if strings.Contains(w.Body.String(), "theirs@example.com") {
		t.Error("one order's token leaked another order's email")
	}
}

// anotherOrderNumber is a well-formed number that differs from number in its
// last character, as a neighbouring order's would.
func anotherOrderNumber(number string) string {
	last := "0"
	if strings.HasSuffix(number, "0") {
		last = "1"
	}
	return number[:len(number)-1] + last
}
