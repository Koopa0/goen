//go:build integration

package cart_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/delivery"
)

func TestCompanyInvoiceSnapshotsTheRegisteredBuyerNotTheRecipient(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(storeRolePool(t))
	cartID := newCart(t, s)
	if err := s.Add(ctx, cartID, freshVariant(t, "company-invoice-buyer"), 1); err != nil {
		t.Fatalf("add company invoice line: %v", err)
	}
	shippingID := shipVersionFor(t, "home_delivery")
	addr := &delivery.Details{
		Email: "company-buyer@example.com", RecipientName: "收件人李大華", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	inv := &cart.Invoice{
		Type: "company", CompanyName: "買受股份有限公司", TaxID: "04595252",
	}
	shown := checkoutQuote(t, s, cartID, uuid.NullUUID{}, shippingID, addr, "")
	number, err := s.PlaceOrder(
		ctx, cartID, uuid.NullUUID{}, shippingID, addr, inv, "", shown,
		checkoutAttemptKey("company-invoice-buyer-"+uuid.NewString()),
	)
	if err != nil {
		t.Fatalf("place company invoice order: %v", err)
	}

	var buyerName, taxID, recipientName string
	if err := pool.QueryRow(ctx, `
		SELECT ip.customer_name, ip.tax_id, opd.recipient_name
		FROM orders o
		JOIN invoice_preferences ip ON ip.order_id=o.id
		JOIN order_private_data opd ON opd.order_id=o.id
		WHERE o.order_number=$1`, number).Scan(&buyerName, &taxID, &recipientName); err != nil {
		t.Fatalf("read company filing snapshot: %v", err)
	}
	if buyerName != "買受股份有限公司" || taxID != "04595252" ||
		recipientName != "收件人李大華" {
		t.Fatalf("company filing/recipient snapshot = %q/%q/%q", buyerName, taxID, recipientName)
	}
}

func TestOrderPricesAreCopiedNotReferenced(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	vid := variantOf(t, "koto-over-ear", true)
	if err := s.Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &delivery.Details{
		Email: "price@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	number, err := placeOrder(t, s, ctx, id, uuid.NullUUID{}, shipID, addr, "", "copied-price-1")
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	before, err := buyer.NewStore(pool).Order(ctx, number)
	if err != nil {
		t.Fatalf("read order: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET price_cents = price_cents + 100000 WHERE id = $1`, vid); err != nil {
		t.Fatalf("reprice: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), //nolint:usetesting // t.Context is already cancelled in Cleanup
			`UPDATE product_variants SET price_cents = price_cents - 100000 WHERE id = $1`, vid)
	})

	after, readErr := buyer.NewStore(pool).Order(ctx, number)
	if readErr != nil {
		t.Fatalf("re-read order: %v", readErr)
	}
	if before.SubtotalCents != after.SubtotalCents {
		t.Errorf("the order's subtotal moved with the catalogue: %d then %d",
			before.SubtotalCents, after.SubtotalCents)
	}
}

// TestAnOrderLineIsSnapshottedInTheBuyersLanguage. order_lines.product_name is
// a SNAPSHOT, so the language is decided at PLACEMENT by the cart read that
// localizes; a receipt already sent must not start disagreeing with itself.
func TestAnOrderLineIsSnapshottedInTheBuyersLanguage(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	vid, zhName, enName := translatedVariant(t)

	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
		other  string
	}{
		{name: "a Chinese buyer", locale: i18n.ZhHant, want: zhName, other: enName},
		{name: "an English buyer", locale: i18n.En, want: enName, other: zhName},
	} {
		t.Run(tt.name, func(t *testing.T) {
			buying := i18n.WithLocale(ctx, tt.locale)
			number := placeOrderInLocale(t, buying, s, vid, string(tt.locale))

			var snapshot string
			if err := pool.QueryRow(ctx, `
				SELECT ol.product_name FROM order_lines ol
				JOIN orders o ON o.id = ol.order_id
				WHERE o.order_number = $1`, number).Scan(&snapshot); err != nil {
				t.Fatalf("read the line: %v", err)
			}
			if snapshot != tt.want {
				t.Errorf("the line was snapshotted as %q, want %q", snapshot, tt.want)
			}
			if snapshot == tt.other {
				t.Errorf("the line was snapshotted in the other language: %q", snapshot)
			}
		})
	}
}

// TestAnOrderLineSnapshotsTheWarrantyPromise holds the checkout side of the
// warranty boundary: the immutable order line receives the promise visible at
// placement, and later catalogue edits cannot reach it.
func TestAnOrderLineSnapshotsTheWarrantyPromise(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "warranty-promise-snapshot")
	const originalNote = "Original checkout promise"

	var productID uuid.UUID
	if err := pool.QueryRow(ctx, `
		UPDATE products p
		SET warranty_months = 36, warranty_note = $2
		FROM product_variants pv
		WHERE pv.id = $1 AND p.id = pv.product_id
		RETURNING p.id`, vid, originalNote).Scan(&productID); err != nil {
		t.Fatalf("set checkout warranty: %v", err)
	}
	number := placeOrderInLocale(t, ctx, s, vid, "warranty-promise")

	readSnapshot := func(stage string) (int, string) {
		t.Helper()
		var months int
		var note string
		if err := pool.QueryRow(ctx, `
			SELECT ol.warranty_months, ol.warranty_note
			FROM order_lines ol
			JOIN orders o ON o.id = ol.order_id
			WHERE o.order_number = $1`, number).Scan(&months, &note); err != nil {
			t.Fatalf("%s: read warranty snapshot: %v", stage, err)
		}
		return months, note
	}
	if months, note := readSnapshot("at placement"); months != 36 || note != originalNote {
		t.Errorf("checkout copied %d months / %q, want 36 / %q", months, note, originalNote)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE products
		SET warranty_months = 3, warranty_note = 'Shorter live promise'
		WHERE id = $1`, productID); err != nil {
		t.Fatalf("edit live warranty: %v", err)
	}
	if months, note := readSnapshot("after live edit"); months != 36 || note != originalNote {
		t.Errorf("catalogue edit rewrote order to %d months / %q, want 36 / %q",
			months, note, originalNote)
	}
}

// placeOrderInLocale puts one unit of vid through a cart and places the order,
// carrying the buying context's locale.
func placeOrderInLocale(
	t *testing.T, ctx context.Context, s *cart.Store, vid uuid.UUID, key string,
) string {
	t.Helper()

	id := newCart(t, s)
	if err := s.Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).
		Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &delivery.Details{
		Email: "snapshot@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	number, err := placeOrder(t, s, ctx, id, uuid.NullUUID{}, shipID, addr, "",
		"snapshot-"+key+"-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	return number
}
