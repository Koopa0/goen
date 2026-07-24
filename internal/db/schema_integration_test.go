//go:build integration

// Schema conformance. Every rule 001 encodes is exercised against a value it
// must refuse — a CHECK nobody has watched reject something is a comment with
// a syntax — and against a neighbouring value it must accept, so a constraint
// cannot pass by rejecting everything.
//
// Each case runs inside a transaction that is rolled back, so the cases are
// independent and their order does not matter.
package db_test

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
)

var pool *pgxpool.Pool

// TestMain owns the container: starting one per test function would spend
// several seconds each, and every case here rolls back, so they can share.
func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database for the schema suite", "error", err)
		os.Exit(1)
	}
	pool = p

	code := m.Run()
	stop()
	os.Exit(code)
}

func schemaPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return pool
}

// run executes stmt against a rolled-back transaction that already holds the
// fixtures, and reports whether the database accepted it.
func run(t *testing.T, stmt string) error {
	t.Helper()

	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, ferr := tx.Exec(ctx, fixtures); ferr != nil {
		t.Fatalf("load fixtures: %v", ferr)
	}
	_, err = tx.Exec(ctx, stmt)
	return err
}

// expectedForeignKeys is every foreign key the schema declares, pinned by name.
// TestEveryForeignKeyIsIndexed only inspects the FKs that still exist, so
// dropping one removes it from that test's input and the drop goes unnoticed.
// This set is what makes a removed — or an unrecorded new — FK fail.
var expectedForeignKeys = map[string]bool{
	"addresses_user_id_fkey":                      true,
	"audit_events_actor_user_id_fkey":             true,
	"cart_items_cart_id_fkey":                     true,
	"cart_items_variant_id_fkey":                  true,
	"carts_user_id_fkey":                          true,
	"categories_parent_id_fkey":                   true,
	"checkout_attempts_cart_id_fkey":              true,
	"checkout_attempts_order_fk":                  true,
	"inventory_movements_actor_user_id_fkey":      true,
	"inventory_movements_variant_id_fkey":         true,
	"inventory_reservations_order_fk":             true,
	"inventory_reservations_variant_id_fkey":      true,
	"invoice_document_lines_document_id_fkey":     true,
	"invoice_documents_order_id_fkey":             true,
	"invoice_documents_original_id_fkey":          true,
	"invoice_preferences_order_id_fkey":           true,
	"order_events_actor_user_id_fkey":             true,
	"order_events_order_id_fkey":                  true,
	"order_lines_order_id_fkey":                   true,
	"order_lines_variant_id_fkey":                 true,
	"order_private_data_order_id_fkey":            true,
	"order_shipment_lines_line_fk":                true,
	"order_shipment_lines_shipment_fk":            true,
	"order_shipments_order_id_fkey":               true,
	"orders_shipping_version_id_fkey":             true,
	"orders_user_id_fkey":                         true,
	"password_reset_tokens_user_id_fkey":          true,
	"payments_order_id_fkey":                      true,
	"product_images_product_id_fkey":              true,
	"product_option_values_option_fk":             true,
	"product_options_product_id_fkey":             true,
	"product_reviews_product_id_fkey":             true,
	"product_reviews_user_id_fkey":                true,
	"product_search_documents_product_id_fkey":    true,
	"product_specs_product_id_fkey":               true,
	"product_variants_product_id_fkey":            true,
	"products_brand_id_fkey":                      true,
	"products_category_id_fkey":                   true,
	"refunds_payment_id_fkey":                     true,
	"refunds_return_request_id_fkey":              true,
	"return_request_lines_line_fk":                true,
	"return_request_lines_request_fk":             true,
	"return_request_lines_return_request_id_fkey": true,
	"return_requests_order_id_fkey":               true,
	"return_requests_requested_by_user_id_fkey":   true,
	"sale_campaign_products_campaign_id_fkey":     true,
	"sale_campaign_products_product_id_fkey":      true,
	"sessions_user_id_fkey":                       true,
	"shipping_method_versions_method_id_fkey":     true,
	"staff_totp_credentials_user_id_fkey":         true,
	"stock_notifications_user_id_fkey":            true,
	"stock_notifications_variant_id_fkey":         true,
	"store_credit_accounts_user_id_fkey":          true,
	"store_credit_entries_account_id_fkey":        true,
	"store_credit_entries_order_fk":               true,
	"store_credit_entries_reverses_id_fkey":       true,
	"user_identities_user_id_fkey":                true,
	"variant_option_values_value_fk":              true,
	"variant_option_values_variant_fk":            true,
	"warranty_registrations_order_line_id_fkey":   true,
	"warranty_registrations_user_id_fkey":         true,
	"wishlist_items_product_id_fkey":              true,
	"wishlist_items_user_id_fkey":                 true,
}

// TestForeignKeySetIsComplete requires the live foreign keys to equal
// expectedForeignKeys exactly. A dropped FK is missing from live; a new one is
// missing from the set. Either way, this is where it surfaces.
func TestForeignKeySetIsComplete(t *testing.T) {
	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT conname
		FROM pg_constraint
		WHERE contype = 'f' AND connamespace = 'public'::regnamespace
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("query foreign keys: %v", err)
	}
	defer rows.Close()

	live := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		live[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}

	for name := range live {
		if !expectedForeignKeys[name] {
			t.Errorf("foreign key %q exists but is not in expectedForeignKeys; add it", name)
		}
	}
	for name := range expectedForeignKeys {
		if !live[name] {
			t.Errorf("foreign key %q is expected but was dropped from the schema", name)
		}
	}
}

// TestEveryForeignKeyIsIndexed catches the omission PostgreSQL does not: it
// creates no index for a foreign key, so an unindexed one turns every parent
// delete into a sequential scan of the child and every join into a slow one.
func TestEveryForeignKeyIsIndexed(t *testing.T) {
	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT c.conrelid::regclass::text, c.conname
		FROM pg_constraint c
		WHERE c.contype = 'f'
		  AND connamespace = 'public'::regnamespace
		  AND NOT EXISTS (
			-- The referencing columns must be a PREFIX of some index, not merely
			-- present in one: an index on (b, a) does nothing for a lookup by a.
			-- indkey is an int2vector, and casting one straight to smallint[]
			-- yields an array whose lower bound is 0, so slicing it from 1 quietly
			-- drops the leading column. Going through its text form gives an
			-- ordinary 1-based array.
			SELECT 1 FROM pg_index i
			WHERE i.indrelid = c.conrelid
			  AND i.indisvalid AND i.indislive
			  AND (string_to_array(i.indkey::text, ' ')::smallint[])[1:array_length(c.conkey, 1)]
			      = c.conkey::smallint[]
			  AND (
			      i.indpred IS NULL
			      -- A partial index still serves the FK when its predicate is
			      -- exactly that the FK columns are NOT NULL: an FK lookup is
			      -- always an equality on those columns, which implies NOT NULL.
			      OR pg_get_expr(i.indpred, i.indrelid) = (
			          SELECT string_agg('(' || quote_ident(a.attname) || ' IS NOT NULL)', ' AND ')
			          FROM unnest(c.conkey) WITH ORDINALITY AS k(attnum, ord)
			          JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.attnum
			      )
			  )
		  )
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("query constraints: %v", err)
	}
	defer rows.Close()

	var missing []string
	for rows.Next() {
		var table, name string
		if err := rows.Scan(&table, &name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		missing = append(missing, table+" ("+name+")")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(missing) > 0 {
		t.Errorf("foreign keys with no index to support them:\n  %s", strings.Join(missing, "\n  "))
	}
}
