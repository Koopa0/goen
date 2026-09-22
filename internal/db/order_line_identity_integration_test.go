//go:build integration

package db_test

import (
	"context"
	"testing"
)

func TestOrderLineIdentityPreservesLocalizedPurchaseSnapshots(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, fixtures); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE products SET name_en = 'English phone' WHERE id = '33333333-3333-4333-8333-333333333333'`); err != nil {
		t.Fatal(err)
	}
	// Exercise the checkout role: validating a snapshot must not need a new grant.
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE store`); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity, position)
		VALUES ('6666aaaa-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 'PXL-9P-256-BL', 'English phone', 100, 1, 5)
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("localized checkout snapshot: %v", err)
	}
	if _, err := tx.Exec(ctx, `RESET ROLE; UPDATE products SET name = 'Renamed phone', name_en = 'Renamed English phone' WHERE id = '33333333-3333-4333-8333-333333333333'`); err != nil {
		t.Fatal(err)
	}
	var name, sku string
	if err := tx.QueryRow(ctx, `SELECT product_name, sku FROM order_lines WHERE id = $1`, id).Scan(&name, &sku); err != nil {
		t.Fatal(err)
	}
	if name != "English phone" || sku != "PXL-9P-256-BL" {
		t.Fatalf("purchase snapshot changed: name=%q sku=%q", name, sku)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
		VALUES ('6666aaaa-6666-4666-8666-666666666666', 'LEGACY-SKU', 'Imported product', 100, 1, 6)`); err != nil {
		t.Fatalf("legacy snapshot without catalogue identity: %v", err)
	}
}
