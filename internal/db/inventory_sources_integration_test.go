//go:build integration

package db_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/koopa0/goen/internal/db"
)

const inventorySourceFixtures = `
INSERT INTO inventory_reservations (id, order_id, variant_id, quantity, expires_at)
VALUES ('eeee0001-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        '44444444-4444-4444-8444-444444444444', 1, now() + interval '1 hour');
INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000',
        '66660003-0000-4000-8000-000000000000', 1);
`

func TestInventoryMovementSourceParents(t *testing.T) {
	const variant = "44444444-4444-4444-8444-444444444444"
	const otherVariant = "4444aaaa-4444-4444-8444-444444444444"
	const missing = "00000000-0000-4000-8000-000000000000"
	const order = "66666666-6666-4666-8666-666666666666"
	const reservation = "eeee0001-0000-4000-8000-000000000001"
	const returned = "88880001-0000-4000-8000-000000000000"
	quoted := func(value string) string { return "'" + value + "'" }
	cases := []struct {
		name, variant, source, id, rule string
	}{
		{"manual receipt", variant, "'admin'", "NULL", ""},
		{"order", variant, "'order'", quoted(order), ""},
		{"reservation", variant, "'reservation'", quoted(reservation), ""},
		{"return", variant, "'return_request'", quoted(returned), ""},
		{"null source", variant, "NULL", "NULL", "inventory_movements_source_known"},
		{"unknown source", variant, "'purchase'", "NULL", "inventory_movements_source_known"},
		{"manual with id", variant, "'admin'", quoted(order), "inventory_movements_source_paired"},
		{"order without id", variant, "'order'", "NULL", "inventory_movements_source_paired"},
		{"reservation without id", variant, "'reservation'", "NULL", "inventory_movements_source_paired"},
		{"return without id", variant, "'return_request'", "NULL", "inventory_movements_source_paired"},
		{"missing order", variant, "'order'", quoted(missing), "inventory_movements_source_parent"},
		{"missing reservation", variant, "'reservation'", quoted(missing), "inventory_movements_source_parent"},
		{"missing return", variant, "'return_request'", quoted(missing), "inventory_movements_source_parent"},
		{"order id is reservation", variant, "'order'", quoted(reservation), "inventory_movements_source_parent"},
		{"reservation id is order", variant, "'reservation'", quoted(order), "inventory_movements_source_parent"},
		{"return id is order", variant, "'return_request'", quoted(order), "inventory_movements_source_parent"},
		{"reservation wrong variant", otherVariant, "'reservation'", quoted(reservation), "inventory_movements_source_parent"},
		{"return wrong variant", otherVariant, "'return_request'", quoted(returned), "inventory_movements_source_parent"},
	}
	for _, role := range []string{"admin", "schema"} {
		for _, c := range cases {
			t.Run(role+"/"+c.name, func(t *testing.T) {
				stmt := inventorySourceFixtures
				if role == "admin" {
					stmt += "SET LOCAL ROLE admin;"
					stmt += fmt.Sprintf(`SELECT record_inventory_movement('%s', 1, 'receipt', 'source-test', %s, %s);`, c.variant, c.source, c.id)
				} else {
					stmt += fmt.Sprintf(`INSERT INTO inventory_movements (variant_id, delta, reason, idempotency_key, source_type, source_id) VALUES ('%s', 1, 'receipt', 'source-test', %s, %s);`, c.variant, c.source, c.id)
				}
				err := run(t, stmt)
				if c.rule == "" {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				code, name := constraintViolation(err)
				if code != "23514" || name != c.rule {
					t.Fatalf("error = %v; want 23514/%s", err, c.rule)
				}
			})
		}
	}
}

func TestInventorySourceRefusalRollsBackStock(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, fixtures+`SET LOCAL ROLE admin; SAVEPOINT invalid_source;`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `SELECT record_inventory_movement('44444444-4444-4444-8444-444444444444', 3, 'receipt', 'bad-parent', 'order', '00000000-0000-4000-8000-000000000000')`)
	code, name := constraintViolation(err)
	if code != "23514" || name != "inventory_movements_source_parent" {
		t.Fatalf("error = %v", err)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_source`); err != nil {
		t.Fatal(err)
	}
	var stock, movements int
	if err = tx.QueryRow(ctx, `SELECT stock_quantity, (SELECT count(*) FROM inventory_movements WHERE idempotency_key = 'bad-parent') FROM product_variants WHERE id = '44444444-4444-4444-8444-444444444444'`).Scan(&stock, &movements); err != nil {
		t.Fatal(err)
	}
	if stock != 14 || movements != 0 {
		t.Fatalf("stock=%d movements=%d; want 14, 0", stock, movements)
	}
	// Omitting an optional parent is the existing manual-stock API; explicit NULL is refused separately.
	if _, err = tx.Exec(ctx, `SELECT record_inventory_movement('44444444-4444-4444-8444-444444444444', 1, 'receipt', 'manual-default')`); err != nil {
		t.Fatal(err)
	}
	var source string
	if err = tx.QueryRow(ctx, `SELECT source_type FROM inventory_movements WHERE idempotency_key = 'manual-default'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != "admin" {
		t.Fatalf("source = %q; want admin", source)
	}
}

func TestReturnInventoryHistoryResolvesOrder(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, fixtures+inventorySourceFixtures+`SET LOCAL ROLE admin;
 SELECT record_inventory_movement('44444444-4444-4444-8444-444444444444', 1, 'return', 'returned-stock', 'return_request', '88880001-0000-4000-8000-000000000000');`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.New(tx).VariantMovements(ctx, db.VariantMovementsParams{SKU: "PXL-9P-256-BL", RowLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].OrderNumber != "GO-260721-000387" {
		t.Fatalf("movements = %+v; want returned order GO-260721-000387", rows)
	}
}
