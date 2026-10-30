//go:build integration

package buyer_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/discount/coupon"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/order/ordertest"
	"github.com/koopa0/goen/internal/ui/frame"
)

// placeWithCredit places an order through the real checkout for a customer holding `balance` of store credit.
func placeWithCredit(t *testing.T, balance int64) string {
	t.Helper()
	ctx := t.Context()
	s := checkout.NewStore(pool)
	userID, _ := checkouttest.CreditedAccount(t, pool, balance)

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	cartID := checkouttest.NewCart(t, s)
	if err := s.Carts().Add(ctx, cartID, ordertest.FreshVariant(t, pool, "credit-history"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	addr := &delivery.Details{
		Email: "credit-history@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	number, err := checkouttest.PlaceOrder(t, s, ctx, cartID, uuid.NullUUID{UUID: userID, Valid: true}, shipID, addr, "", "credit-history-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	return number
}

func kindsOf(events []buyer.OrderEvent) []order.EventKind {
	kinds := make([]order.EventKind, 0, len(events))
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	return kinds
}

// TestStore_OrderCreditPaidTimeline: an order store credit paid in full has no payment event, so
// Store.Order adds a display-only paid entry right after the placement; an order credit paid only
// in part, which still owes money, gets none, and nothing is written for either.
func TestStore_OrderCreditPaidTimeline(t *testing.T) {
	ctx := t.Context()
	s := buyer.NewStore(pool)

	full := placeWithCredit(t, 100_000_000)
	view, err := s.Order(ctx, full)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if !view.PaidByCredit() {
		t.Fatalf("the fixture owes %d with %d of credit, want an order credit paid in full", view.OwedCents, view.CreditCents)
	}
	if got, want := kindsOf(view.Timeline), []order.EventKind{order.EventPlaced, order.EventPaid}; !slices.Equal(got, want) {
		t.Fatalf("the credit-paid timeline is %v, want %v", got, want)
	}
	if paid := view.Timeline[1]; !paid.ByCredit || !paid.At.Equal(view.PlacedAt) {
		t.Errorf("the paid entry is %+v, want a credit entry dated at the placement %v", paid, view.PlacedAt)
	}
	var stored int
	if err = pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events WHERE kind = 'paid' AND order_id = (SELECT id FROM orders WHERE order_number = $1)`, full).Scan(&stored); err != nil || stored != 0 {
		t.Errorf("stored paid events = %d, %v, want none: the entry is display only", stored, err)
	}

	zh := i18n.WithLocale(ctx, i18n.ZhHant)
	var page strings.Builder
	if err = buyer.Order(frame.Page{Title: "order"}, &view).Render(zh, &page); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(page.String(), i18n.T(zh, i18n.KeyEventPaidByCredit)) {
		t.Errorf("the order page does not say %q", i18n.T(zh, i18n.KeyEventPaidByCredit))
	}

	partial, _, _ := checkouttest.PlaceLapsed(t, pool, "part-credit")
	view, err = s.Order(ctx, partial)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if view.OwedCents == 0 || view.CreditCents == 0 {
		t.Fatalf("the fixture owes %d with %d of credit, want a part-credited order that still owes", view.OwedCents, view.CreditCents)
	}
	if got, want := kindsOf(view.Timeline), []order.EventKind{order.EventPlaced}; !slices.Equal(got, want) {
		t.Errorf("the part-credited timeline is %v, want %v", got, want)
	}
}

// TestStore_OrderFreeDeliveryCode: an order placed with a free-delivery code reports the coupon
// with nothing taken off, and its page lists the code with what it did.
func TestStore_OrderFreeDeliveryCode(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	code := "SHIP" + strings.ToUpper(uuid.NewString()[:6])
	if _, err := pool.Exec(ctx, `
		INSERT INTO coupons (code, description, kind) VALUES ($1, '免運活動', 'free_shipping')`, code); err != nil {
		t.Fatalf("create coupon: %v", err)
	}
	number, err := checkouttest.PlaceWithCoupon(t, pool, checkout.NewStore(pool), code, 0)
	if err != nil {
		t.Fatalf("place with %s: %v", code, err)
	}

	view, err := buyer.NewStore(pool).Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if want := (coupon.Redeemed{Code: code, Description: "免運活動", Kind: coupon.FreeShipping}); view.Coupon != want {
		t.Errorf("Coupon = %+v, want %+v", view.Coupon, want)
	}
	if view.DiscountCents != 0 {
		t.Errorf("a free-delivery code took %d off the goods, want nothing", view.DiscountCents)
	}
	var page strings.Builder
	if err := buyer.Order(frame.Page{Title: "order"}, &view).Render(ctx, &page); err != nil {
		t.Fatalf("render: %v", err)
	}
	if want := "<dt>折扣碼 " + code + "：免運</dt>"; !strings.Contains(page.String(), want) {
		t.Errorf("the order page lacks %q", want)
	}
}
