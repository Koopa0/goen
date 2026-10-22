//go:build integration

package db_test

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestOrderNoTaxColumn: no column of orders adds tax on top of the price,
// because every price in the shop already includes it.
//
// TestOrderNoTaxColumn holds the decision that every price in the shop
// already includes tax: an order's total is its lines less the discount plus
// shipping, and no column adds tax on top. The ECPay request says the same
// (vat='1'), and the Terms promise it. Bringing a tax figure back is a
// product decision with its own migration, so it must go through this test.
func TestOrderNoTaxColumn(t *testing.T) {
	ctx := t.Context()

	rows, err := schemaPool(t).Query(ctx, `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'orders' AND column_name ~ 'tax'
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("read the orders columns: %v", err)
	}
	columns, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read the orders columns: %v", err)
	}
	if len(columns) > 0 {
		t.Errorf("orders has tax columns %v: prices include tax, so a total adds none", columns)
	}

	// A formula that kept naming the column would stop working with it, but a
	// function or view created later could name a new one: refuse the old
	// spelling in every body and view definition in the catalogue.
	rows, err = schemaPool(t).Query(ctx, `
		SELECT p.proname FROM pg_proc p
		WHERE p.pronamespace = 'public'::regnamespace AND p.prosrc ~* 'tax_cents'
		UNION ALL
		SELECT c.relname FROM pg_class c
		WHERE c.relnamespace = 'public'::regnamespace AND c.relkind = 'v'
		  AND pg_get_viewdef(c.oid) ~* 'tax_cents'
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("read the catalogue for tax_cents: %v", err)
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read the catalogue for tax_cents: %v", err)
	}
	if len(users) > 0 {
		t.Errorf("these still name tax_cents: %v", users)
	}

	// The writer's side: a statement that sets a tax figure cannot run.
	err = run(t, `INSERT INTO orders (id, tax_cents, shipping_version_id, shipping_method_code, shipping_method_name)
		VALUES ('11110001-0000-4000-8000-000000000001', 1, 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`)
	if code, _ := constraintViolation(err); code != "42703" {
		t.Errorf("an insert naming tax_cents = %v, want undefined_column (42703)", err)
	}
}
