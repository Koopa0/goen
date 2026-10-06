//go:build integration

package db_test

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/shoptime"
)

// demoShiftLeaves are the tables whose times bound a credential or a retention
// sweep. seed/demo_shift.sql leaves them on the real clock, so that nothing
// which expired comes back to life.
var demoShiftLeaves = []string{
	"sessions", "password_reset_tokens", "email_verifications", "newsletter_confirmations",
	"carts", "cart_items", "checkout_attempts", "order_access_grants",
}

// demoBooks is what a shift must leave as it was: the committed order count,
// every order's total and every SKU's stock.
const demoBooks = `
	SELECT 'committed orders ' || count(*) FROM committed_orders
	UNION ALL
	SELECT 'order ' || o.id || ' totals '
	       || (coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol WHERE ol.order_id = o.id), 0)
	           - o.discount_cents + o.shipping_cents + o.tax_cents)
	FROM orders o
	UNION ALL
	SELECT 'sku ' || sku || ' stock ' || stock_quantity FROM product_variants
	ORDER BY 1`

// assertShiftedToToday makes the database read as a snapshot taken days ago,
// then runs seed/demo_shift.sql with that day as the anchor. Every table but
// those the shift leaves must be back as it was, nothing may lie ahead of the
// clock but expiries and end dates, the books must read the same, and each
// order must carry the day it was placed in its number.
func assertShiftedToToday(t *testing.T, shop *pgxpool.Pool, days int) {
	t.Helper()
	conn := shop.Config().ConnString()
	made := tableDigests(t, shop)
	books := textRows(t, shop, demoBooks)

	if out, err := runSeed(t, "demo_shift.sql", conn, shiftArgs(shop, 0)...); err != nil {
		t.Fatalf("seed/demo_shift.sql on the anchor day: %v\n%s", err, out)
	}
	if changed := changedTables(made, tableDigests(t, shop)); len(changed) > 0 {
		t.Errorf("on the anchor day the shift changed %s, want nothing", strings.Join(changed, ", "))
	}

	ageSnapshot(t, shop, days)
	aged := tableDigests(t, shop)
	if aged["orders"] == made["orders"] {
		t.Fatalf("ageSnapshot(%d) left every order as it was", days)
	}
	if out, err := runSeed(t, "demo_shift.sql", conn, shiftArgs(shop, days)...); err != nil {
		t.Fatalf("seed/demo_shift.sql %d days after the snapshot: %v\n%s", days, err, out)
	}

	want := maps.Clone(made)
	for _, table := range demoShiftLeaves {
		want[table] = aged[table]
	}
	if changed := changedTables(want, tableDigests(t, shop)); len(changed) > 0 {
		t.Errorf("shifted %d days, these tables are not as they were before the snapshot aged: %s",
			days, strings.Join(changed, ", "))
	}
	assertNothingInTheFuture(t, shop)
	if after := textRows(t, shop, demoBooks); !slices.Equal(after, books) {
		var lost []string
		for _, line := range books {
			if !slices.Contains(after, line) {
				lost = append(lost, line)
			}
		}
		t.Errorf("the shift changed the books: %d lines before, %d after; gone: %s",
			len(books), len(after), strings.Join(lost[:min(len(lost), 5)], "; "))
	}
	if off := textRows(t, shop, `
		SELECT order_number FROM orders
		WHERE substr(order_number, 4, 6) <> to_char(shop_day(placed_at), 'YYMMDD')`); len(off) > 0 {
		t.Errorf("orders numbered for another day than the one they were placed on: %s",
			strings.Join(off[:min(len(off), 5)], ", "))
	}
}

// ageSnapshot makes the database read as if it had been made days earlier:
// every time column in public, every order number and every day's counter
// move back that many days. It reads the columns from the catalogue on its
// own, so a column the shift misses stays behind and shows.
func ageSnapshot(t *testing.T, shop *pgxpool.Pool, days int) {
	t.Helper()
	ctx := t.Context()
	tx, err := shop.Begin(ctx)
	if err != nil {
		t.Fatalf("begin ageing the snapshot: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT set_config('demo_test.days', $1::integer::text, true)`, days); err != nil {
		t.Fatalf("pass the days to age by: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		DO $$
		DECLARE
		    v_days constant integer := current_setting('demo_test.days')::integer;
		    v_stmt text;
		    r      record;
		BEGIN
		    SET LOCAL session_replication_role = replica;
		    SET LOCAL TimeZone = 'Asia/Taipei';
		    FOR v_stmt IN
		        SELECT format('UPDATE %s SET %s', c.oid::regclass,
		                      string_agg(format(CASE WHEN a.atttypid = 'date'::regtype THEN '%1$I = %1$I - %2$s'
		                                             ELSE '%1$I = %1$I - make_interval(days => %2$s)' END,
		                                        a.attname, v_days),
		                                 ', '))
		        FROM pg_class c
		        JOIN pg_attribute a ON a.attrelid = c.oid
		        WHERE c.relnamespace = 'public'::regnamespace AND c.relkind = 'r'
		          AND c.relname <> 'order_number_counters'
		          AND a.attnum > 0 AND NOT a.attisdropped
		          AND a.atttypid IN ('timestamptz'::regtype, 'timestamp'::regtype, 'date'::regtype)
		        GROUP BY c.oid
		    LOOP
		        EXECUTE v_stmt;
		    END LOOP;
		    FOR r IN SELECT business_date FROM order_number_counters ORDER BY business_date LOOP
		        UPDATE order_number_counters SET business_date = business_date - v_days
		        WHERE business_date = r.business_date;
		    END LOOP;
		    FOR r IN SELECT id FROM orders ORDER BY order_number LOOP
		        UPDATE orders
		        SET order_number = 'GO-' || to_char(to_date(substr(order_number, 4, 6), 'YYMMDD') - v_days, 'YYMMDD')
		                        || substr(order_number, 10)
		        WHERE id = r.id;
		    END LOOP;
		END
		$$`); err != nil {
		t.Fatalf("age the snapshot %d days: %v", days, err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit the aged snapshot: %v", err)
	}
}

// shiftArgs names the database and, as the anchor, the shop day days ago.
func shiftArgs(shop *pgxpool.Pool, days int) []string {
	anchor := shoptime.Day(shoptime.In(time.Now()).AddDate(0, 0, -days))
	return append(namingItself(shop), "-v", "anchor_day="+anchor)
}

// seed/demo_shift_columns.sql picks what seed/demo_shift.sql moves in replica
// mode, with foreign keys and the append-only triggers off: time columns only,
// none in a table whose times stay on the real clock, and none of the order
// number counters, which the script moves one day at a time.
func TestDemoShiftMovesOnlyTimes(t *testing.T) {
	t.Parallel()
	shop := dbtest.Pool(t)
	ctx := t.Context()
	script, err := os.ReadFile(filepath.Join("..", "..", "seed", "demo_shift_columns.sql"))
	if err != nil {
		t.Fatalf("read seed/demo_shift_columns.sql: %v", err)
	}
	// The columns are a temporary table, read on the session that made it.
	conn, err := shop.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire a connection: %v", err)
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, string(script)); err != nil {
		t.Fatalf("run seed/demo_shift_columns.sql: %v", err)
	}
	rows, err := conn.Query(ctx, `SELECT relid::text, attname::text, typ::text FROM pg_temp.demo_time_column`)
	if err != nil {
		t.Fatalf("read the columns the shift moves: %v", err)
	}
	type column struct{ Table, Name, Type string }
	columns, err := pgx.CollectRows(rows, pgx.RowToStructByPos[column])
	if err != nil {
		t.Fatalf("read the columns the shift moves: %v", err)
	}
	if len(columns) == 0 {
		t.Fatal("seed/demo_shift_columns.sql selected no column")
	}

	times := []string{"timestamp with time zone", "timestamp without time zone", "date"}
	for _, c := range columns {
		if !slices.Contains(times, c.Type) {
			t.Errorf("%s.%s is %s: the shift may move time columns only", c.Table, c.Name, c.Type)
		}
		if slices.Contains(demoShiftLeaves, c.Table) {
			t.Errorf("%s.%s is selected: the shift must leave %s on the real clock", c.Table, c.Name, c.Table)
		}
		if c.Table == "order_number_counters" {
			t.Errorf("order_number_counters.%s is selected: one UPDATE moving every day collides with the next day's key", c.Name)
		}
	}
}

func TestDemoShiftRefuses(t *testing.T) {
	t.Parallel()
	itself := func(own string) string { return own }
	today := shoptime.In(time.Now())
	yesterday, tomorrow := shoptime.Day(today.AddDate(0, 0, -1)), shoptime.Day(today.AddDate(0, 0, 1))
	tests := []struct {
		name    string
		prepare []func(*testing.T, *pgxpool.Pool)
		named   func(own string) string // what the run passes as demo_database, given the database's name; nil passes nothing
		anchor  string                  // what the run passes as anchor_day; empty passes nothing
		refusal string
	}{
		{
			name:    "no demo_database",
			prepare: []func(*testing.T, *pgxpool.Pool){seedCatalogue, orderPaidDaysAgo("cs_test_a1before", 2)},
			anchor:  yesterday,
			refusal: "pass -v demo_database=<this database's name> to shift it",
		},
		{
			name:    "a demo_database naming another database",
			prepare: []func(*testing.T, *pgxpool.Pool){seedCatalogue, orderPaidDaysAgo("cs_test_a1before", 2)},
			named:   func(string) string { return "goen" },
			anchor:  yesterday,
			refusal: "demo_database is goen, not this database",
		},
		{
			name:    "no anchor_day",
			prepare: []func(*testing.T, *pgxpool.Pool){seedCatalogue, orderPaidDaysAgo("cs_test_a1before", 2)},
			named:   itself,
			refusal: "pass -v anchor_day=<the day the snapshot was taken, YYYY-MM-DD>",
		},
		{
			name:    "an anchor_day not written YYYY-MM-DD",
			prepare: []func(*testing.T, *pgxpool.Pool){seedCatalogue, orderPaidDaysAgo("cs_test_a1before", 2)},
			named:   itself,
			anchor:  "10/05/2026",
			refusal: "anchor_day is 10/05/2026, not a day written YYYY-MM-DD",
		},
		{
			name:    "an anchor_day after today",
			prepare: []func(*testing.T, *pgxpool.Pool){seedCatalogue, orderPaidDaysAgo("cs_test_a1before", 2)},
			named:   itself,
			anchor:  tomorrow,
			refusal: "anchor_day " + tomorrow + " is after today",
		},
		{
			name: "a live session that was never paid",
			prepare: []func(*testing.T, *pgxpool.Pool){
				seedCatalogue, orderPaidDaysAgo("cs_test_a1before", 2), cancelledPayment("cs_live_a1lapsed"),
			},
			named:   itself,
			anchor:  yesterday,
			refusal: "payment cs_live_a1lapsed is not a demo or test payment",
		},
		{
			name:    "a succeeded payment that is neither a demo nor a test one",
			prepare: []func(*testing.T, *pgxpool.Pool){seedCatalogue, orderPaidDaysAgo("pi_a1paid", 2)},
			named:   itself,
			anchor:  yesterday,
			refusal: "payment pi_a1paid is not a demo or test payment",
		},
		{
			name:    "a catalogue the seed did not build",
			named:   itself,
			anchor:  yesterday,
			refusal: "no opening stock from seed/dev_catalog.sql",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			shop := dbtest.Pool(t)
			for _, prepare := range tt.prepare {
				prepare(t, shop)
			}
			var args []string
			if tt.named != nil {
				args = append(args, "-v", "demo_database="+tt.named(shop.Config().ConnConfig.Database))
			}
			if tt.anchor != "" {
				args = append(args, "-v", "anchor_day="+tt.anchor)
			}
			assertRefused(t, shop, "demo_shift.sql", shop.Config().ConnString(), args, tt.refusal)
		})
	}
}

// cancelledPayment gives the earliest order a payment through ref that was
// never taken.
func cancelledPayment(ref string) func(*testing.T, *pgxpool.Pool) {
	return func(t *testing.T, shop *pgxpool.Pool) {
		t.Helper()
		if _, err := shop.Exec(t.Context(), `
			INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents)
			SELECT id, $1, 'cancelled', order_amount_after_credit(id)
			FROM orders
			ORDER BY placed_at
			LIMIT 1`, ref); err != nil {
			t.Fatalf("add the cancelled payment %s: %v", ref, err)
		}
	}
}

// tableDigests digests each table in public row by row, so that comparing two
// readings names every table that changed in between.
func tableDigests(t *testing.T, shop *pgxpool.Pool) map[string]string {
	t.Helper()
	digests := make(map[string]string)
	for _, query := range textRows(t, shop, `
		SELECT format('SELECT %L, md5(coalesce(string_agg(t::text, '','' ORDER BY t::text), '''')) FROM %s t',
		              c.relname, c.oid::regclass)
		FROM pg_class c
		WHERE c.relnamespace = 'public'::regnamespace AND c.relkind = 'r'`) {
		var table, digest string
		if err := shop.QueryRow(t.Context(), query).Scan(&table, &digest); err != nil {
			t.Fatalf("digest %s: %v", query, err)
		}
		digests[table] = digest
	}
	return digests
}

// changedTables names, in order, the tables whose digest in got is not the
// one in want.
func changedTables(want, got map[string]string) []string {
	var changed []string
	for table, digest := range got {
		if want[table] != digest {
			changed = append(changed, table)
		}
	}
	slices.Sort(changed)
	return changed
}
