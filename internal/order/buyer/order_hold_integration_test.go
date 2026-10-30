//go:build integration

package buyer_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/shoptime"
)

// TestHandler_OrderPageHold: an unpaid order's page offers 前往付款 and states the
// hold's end from the stored reservations, in the words the pay page uses, and does
// not offer to buy the same goods again.
func TestHandler_OrderPageHold(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	asStore := storeRolePool(t)
	h := buyer.NewHandler(buyer.NewStore(asStore), access.NewStore(asStore, false), slog.New(slog.DiscardHandler), testLimiter(), true)
	number := checkouttest.PlaceUnpaidOrderFor(t, pool, checkout.NewStore(asStore), "hold@example.com")

	var from, until time.Time
	if err := pool.QueryRow(ctx, `SELECT min(created_at), min(expires_at) FROM inventory_reservations WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`, number).Scan(&from, &until); err != nil {
		t.Fatalf("read the stored hold: %v", err)
	}
	lead, _, ok := payment.PayHold{PlacedAt: from, Until: until}.Window(ctx)
	if !ok || !strings.Contains(lead, shoptime.ClockText(until)) {
		t.Fatalf("the pay page's hold wording %q does not name the stored expiry %s", lead, shoptime.ClockText(until))
	}

	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
	r.SetPathValue("number", number)
	r.AddCookie(placedCookie(t, number))
	w := httptest.NewRecorder()
	h.OrderPage(w, r)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("order page status = %d, want %d", w.Code, http.StatusOK)
	}
	for _, want := range []string{lead, `href="/orders/` + number + `/pay"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the unpaid order page lacks %q", want)
		}
	}
	if strings.Contains(body, `action="/orders/`+number+`/reorder"`) {
		t.Error("the unpaid order page offers to buy the same goods again")
	}
}
