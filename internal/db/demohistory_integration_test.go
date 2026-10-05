//go:build integration

package db_test

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
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
	{"the history ends the day before it was generated", `
		SELECT order_number FROM orders
		WHERE shop_day(placed_at) >= shop_today() OR shop_day(placed_at) < shop_today() - 90`},
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
	windows := saleWindows(t, shop)

	if out, err := runDemoHistory(t, shop.Config().ConnString(), namingItself(shop)...); err != nil {
		t.Fatalf("seed/demo_history.sql: %v\n%s", err, out)
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

	assertRefused(t, shop, shop.Config().ConnString(), namingItself(shop),
		"this database already has a demo history (complete or partial); restore the snapshot to run again")
}

// The demo holds a few checkouts from before the history; it numbers its own
// orders of their day after them.
func TestDemoHistoryNumbersAfterADaysOrders(t *testing.T) {
	shop := dbtest.Pool(t)
	seedCatalogue(t, shop)
	addAdmin(t, shop)
	orderPaidYesterday("cs_test_a1before")(t, shop)
	const earlier = `
		SELECT (to_jsonb(o) || jsonb_build_object(
		           'payments', (SELECT jsonb_agg(to_jsonb(p)) FROM payments p WHERE p.order_id = o.id),
		           'lines', (SELECT jsonb_agg(to_jsonb(l)) FROM order_lines l WHERE l.order_id = o.id)))::text
		FROM orders o
		WHERE o.order_number = 'GO-' || to_char(shop_today() - 1, 'YYMMDD') || '-000001'`
	before := textRows(t, shop, earlier)

	if out, err := runDemoHistory(t, shop.Config().ConnString(), namingItself(shop)...); err != nil {
		t.Fatalf("seed/demo_history.sql beside GO-<yesterday>-000001: %v\n%s", err, out)
	}

	if after := textRows(t, shop, earlier); len(before) != 1 || !slices.Equal(after, before) {
		t.Errorf("the order placed before the history:\nbefore %v\nafter  %v", before, after)
	}
	if off := textRows(t, shop, `
		SELECT c.business_date::text
		FROM order_number_counters c
		WHERE c.last_no <> (SELECT count(*) FROM orders o
		                    WHERE o.order_number LIKE 'GO-' || to_char(c.business_date, 'YYMMDD') || '-%')`); len(off) > 0 {
		t.Errorf("days whose counter is not the number of orders numbered on them: %s", strings.Join(off, ", "))
	}
}

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
			prepare: []func(*testing.T, *pgxpool.Pool){seedCatalogue, addAdmin, orderPaidYesterday("cs_test_a1soft")},
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
			prepare: []func(*testing.T, *pgxpool.Pool){seedCatalogue, addAdmin, orderPaidYesterday("cs_live_a1paid")},
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
			assertRefused(t, shop, conn, args, tt.refusal)
		})
	}
}

// runDemoHistory runs the script with psql, as its header says, through conn.
func runDemoHistory(t *testing.T, conn string, args ...string) (string, error) {
	t.Helper()
	psql, err := exec.LookPath("psql")
	if err != nil {
		t.Fatalf("seed/demo_history.sql is a psql script: %v", err)
	}
	args = append([]string{"-X", "-q", "-v", "ON_ERROR_STOP=1", "-d", conn}, args...)
	args = append(args, "-f", filepath.Join("..", "..", "seed", "demo_history.sql"))
	//nolint:gosec // G204: psql comes from exec.LookPath and every argument is the test's own.
	out, err := exec.CommandContext(t.Context(), psql, args...).CombinedOutput()
	return string(out), err
}

// namingItself is the opt-in the script asks for: demo_database naming the
// database it writes into.
func namingItself(shop *pgxpool.Pool) []string {
	return []string{"-v", "demo_database=" + shop.Config().ConnConfig.Database}
}

// assertRefused wants the script stopped before it writes: a non-zero exit,
// the refusal in what it printed, and no order added.
func assertRefused(t *testing.T, shop *pgxpool.Pool, conn string, args []string, refusal string) {
	t.Helper()
	before := countOrders(t, shop)
	out, err := runDemoHistory(t, conn, args...)
	switch _, exited := errors.AsType[*exec.ExitError](err); {
	case !exited:
		t.Errorf("seed/demo_history.sql ran (%v), want it to refuse with %q:\n%s", err, refusal, out)
	case !strings.Contains(out, refusal):
		t.Errorf("seed/demo_history.sql stopped (%v) without saying %q:\n%s", err, refusal, out)
	}
	if after := countOrders(t, shop); after != before {
		t.Errorf("refused run left %d orders, want the %d it found", after, before)
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

// orderPaidYesterday places the first order of yesterday, numbered through the
// day's counter as next_order_number() numbered it then, and paid through ref.
func orderPaidYesterday(ref string) func(*testing.T, *pgxpool.Pool) {
	return func(t *testing.T, shop *pgxpool.Pool) {
		t.Helper()
		ctx := t.Context()
		tx, err := shop.Begin(ctx)
		if err != nil {
			t.Fatalf("begin yesterday's order: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		var order string
		if err = tx.QueryRow(ctx, `
			WITH counter AS (
			    INSERT INTO order_number_counters (business_date, last_no) VALUES (shop_today() - 1, 1)
			    RETURNING business_date, last_no
			)
			INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name,
			                    shipping_cents, placed_at)
			SELECT 'GO-' || to_char(c.business_date, 'YYMMDD') || '-' || to_char(c.last_no, 'FM000000'),
			       sv.id, sm.code, sv.name, sv.fee_cents, now() - interval '1 day'
			FROM counter c
			CROSS JOIN shipping_method_versions sv
			JOIN shipping_methods sm ON sm.id = sv.method_id
			WHERE sm.code = 'home_delivery'
			ORDER BY sv.effective_at DESC
			LIMIT 1
			RETURNING id::text`).Scan(&order); err != nil {
			t.Fatalf("place yesterday's order: %v", err)
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO order_lines (order_id, product_id, variant_id, sku, product_name, unit_price_cents, quantity, position)
			SELECT $1::uuid, p.id, pv.id, pv.sku, p.name, pv.price_cents, 1, 0
			FROM product_variants pv
			JOIN products p ON p.id = pv.product_id
			WHERE p.status = 'active' AND pv.is_active
			ORDER BY pv.price_cents, pv.sku
			LIMIT 1`, order); err != nil {
			t.Fatalf("add a line to yesterday's order: %v", err)
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
			VALUES ($1::uuid, 'early@goen.invalid', '早鳥', '0912000000', '106', '台北市', '大安區', '復興南路一段 1 號')`,
			order); err != nil {
			t.Fatalf("address yesterday's order: %v", err)
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents, captured_amount_cents,
			                      card_brand, card_last4, paid_at)
			SELECT $1::uuid, $2, 'succeeded', order_amount_after_credit($1::uuid), order_amount_after_credit($1::uuid),
			       'visa', '4242', now() - interval '1 day'`, order, ref); err != nil {
			t.Fatalf("pay yesterday's order through %s: %v", ref, err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatalf("commit yesterday's order: %v", err)
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

func countOrders(t *testing.T, shop *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := shop.QueryRow(t.Context(), `SELECT count(*) FROM orders`).Scan(&n); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	return n
}

func textRows(t *testing.T, shop *pgxpool.Pool, query string) []string {
	t.Helper()
	rows, err := shop.Query(t.Context(), query)
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
		worker.ExpiredSessions != 0 || worker.UnreferencedMedia != 0 || worker.UnreconciledPayments != 0 ||
		!worker.CopurchaseEverBuilt || worker.CopurchaseAgeSeconds > 600 {
		t.Errorf("WorkerHealth = %+v, want every count 0 and co-purchases just rebuilt", worker)
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
	stranded, err := q.StrandedInvoiceClaims(ctx)
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
	for _, days := range []int32{7, 30, 90} {
		revenue, err := q.RevenueSince(ctx, days)
		if err != nil {
			t.Fatalf("RevenueSince(%d): %v", days, err)
		}
		best, err := q.BestSellersSince(ctx, db.BestSellersSinceParams{WindowDays: days, LimitTo: 10})
		if err != nil {
			t.Fatalf("BestSellersSince(%d): %v", days, err)
		}
		completion, err := q.CheckoutCompletionSince(ctx, days)
		if err != nil {
			t.Fatalf("CheckoutCompletionSince(%d): %v", days, err)
		}
		risk, err := q.StockAtRisk(ctx, db.StockAtRiskParams{WindowDays: days, LimitTo: 10})
		if err != nil {
			t.Fatalf("StockAtRisk(%d): %v", days, err)
		}
		if revenue.Orders == 0 || revenue.RevenueCents == 0 || len(best) == 0 || completion.Committed == 0 || len(risk) == 0 {
			t.Errorf("last %d days: %d orders, %d revenue, %d best sellers, %d committed, %d stock rows; want data in each",
				days, revenue.Orders, revenue.RevenueCents, len(best), completion.Committed, len(risk))
		}
	}
}
