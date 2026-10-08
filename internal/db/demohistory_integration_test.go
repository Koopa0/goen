//go:build integration

package db_test

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/shoptime"
)

// demoHistoryInvariants are the shop's own rules over what seed/demo_history.sql
// wrote. Each query names the orders or SKUs that break its rule.
var demoHistoryInvariants = []struct {
	rule  string
	query string
}{
	{"a committed order's money matches its lines", `
		SELECT o.order_number
		FROM orders o
		JOIN committed_orders c ON c.id = o.id
		WHERE (SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol WHERE ol.order_id = o.id)
		      - o.discount_cents + o.shipping_cents + o.tax_cents
		   <> coalesce((SELECT p.captured_amount_cents FROM payments p
		                WHERE p.order_id = o.id AND p.status = 'succeeded'), 0)
		      - coalesce((SELECT sum(e.amount_cents) FROM store_credit_entries e
		                  WHERE e.idempotency_key = 'order:' || o.id), 0)`},
	{"a committed order has one issued invoice for its total", `
		SELECT o.order_number
		FROM orders o
		JOIN committed_orders c ON c.id = o.id
		WHERE (SELECT count(*) FROM invoice_documents d
		       WHERE d.order_id = o.id AND d.kind = 'invoice' AND d.status = 'issued'
		         AND d.amount_cents = ((SELECT sum(ol.unit_price_cents * ol.quantity)
		                                FROM order_lines ol WHERE ol.order_id = o.id)
		                               - o.discount_cents + o.shipping_cents + o.tax_cents) / 100 * 100) <> 1`},
	{"an order that left is shipped in full and holds nothing", `
		SELECT o.order_number
		FROM orders o
		WHERE o.fulfillment_status IN ('shipped', 'delivered', 'completed')
		  AND (EXISTS (SELECT 1 FROM order_lines ol
		               WHERE ol.order_id = o.id
		                 AND ol.quantity <> coalesce((SELECT sum(sl.quantity) FROM order_shipment_lines sl
		                                              WHERE sl.order_line_id = ol.id), 0))
		       OR EXISTS (SELECT 1 FROM inventory_reservations r
		                  WHERE r.order_id = o.id AND r.state = 'held'))`},
	{"a cancelled order took no money, holds no stock and has no invoice", `
		SELECT o.order_number
		FROM orders o
		WHERE o.fulfillment_status = 'cancelled'
		  AND (EXISTS (SELECT 1 FROM payments p WHERE p.order_id = o.id AND p.status = 'succeeded')
		       OR EXISTS (SELECT 1 FROM inventory_reservations r WHERE r.order_id = o.id AND r.state = 'held')
		       OR EXISTS (SELECT 1 FROM invoice_documents d WHERE d.order_id = o.id))`},
	{"no unpaid order is left open", `
		SELECT o.order_number
		FROM orders o
		WHERE o.fulfillment_status = 'pending'
		  AND NOT order_is_committed(o.id) AND order_amount_after_credit(o.id) <> 0`},
	{"an order's timeline starts when it was placed and runs forward", `
		SELECT o.order_number
		FROM orders o
		WHERE NOT EXISTS (SELECT 1 FROM order_events e
		                  WHERE e.order_id = o.id AND e.kind = 'placed' AND e.occurred_at = o.placed_at)
		   OR EXISTS (SELECT 1
		              FROM order_events a
		              JOIN order_events b ON b.order_id = a.order_id
		              WHERE a.order_id = o.id
		                AND array_position(ARRAY['placed', 'paid', 'picking', 'shipped', 'delivered', 'completed'], a.kind)
		                  < array_position(ARRAY['placed', 'paid', 'picking', 'shipped', 'delivered', 'completed'], b.kind)
		                AND a.occurred_at > b.occurred_at)`},
	{"orders were written in the order they were placed", `
		SELECT order_number
		FROM (SELECT order_number, placed_at < lag(placed_at) OVER (ORDER BY id) AS earlier FROM orders) s
		WHERE earlier`},
	{"stock is never negative and is what its ledger adds up to", `
		SELECT pv.sku
		FROM product_variants pv
		WHERE pv.stock_quantity < 0
		   OR pv.stock_quantity <> coalesce((SELECT sum(m.delta) FROM inventory_movements m
		                                     WHERE m.variant_id = pv.id), 0)`},
	{"the ledger read by date never goes negative and dates follow the ledger's order", `
		SELECT DISTINCT sku
		FROM (SELECT pv.sku,
		             sum(m.delta) OVER (PARTITION BY m.variant_id ORDER BY m.created_at, m.id) AS running,
		             m.created_at < lag(m.created_at) OVER (PARTITION BY m.variant_id ORDER BY m.id) AS earlier
		      FROM inventory_movements m
		      JOIN product_variants pv ON pv.id = m.variant_id) s
		WHERE running < 0 OR earlier`},
	{"points earned expire a year after the day they were earned", `
		SELECT id::text FROM loyalty_entries
		WHERE kind = 'award' AND expires_on <> shop_day(created_at) + 365`},
	{"an entry against a lot carries the lot's expiry", `
		SELECT e.id::text
		FROM loyalty_entries e
		JOIN loyalty_entries l ON l.id = e.lot_id
		WHERE e.expires_on <> l.expires_on`},
	{"a card refund is no more than the payment it refunds", `
		SELECT o.order_number
		FROM orders o
		JOIN payments p ON p.order_id = o.id
		WHERE p.status = 'succeeded'
		  AND (SELECT coalesce(sum(r.amount_cents), 0) FROM refunds r
		       WHERE r.payment_id = p.id AND r.status = 'succeeded') > p.captured_amount_cents`},
	{"credit given back on an order is no more than the credit spent on it", `
		SELECT o.order_number
		FROM orders o
		WHERE (SELECT coalesce(sum(e.amount_cents), 0) FROM store_credit_entries e
		       WHERE e.order_id = o.id AND e.amount_cents > 0)
		    > -(SELECT coalesce(sum(e.amount_cents), 0) FROM store_credit_entries e
		        WHERE e.idempotency_key = 'order:' || o.id)`},
	// A paid order keeps its hold until dispatch consumes it, and the history
	// leaves its last orders waiting to be sent.
	{"stock is held only for an order paid for and waiting to be sent", `
		SELECT DISTINCT o.order_number
		FROM orders o
		JOIN inventory_reservations r ON r.order_id = o.id
		WHERE r.state = 'held'
		  AND NOT (o.fulfillment_status IN ('pending', 'picking')
		           AND (order_is_committed(o.id) OR order_amount_after_credit(o.id) = 0))`},
	{"every payment is a demo payment", `
		SELECT provider_ref FROM payments WHERE provider_ref NOT LIKE 'cs\_demo\_%'`},
}

// The history commits thousands of transactions, so it gets a database of its
// own rather than the suite's shared one.
func TestDemoHistoryKeepsTheShopsRules(t *testing.T) {
	shop := dbtest.Pool(t)
	ctx := t.Context()
	seedCatalogue(t, shop)
	addAdmin(t, shop)
	// The seed and the history are separate runs, with the second factor
	// enrolled in between, so they need not fall on the same day.
	ageSnapshot(t, shop, 1)
	windows := saleWindows(t, shop)

	// The history can end after the shop's midnight; every day below is the
	// one it started on.
	ran := shoptime.In(time.Now())
	if out, err := runSeed(t, "demo_history.sql", shop.Config().ConnString(), namingItself(shop)...); err != nil {
		t.Fatalf("seed/demo_history.sql: %v\n%s", err, out)
	}
	if off := textRows(t, shop, `
		SELECT order_number FROM orders
		WHERE shop_day(placed_at) >= $1::date OR shop_day(placed_at) < $1::date - 90`, shoptime.Day(ran)); len(off) > 0 {
		t.Errorf("the history ends the day before it was generated (%s): broken by %d, e.g. %s",
			shoptime.Day(ran), len(off), strings.Join(off[:min(len(off), 5)], ", "))
	}

	for _, inv := range demoHistoryInvariants {
		if found := textRows(t, shop, inv.query); len(found) > 0 {
			t.Errorf("%s: broken by %d, e.g. %s", inv.rule, len(found), strings.Join(found[:min(len(found), 5)], ", "))
		}
	}

	var orders, cancelled, guests, returns, creditSpends, distinctTimes, hours int
	if err := shop.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE fulfillment_status = 'cancelled'),
		       count(*) FILTER (WHERE user_id IS NULL),
		       (SELECT count(*) FROM return_requests WHERE status = 'completed'),
		       (SELECT count(*) FROM store_credit_entries WHERE amount_cents < 0 AND order_id IS NOT NULL),
		       count(DISTINCT placed_at),
		       count(DISTINCT extract(hour FROM placed_at AT TIME ZONE 'Asia/Taipei'))
		FROM orders`).Scan(&orders, &cancelled, &guests, &returns, &creditSpends, &distinctTimes, &hours); err != nil {
		t.Fatalf("count the history: %v", err)
	}
	if orders < 300 || cancelled == 0 || guests == 0 || returns == 0 || creditSpends == 0 {
		t.Errorf("history has %d orders, %d cancelled, %d by guests, %d returns completed, %d paid with credit; want ~450 and some of each",
			orders, cancelled, guests, returns, creditSpends)
	}
	if distinctTimes != orders || hours < 12 {
		t.Errorf("%d orders at %d distinct moments over %d hours of the day; want each at its own time, spread over the day",
			orders, distinctTimes, hours)
	}

	if got := saleWindows(t, shop); got != windows {
		t.Errorf("campaign and coupon windows moved:\nbefore %s\nafter  %s", windows, got)
	}

	assertNothingInTheFuture(t, shop)
	assertHealthQuiet(t, shop)
	assertReportsHaveData(t, shop)

	assertRefused(t, shop, "demo_history.sql", shop.Config().ConnString(), namingItself(shop),
		"this database already has a demo history (complete or partial); restore the snapshot to run again")

	t.Run("restored a week later", func(t *testing.T) { assertShiftedToToday(t, shop, ran, ran, 7) })
}

// The demo holds a few checkouts from before the history; it numbers its own
// orders of their day after them. The plan can leave a day without orders, so
// each of the last seven days has one from before.
func TestDemoHistoryNumbersAfterADaysOrders(t *testing.T) {
	shop := dbtest.Pool(t)
	seedCatalogue(t, shop)
	addAdmin(t, shop)
	for days := 1; days <= 7; days++ {
		orderPaidDaysAgo(fmt.Sprintf("cs_test_a1before%d", days), days)(t, shop)
	}
	const earlier = `
		SELECT (to_jsonb(o) || jsonb_build_object(
		           'payments', (SELECT jsonb_agg(to_jsonb(p)) FROM payments p WHERE p.order_id = o.id),
		           'lines', (SELECT jsonb_agg(to_jsonb(l)) FROM order_lines l WHERE l.order_id = o.id)))::text
		FROM orders o
		WHERE EXISTS (SELECT 1 FROM payments p WHERE p.order_id = o.id AND p.provider_ref LIKE 'cs\_test\_%')
		ORDER BY o.order_number`
	before := textRows(t, shop, earlier)

	if out, err := runSeed(t, "demo_history.sql", shop.Config().ConnString(), namingItself(shop)...); err != nil {
		t.Fatalf("seed/demo_history.sql beside GO-<day>-000001 on each of the last seven days: %v\n%s", err, out)
	}

	if after := textRows(t, shop, earlier); len(before) != 7 || !slices.Equal(after, before) {
		t.Errorf("the orders placed before the history:\nbefore %v\nafter  %v", before, after)
	}
	if off := textRows(t, shop, `
		SELECT c.business_date::text
		FROM order_number_counters c
		WHERE c.last_no <> (SELECT count(*) FROM orders o
		                    WHERE o.order_number LIKE 'GO-' || to_char(c.business_date, 'YYMMDD') || '-%')`); len(off) > 0 {
		t.Errorf("days whose counter is not the number of orders numbered on them: %s", strings.Join(off, ", "))
	}
	if shared := textRows(t, shop, `
		SELECT business_date::text FROM order_number_counters
		WHERE business_date >= shop_today() - 7 AND last_no > 1`); len(shared) == 0 {
		t.Fatal("no order drawn on any of the last seven days: the history numbered nothing after an earlier order")
	}
}

// seed/demo_backdating.sql's statements run in replica mode, with foreign keys
// and the append-only triggers off, so they may move times and nothing else:
// every column they set is a timestamptz, but for a points lot's expiry, the
// one date counted from the day it was written.
func TestDemoBackdatingSetsOnlyTimes(t *testing.T) {
	shop := dbtest.Pool(t)
	ctx := t.Context()
	script, err := os.ReadFile(filepath.Join("..", "..", "seed", "demo_backdating.sql"))
	if err != nil {
		t.Fatalf("read seed/demo_backdating.sql: %v", err)
	}
	// The statements are a temporary table, read on the session that made it.
	conn, err := shop.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire a connection: %v", err)
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, string(script)); err != nil {
		t.Fatalf("run seed/demo_backdating.sql: %v", err)
	}
	rows, err := conn.Query(ctx, `SELECT stmt FROM pg_temp.demo_backdating`)
	if err != nil {
		t.Fatalf("read the back-dating statements: %v", err)
	}
	stmts, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read the back-dating statements: %v", err)
	}
	if len(stmts) == 0 {
		t.Fatal("seed/demo_backdating.sql generated no statement")
	}

	for _, stmt := range stmts {
		update := backdatingUpdate.FindStringSubmatch(stmt)
		if update == nil {
			t.Errorf("not an UPDATE ... SET ... WHERE: %s", stmt)
			continue
		}
		table := update[1]
		targets := backdatingTarget.FindAllStringSubmatch(update[2], -1)
		if len(targets) == 0 {
			t.Errorf("no column found in the SET list of %s", stmt)
		}
		for _, target := range targets {
			column := target[1]
			var typ string
			if err = conn.QueryRow(ctx, `
				SELECT a.atttypid::regtype::text
				FROM pg_attribute a
				WHERE a.attrelid = $1::text::regclass AND quote_ident(a.attname) = $2
				  AND a.attnum > 0 AND NOT a.attisdropped`, table, column).Scan(&typ); err != nil {
				t.Errorf("type of %s.%s: %v", table, column, err)
				continue
			}
			if typ != "timestamp with time zone" && (table != "loyalty_entries" || column != "expires_on") {
				t.Errorf("%s.%s is %s: back-dating may set timestamptz columns and loyalty_entries.expires_on only",
					table, column, typ)
			}
		}
	}
}

// backdatingUpdate splits a back-dating statement into its table and its SET
// list; backdatingTarget finds each column the list assigns, bare or quoted as
// format's %I writes it.
var (
	backdatingUpdate = regexp.MustCompile(`^UPDATE (\S+) SET (.+) WHERE `)
	backdatingTarget = regexp.MustCompile(`(?:^|, )("(?:[^"]|"")+"|[a-z_][a-z0-9_$]*) = `)
)

func TestDemoHistoryRefuses(t *testing.T) {
	t.Parallel()
	itself := func(own string) string { return own }
	tests := []struct {
		name    string
		prepare []func(*testing.T, *pgxpool.Pool)
		named   func(own string) string // what the run passes as demo_database, given the database's name; nil passes nothing
		clerk   bool                    // the run logs in as a role that is not a superuser
		refusal string
	}{
		{
			name:    "an empty payments table without demo_database",
			prepare: []func(*testing.T, *pgxpool.Pool){seedCatalogue, addAdmin},
			refusal: "pass -v demo_database=<this database's name> to write a demo history into it",
		},
		{
			name:    "Stripe test payments without demo_database",
			prepare: []func(*testing.T, *pgxpool.Pool){seedCatalogue, addAdmin, orderPaidDaysAgo("cs_test_a1soft", 1)},
			refusal: "pass -v demo_database=<this database's name> to write a demo history into it",
		},
		{
			name:    "a demo_database naming another database",
			prepare: []func(*testing.T, *pgxpool.Pool){seedCatalogue, addAdmin},
			named:   func(string) string { return "goen" },
			refusal: "demo_database is goen, not this database",
		},
		{
			name:    "a role that is not a superuser",
			prepare: []func(*testing.T, *pgxpool.Pool){seedCatalogue, addAdmin},
			named:   itself,
			clerk:   true,
			refusal: "run this as a superuser",
		},
		{
			name:    "a live payment",
			prepare: []func(*testing.T, *pgxpool.Pool){seedCatalogue, addAdmin, orderPaidDaysAgo("cs_live_a1paid", 1)},
			named:   itself,
			refusal: "payment cs_live_a1paid is not a demo or test payment",
		},
		{
			name:    "a catalogue the seed did not build",
			prepare: []func(*testing.T, *pgxpool.Pool){addAdmin},
			named:   itself,
			refusal: "no opening stock from seed/dev_catalog.sql",
		},
		{
			name:    "no admin account",
			prepare: []func(*testing.T, *pgxpool.Pool){seedCatalogue},
			named:   itself,
			refusal: "the back-office steps need an admin account",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			shop := dbtest.Pool(t)
			for _, prepare := range tt.prepare {
				prepare(t, shop)
			}
			conn := shop.Config().ConnString()
			if tt.clerk {
				conn = clerkConnString(t, shop)
			}
			var args []string
			if tt.named != nil {
				args = []string{"-v", "demo_database=" + tt.named(shop.Config().ConnConfig.Database)}
			}
			assertRefused(t, shop, "demo_history.sql", conn, args, tt.refusal)
		})
	}
}

// runSeed runs seed/<script> with psql, as its header says, through conn.
func runSeed(t *testing.T, script, conn string, args ...string) (string, error) {
	t.Helper()
	psql, err := exec.LookPath("psql")
	if err != nil {
		t.Fatalf("seed/%s is a psql script: %v", script, err)
	}
	args = append([]string{"-X", "-q", "-v", "ON_ERROR_STOP=1", "-d", conn}, args...)
	args = append(args, "-f", filepath.Join("..", "..", "seed", script))
	//nolint:gosec // G204: psql comes from exec.LookPath and every argument is the test's own.
	out, err := exec.CommandContext(t.Context(), psql, args...).CombinedOutput()
	return string(out), err
}

// namingItself is the opt-in the script asks for: demo_database naming the
// database it writes into.
func namingItself(shop *pgxpool.Pool) []string {
	return []string{"-v", "demo_database=" + shop.Config().ConnConfig.Database}
}

// assertRefused wants seed/<script> stopped before it writes: a non-zero
// exit, the refusal in what it printed, and every table as it was.
func assertRefused(t *testing.T, shop *pgxpool.Pool, script, conn string, args []string, refusal string) {
	t.Helper()
	before := tableDigests(t, shop)
	out, err := runSeed(t, script, conn, args...)
	switch _, exited := errors.AsType[*exec.ExitError](err); {
	case !exited:
		t.Errorf("seed/%s ran (%v), want it to refuse with %q:\n%s", script, err, refusal, out)
	case !strings.Contains(out, refusal):
		t.Errorf("seed/%s stopped (%v) without saying %q:\n%s", script, err, refusal, out)
	}
	if changed := changedTables(before, tableDigests(t, shop)); len(changed) > 0 {
		t.Errorf("refused run of seed/%s changed %s", script, strings.Join(changed, ", "))
	}
}

func addAdmin(t *testing.T, shop *pgxpool.Pool) {
	t.Helper()
	if _, err := shop.Exec(t.Context(), `SELECT upsert_staff('owner@goen.invalid', '店主', 'admin')`); err != nil {
		t.Fatalf("create the admin the history acts as: %v", err)
	}
}

// clerkConnString creates a role that may log in but is not a superuser and
// returns the connection string that logs in as it.
func clerkConnString(t *testing.T, shop *pgxpool.Pool) string {
	t.Helper()
	if _, err := shop.Exec(t.Context(), `CREATE ROLE clerk LOGIN PASSWORD 'clerk'`); err != nil {
		t.Fatalf("create role clerk: %v", err)
	}
	conn, err := url.Parse(shop.Config().ConnString())
	if err != nil {
		t.Fatalf("parse %s: %v", shop.Config().ConnString(), err)
	}
	conn.User = url.UserPassword("clerk", "clerk")
	return conn.String()
}

// orderPaidDaysAgo places the first order of the day that many days ago,
// numbered through the day's counter as next_order_number() numbered it then,
// and paid through ref.
func orderPaidDaysAgo(ref string, days int) func(*testing.T, *pgxpool.Pool) {
	return func(t *testing.T, shop *pgxpool.Pool) {
		t.Helper()
		ctx := t.Context()
		tx, err := shop.Begin(ctx)
		if err != nil {
			t.Fatalf("begin the order of %d days ago: %v", days, err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		var order string
		if err = tx.QueryRow(ctx, `
			WITH counter AS (
			    INSERT INTO order_number_counters (business_date, last_no) VALUES (shop_today() - $1::integer, 1)
			    RETURNING business_date, last_no
			)
			INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name,
			                    shipping_cents, placed_at)
			SELECT 'GO-' || to_char(c.business_date, 'YYMMDD') || '-' || to_char(c.last_no, 'FM000000'),
			       sv.id, sm.code, sv.name, sv.fee_cents, now() - make_interval(days => $1::integer)
			FROM counter c
			CROSS JOIN shipping_method_versions sv
			JOIN shipping_methods sm ON sm.id = sv.method_id
			WHERE sm.code = 'home_delivery'
			ORDER BY sv.effective_at DESC
			LIMIT 1
			RETURNING id::text`, days).Scan(&order); err != nil {
			t.Fatalf("place the order of %d days ago: %v", days, err)
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO order_lines (order_id, product_id, variant_id, sku, product_name, unit_price_cents, quantity, position)
			SELECT $1::uuid, p.id, pv.id, pv.sku, p.name, pv.price_cents, 1, 0
			FROM product_variants pv
			JOIN products p ON p.id = pv.product_id
			WHERE p.status = 'active' AND pv.is_active
			ORDER BY pv.price_cents, pv.sku
			LIMIT 1`, order); err != nil {
			t.Fatalf("add a line to the order of %d days ago: %v", days, err)
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
			VALUES ($1::uuid, 'early@goen.invalid', '早鳥', '0912000000', '106', '台北市', '大安區', '復興南路一段 1 號')`,
			order); err != nil {
			t.Fatalf("address the order of %d days ago: %v", days, err)
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents, captured_amount_cents,
			                      card_brand, card_last4, paid_at)
			SELECT $1::uuid, $2, 'succeeded', order_amount_after_credit($1::uuid), order_amount_after_credit($1::uuid),
			       'visa', '4242', now() - make_interval(days => $3::integer)`, order, ref, days); err != nil {
			t.Fatalf("pay the order of %d days ago through %s: %v", days, ref, err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatalf("commit the order of %d days ago: %v", days, err)
		}
	}
}

func seedCatalogue(t *testing.T, shop *pgxpool.Pool) {
	t.Helper()
	seed, err := os.ReadFile(filepath.Join("..", "..", "seed", "dev_catalog.sql"))
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	if _, err := shop.Exec(t.Context(), string(seed)); err != nil {
		t.Fatalf("load seed: %v", err)
	}
}

func saleWindows(t *testing.T, shop *pgxpool.Pool) string {
	t.Helper()
	var windows string
	if err := shop.QueryRow(t.Context(), `
		SELECT coalesce(string_agg(w.id || ' ' || w.starts_at || ' ' || coalesce(w.ends_at::text, '-'), '; ' ORDER BY w.id), '')
		FROM (SELECT id, starts_at, ends_at FROM sale_campaigns
		      UNION ALL SELECT id, starts_at, ends_at FROM coupons) w`).Scan(&windows); err != nil {
		t.Fatalf("read campaign and coupon windows: %v", err)
	}
	return windows
}

func textRows(t *testing.T, shop *pgxpool.Pool, query string, args ...any) []string {
	t.Helper()
	rows, err := shop.Query(t.Context(), query, args...)
	if err != nil {
		t.Fatalf("query %s: %v", query, err)
	}
	found, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read %s: %v", query, err)
	}
	return found
}

// Expiries and end dates may lie ahead; nothing that happened may.
func assertNothingInTheFuture(t *testing.T, shop *pgxpool.Pool) {
	t.Helper()
	checks := textRows(t, shop, `
		SELECT format('SELECT %L FROM %s HAVING max(%I) > %s',
		              c.relname || '.' || a.attname, c.oid::regclass, a.attname,
		              CASE WHEN a.atttypid = 'date'::regtype THEN 'shop_today()' ELSE 'now()' END)
		FROM pg_class c
		JOIN pg_attribute a ON a.attrelid = c.oid
		WHERE c.relnamespace = 'public'::regnamespace AND c.relkind = 'r'
		  AND a.attnum > 0 AND NOT a.attisdropped
		  AND a.atttypid IN ('timestamptz'::regtype, 'date'::regtype)
		  AND a.attname NOT LIKE 'expires%' AND a.attname <> 'ends_at'
		ORDER BY 1`)
	ahead := make([]string, 0, len(checks))
	for _, check := range checks {
		ahead = append(ahead, textRows(t, shop, check)...)
	}
	if len(ahead) > 0 {
		t.Errorf("columns holding a time after now: %s", strings.Join(ahead, ", "))
	}
}

// Every alarm query behind /admin/health, with no allowance: the history
// leaves nothing for a person to do.
func assertHealthQuiet(t *testing.T, shop *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	q := db.New(shop)

	worker, err := q.WorkerHealth(ctx, 1)
	if err != nil {
		t.Fatalf("WorkerHealth: %v", err)
	}
	if worker.OutboxPending != 0 || worker.OutboxStuck != 0 || worker.ExpiredHolds != 0 ||
		worker.ExpiredSessions != 0 || worker.UnreferencedMedia != 0 ||
		!worker.CopurchaseEverBuilt || worker.CopurchaseAgeSeconds > 600 {
		t.Errorf("WorkerHealth = %+v, want every count 0 and co-purchases just rebuilt", worker)
	}
	unreconciledCount, err := q.UnreconciledPaymentCount(ctx)
	if err != nil {
		t.Fatalf("UnreconciledPaymentCount: %v", err)
	}
	if unreconciledCount != 0 {
		t.Errorf("UnreconciledPaymentCount = %d, want 0", unreconciledCount)
	}
	refunds, err := q.OpenRefundCount(ctx)
	if err != nil {
		t.Fatalf("OpenRefundCount: %v", err)
	}
	if refunds != 0 {
		t.Errorf("OpenRefundCount = %d, want 0", refunds)
	}
	events, err := q.UnreconciledPayments(ctx)
	if err != nil {
		t.Fatalf("UnreconciledPayments: %v", err)
	}
	complete, err := q.UnreconciledCompletePayments(ctx)
	if err != nil {
		t.Fatalf("UnreconciledCompletePayments: %v", err)
	}
	stranded, err := q.StrandedInvoiceClaims(ctx, true)
	if err != nil {
		t.Fatalf("StrandedInvoiceClaims: %v", err)
	}
	immediately := pgtype.Interval{Valid: true}
	uninvoiced, err := q.UninvoicedOrders(ctx, immediately)
	if err != nil {
		t.Fatalf("UninvoicedOrders: %v", err)
	}
	unvoided, err := q.CancelledOrderInvoices(ctx, immediately)
	if err != nil {
		t.Fatalf("CancelledOrderInvoices: %v", err)
	}
	if len(events)+len(complete)+len(stranded)+len(uninvoiced)+len(unvoided) > 0 {
		t.Errorf("health lists %d unreconciled events, %d complete payments, %d stranded claims, %d uninvoiced orders, %d live invoices of cancelled orders; want none",
			len(events), len(complete), len(stranded), len(uninvoiced), len(unvoided))
	}
}

func assertReportsHaveData(t *testing.T, shop *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	q := db.New(shop)
	now := time.Now()
	for _, days := range []int32{7, 30, 90} {
		// The report's period: the last days shop days, today included, ending now.
		from := shoptime.Midnight(now).AddDate(0, 0, 1-int(days))
		revenue, err := q.RevenueBetween(ctx, db.RevenueBetweenParams{FromAt: from, ToAt: now})
		if err != nil {
			t.Fatalf("RevenueBetween(%d days): %v", days, err)
		}
		best, err := q.BestSellersBetween(ctx, db.BestSellersBetweenParams{FromAt: from, ToAt: now, LimitTo: 10})
		if err != nil {
			t.Fatalf("BestSellersBetween(%d days): %v", days, err)
		}
		completion, err := q.CheckoutCompletionBetween(ctx, db.CheckoutCompletionBetweenParams{FromAt: from, ToAt: now})
		if err != nil {
			t.Fatalf("CheckoutCompletionBetween(%d days): %v", days, err)
		}
		risk, err := q.StockAtRisk(ctx, db.StockAtRiskParams{FromAt: from, ToAt: now})
		if err != nil {
			t.Fatalf("StockAtRisk(%d): %v", days, err)
		}
		if revenue.Orders == 0 || revenue.RevenueCents == 0 || len(best) == 0 || completion.Committed == 0 || len(risk) == 0 {
			t.Errorf("last %d days: %d orders, %d revenue, %d best sellers, %d committed, %d stock rows; want data in each",
				days, revenue.Orders, revenue.RevenueCents, len(best), completion.Committed, len(risk))
		}
	}
}
