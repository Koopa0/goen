//go:build integration

package db_test

import (
	"regexp"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/money"
)

// uncappedMoney names a cents column that may carry no ceiling, with the
// reason. It is empty: every amount 001 stores is bounded.
var uncappedMoney = map[string]string{}

// TestEveryMoneyColumnHasACeiling holds money.MaxCents ("the ceiling every
// money column carries") to the schema: each *_cents column of each table has a
// CHECK bounding it at or below that ceiling, or a written reason in
// uncappedMoney. Six had none. orders.shipping_cents among them let the store
// role commit a delivery fee of 9e18 cents, and one at bigint's limit made
// order_totals fail with "bigint out of range" for every list that reads it.
func TestEveryMoneyColumnHasACeiling(t *testing.T) {
	ctx := t.Context()
	rows, err := schemaPool(t).Query(ctx, `
		SELECT c.relname, a.attname,
		       coalesce(array_agg(pg_get_constraintdef(k.oid)) FILTER (WHERE k.oid IS NOT NULL), '{}')
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		LEFT JOIN pg_constraint k ON k.conrelid = c.oid AND k.contype = 'c' AND a.attnum = ANY (k.conkey)
		WHERE c.relnamespace = 'public'::regnamespace AND c.relkind = 'r'
		  AND a.attnum > 0 AND NOT a.attisdropped AND a.attname LIKE '%\_cents'
		GROUP BY c.relname, a.attname
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("list money columns: %v", err)
	}
	type column struct {
		Table, Name string
		Checks      []string
	}
	columns, err := pgx.CollectRows(rows, pgx.RowToStructByPos[column])
	if err != nil {
		t.Fatalf("read money columns: %v", err)
	}
	if len(columns) < 20 {
		t.Fatalf("found %d *_cents columns, want at least 20 (the schema has 26): the query no longer sees them", len(columns))
	}

	seen := map[string]bool{}
	for _, col := range columns {
		name := col.Table + "." + col.Name
		seen[name] = true
		ceiling := regexp.MustCompile(`\b` + regexp.QuoteMeta(col.Name) + `\s*<=\s*'?(\d+)`)
		capped := false
		for _, def := range col.Checks {
			for _, m := range ceiling.FindAllStringSubmatch(def, -1) {
				n, convErr := strconv.ParseInt(m[1], 10, 64)
				if convErr != nil {
					t.Errorf("%s: bound %q: %v", name, m[1], convErr)
					continue
				}
				if n > money.MaxCents {
					t.Errorf("%s is capped at %d, above money.MaxCents (%d)", name, n, int64(money.MaxCents))
					continue
				}
				capped = true
			}
		}
		_, excused := uncappedMoney[name]
		switch {
		case !capped && !excused:
			t.Errorf("%s has no ceiling: add CHECK (%s <= %d) beside it in migrations/001 "+
				"(money.MaxCents, the ceiling every money column carries), or name it in "+
				"uncappedMoney with the reason", name, col.Name, int64(money.MaxCents))
		case capped && excused:
			t.Errorf("%s now has a ceiling: remove it from uncappedMoney", name)
		}
	}
	for name := range uncappedMoney {
		if !seen[name] {
			t.Errorf("uncappedMoney names %s, which the schema no longer has", name)
		}
	}
}

// TestAnOrderTotalCannotLeaveBigint reproduces the overflow: a delivery fee
// at bigint's limit, written with triggers off as a restore or a maintenance
// script would, made order_totals answer "bigint out of range" for that order
// and for every list that reads it. A CHECK still applies with triggers off.
func TestAnOrderTotalCannotLeaveBigint(t *testing.T) {
	expectRefusal(t, "a delivery fee at bigint's limit, triggers off", `
		SET LOCAL session_replication_role = replica;
		INSERT INTO orders (id, shipping_cents, shipping_version_id, shipping_method_code, shipping_method_name)
		VALUES ('11110001-0000-4000-8000-000000000001', 9223372036854775807,
		        'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ('11110001-0000-4000-8000-000000000001', 'OVERFLOW-1', 'Overflow', 100, 1);
		SELECT total_cents FROM order_totals WHERE order_id = '11110001-0000-4000-8000-000000000001'`,
		"23514", "orders_shipping_in_range")
}
