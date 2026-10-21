//go:build integration

package checkout_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/order/delivery"
)

func TestSetQuantityClampsToWhatCanBeSupplied(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)

	vid := freshVariant(t, "stockfix-setqty")
	var wasStock, wasSafety int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity, safety_stock FROM product_variants WHERE id = $1`,
		vid).Scan(&wasStock, &wasSafety); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), //nolint:usetesting // t.Context is already cancelled in Cleanup
			`UPDATE product_variants SET stock_quantity = $2, safety_stock = $3 WHERE id = $1`,
			vid, wasStock, wasSafety)
	})
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET stock_quantity = 5, safety_stock = 2 WHERE id = $1`,
		vid); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	// 3 can be sold: 5 on hand less safety stock of 2.
	id := newCart(t, s)
	if err := s.Carts().Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Set quantity above sellable clamps to 3.
	if err := s.Carts().SetQuantity(ctx, id, vid, 10); !errors.Is(err, cart.ErrQuantityAdjusted) {
		t.Fatalf("set quantity over stock returned %v, want ErrQuantityAdjusted", err)
	}

	var stored int32
	if err := pool.QueryRow(ctx,
		`SELECT quantity FROM cart_items WHERE cart_id = $1 AND variant_id = $2`,
		id, vid).Scan(&stored); err != nil {
		t.Fatalf("read line: %v", err)
	}
	if stored != 3 {
		t.Errorf("set quantity to 10 on variant with 3 sellable, cart holds %d; want 3", stored)
	}

	// The clamped cart can checkout without ErrUnavailable dead-end.
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &delivery.Details{
		Email: "setqty-checkout@example.com", RecipientName: "李大華", Phone: "0987654321",
		PostalCode: "220", City: "新北市", District: "板橋區", Street: "文化路一段 1 號",
	}
	if _, err := placeOrder(t, s, ctx, id, uuid.NullUUID{}, shipID, addr, "", "setqty-checkout-1"); err != nil {
		t.Fatalf("checkout after set quantity clamp returned error: %v", err)
	}
}
