//go:build integration

package db_test

import (
	"os"
	"os/exec"
	"path/filepath"
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
	if _, err := shop.Exec(ctx, `SELECT upsert_staff('owner@goen.invalid', '店主', 'admin')`); err != nil {
		t.Fatalf("create the admin the history acts as: %v", err)
	}
	windows := saleWindows(t, shop)

	if out, err := runDemoHistory(t, shop); err != nil {
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
}

func TestDemoHistoryRefusesADatabaseWithOtherPayments(t *testing.T) {
	shop := dbtest.Pool(t)
	ctx := t.Context()
	// fixtures carry pi_fixture, a payment that is neither cs_test_ nor cs_demo_.
	if _, err := shop.Exec(ctx, fixtures); err != nil {
		t.Fatalf("load fixtures: %v", err)
	}
	before := countOrders(t, shop)

	out, err := runDemoHistory(t, shop)
	if err == nil {
		t.Fatalf("seed/demo_history.sql ran beside payment pi_fixture:\n%s", out)
	}
	if !strings.Contains(out, "pi_fixture") {
		t.Errorf("seed/demo_history.sql stopped without naming pi_fixture:\n%s", out)
	}
	if after := countOrders(t, shop); after != before {
		t.Errorf("refused run left %d orders, want the %d it found", after, before)
	}
}

func runDemoHistory(t *testing.T, shop *pgxpool.Pool) (string, error) {
	t.Helper()
	psql, err := exec.LookPath("psql")
	if err != nil {
		t.Fatalf("seed/demo_history.sql is a psql script: %v", err)
	}
	//nolint:gosec // G204: psql comes from exec.LookPath and every argument is the test's own.
	cmd := exec.CommandContext(t.Context(), psql, "-X", "-q", "-v", "ON_ERROR_STOP=1",
		"-d", shop.Config().ConnString(), "-f", filepath.Join("..", "..", "seed", "demo_history.sql"))
	out, err := cmd.CombinedOutput()
	return string(out), err
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
