//go:build integration

package cart_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/order"
)

func TestStoreRoleRefusesMixedTaxCheckoutWithoutLeavingAnOrder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(storeRolePool(t))
	id := newCart(t, s)
	taxable := freshVariant(t, "taxable-cart")
	exempt := freshVariant(t, "exempt-cart")
	if _, err := pool.Exec(ctx, `UPDATE products SET tax_type='exempt', invoice_unit='包' WHERE id=(SELECT product_id FROM product_variants WHERE id=$1)`, exempt); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []uuid.UUID{taxable, exempt} {
		if err := s.Add(ctx, id, variant, 1); err != nil {
			t.Fatal(err)
		}
	}
	view, err := s.View(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !view.MixedTaxTypes || view.CanCheckout() {
		t.Fatalf("mixed cart: %+v", view)
	}
	for _, line := range view.Lines {
		want := line.VariantID == exempt.String()
		if line.TaxExempt != want {
			t.Errorf("cart line %s tax exempt = %t, want %t", line.VariantID, line.TaxExempt, want)
		}
	}
	addr := &order.Delivery{Email: "tax@example.com", RecipientName: "王小明", Phone: "0912345678", PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號"}
	key := checkoutAttemptKey("mixed-tax-" + uuid.NewString())
	_, err = placeOrder(t, s, ctx, id, uuid.NullUUID{}, shipVersionFor(t, "home_delivery"), addr, "", key)
	if !errors.Is(err, cart.ErrMixedTaxTypes) {
		t.Fatalf("mixed checkout=%v, want mixed-tax refusal", err)
	}
	var attempts int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM checkout_attempts WHERE idempotency_key=$1`, key).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Fatalf("refused checkout left %d attempts", attempts)
	}
	count, countErr := s.ItemCount(ctx, id)
	if countErr != nil || count != 2 {
		t.Fatalf("refused checkout left %d cart items: %v", count, countErr)
	}
	if _, err = pool.Exec(ctx, `UPDATE products SET tax_type='exempt' WHERE id=(SELECT product_id FROM product_variants WHERE id=$1)`, taxable); err != nil {
		t.Fatal(err)
	}
	view, err = s.View(ctx, id)
	if err != nil || view.MixedTaxTypes || !view.CanCheckout() {
		t.Fatalf("all-exempt cart: %+v: %v", view, err)
	}
	number, err := placeOrder(t, s, ctx, id, uuid.NullUUID{}, shipVersionFor(t, "home_delivery"), addr, "", key)
	if err != nil {
		t.Fatalf("all-exempt checkout=%v", err)
	}
	var exemptLines int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM order_lines l JOIN orders o ON o.id=l.order_id WHERE o.order_number=$1 AND l.tax_type='exempt'`, number).Scan(&exemptLines); err != nil || exemptLines != 2 {
		t.Fatalf("exempt snapshots=%d: %v", exemptLines, err)
	}
}
