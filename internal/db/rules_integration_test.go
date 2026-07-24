//go:build integration

package db_test

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The rules in this file span rows, so a CHECK cannot express them and a
// per-row test cannot prove them. Each is enforced by a trigger that locks its
// aggregate root before it reads; the concurrency tests below are what
// distinguish that from a trigger that merely looks correct in isolation.

// TestEveryRuleTriggerIsExercised is the completeness gate for triggers, the
// same shape as the one for constraints: the list comes from the catalog, so a
// trigger added without a case fails the build.
func TestEveryRuleTriggerIsExercised(t *testing.T) {
	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT tg.tgname
		FROM pg_trigger tg
		JOIN pg_class t ON t.oid = tg.tgrelid
		WHERE NOT tg.tgisinternal
		  AND t.relnamespace = 'public'::regnamespace
		  -- set_updated_at is bookkeeping, not a rule; it has its own test below.
		  AND tg.tgname NOT LIKE '%_set_updated_at'
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("read triggers: %v", err)
	}
	defer rows.Close()

	covered := make(map[string]bool, len(ruleCases))
	for _, c := range ruleCases {
		covered[c.rule] = true
	}

	var missing []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d rule triggers have no case:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

// TestRulesReject requires each trigger to refuse its own violation, named.
func TestRulesReject(t *testing.T) {
	for _, c := range ruleCases {
		t.Run(c.rule, func(t *testing.T) {
			err := run(t, c.reject)
			if err == nil {
				t.Fatalf("the database accepted it; %s does not enforce this", c.rule)
			}
			code, name := constraintViolation(err)
			if name != c.rule {
				t.Fatalf("refused by %q (SQLSTATE %s), want %q: %v", name, code, c.rule, err)
			}
		})
	}
}

// TestRulesAccept runs the neighbouring legal operation.
func TestRulesAccept(t *testing.T) {
	for _, c := range ruleCases {
		if c.accept == "" {
			t.Run(c.rule, func(t *testing.T) { t.Skipf("no legal neighbour: %s", c.acceptNote) })
			continue
		}
		t.Run(c.rule, func(t *testing.T) {
			if err := run(t, c.accept); err != nil {
				t.Fatalf("the database refused a legal operation: %v", err)
			}
		})
	}
}

type ruleCase struct {
	rule       string
	reject     string
	accept     string
	acceptNote string
}

// ruleCases pairs each rule trigger with a violation and a legal neighbour.
var ruleCases = []ruleCase{
	{
		rule: "categories_acyclic",
		// A → B is fine; making B the parent of A closes the loop.
		reject: `UPDATE categories SET parent_id = '22222222-2222-4222-8222-222222222222'
		         WHERE slug = 'laptops';
		         UPDATE categories SET parent_id = '2222aaaa-2222-4222-8222-222222222222'
		         WHERE slug = 'phones';`,
		accept: `UPDATE categories SET parent_id = '22222222-2222-4222-8222-222222222222'
		         WHERE slug = 'laptops';`,
	},
	{
		rule:   "store_credit_never_negative",
		reject: creditEntry(-200000, "overspend"),
		accept: creditEntry(-50000, "within balance"),
	},
	{
		rule: "store_credit_entries_append_only",
		reject: `UPDATE store_credit_entries SET amount_cents = 999999
		         WHERE idempotency_key = 'fixture-grant';`,
		acceptNote: "the table admits nothing but inserts, which the guard above covers",
	},
	{
		rule: "inventory_movements_append_only",
		reject: `INSERT INTO inventory_movements (variant_id, delta, reason, idempotency_key)
		         VALUES ('44444444-4444-4444-8444-444444444444', 5, 'receipt', 'k1');
		         DELETE FROM inventory_movements WHERE idempotency_key = 'k1';`,
		acceptNote: "inserting is the only permitted operation and is exercised throughout",
	},
	{
		rule: "orders_legal_transition",
		// pending cannot jump to shipped: the picking step is where stock leaves.
		// The reject uses the unpaid order (the legal-transition check fires before
		// the funded check, so it still names this rule); the accept must use the
		// PAID order, since an unfunded order can no longer leave pending.
		reject: `UPDATE orders SET fulfillment_status = 'shipped'
		         WHERE order_number = 'GO-260721-000388';`,
		accept: `UPDATE orders SET fulfillment_status = 'picking'
		         WHERE order_number = 'GO-260721-000387';`,
	},
	{
		rule: "orders_have_lines",
		reject: `INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		         VALUES ('11110001-0000-4000-8000-000000000001', 'GO-260721-000999', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配');
		         SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
		accept: `INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		         VALUES ('11110001-0000-4000-8000-000000000001', 'GO-260721-000999', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配');
		         INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		         VALUES ('11110001-0000-4000-8000-000000000001', 'SKU-X', '商品', 100000, 1);
		         INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
		         VALUES ('11110001-0000-4000-8000-000000000001', 'x@example.com', '王小明', '0912345678', '110', '台北市', '信義區', '松高路 1 號');
		         SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		rule: "order_lines_frozen_once_paid",
		// The fixture order carries a succeeded payment.
		reject: `UPDATE order_lines SET unit_price_cents = 1
		         WHERE order_id = '66666666-6666-4666-8666-666666666666';`,
		// The unpaid order's lines are still editable.
		accept: `UPDATE order_lines SET unit_price_cents = 1
		         WHERE order_id = '6666aaaa-6666-4666-8666-666666666666';`,
	},
	{
		rule: "orders_money_frozen_once_paid",
		reject: `UPDATE orders SET discount_cents = 50000
		         WHERE id = '66666666-6666-4666-8666-666666666666';`,
		accept: `UPDATE orders SET discount_cents = 50000
		         WHERE id = '6666aaaa-6666-4666-8666-666666666666';`,
	},
	{
		rule: "return_within_purchase",
		// The line holds two units; asking for three is one too many.
		reject: `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		         VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660001-0000-4000-8000-000000000000', 3);`,
		accept: `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		         VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660001-0000-4000-8000-000000000000', 2);`,
	},
	{
		rule: "warranty_unit_within_purchase",
		reject: `INSERT INTO warranty_registrations (order_line_id, unit_no, expires_on)
		         VALUES ('66660001-0000-4000-8000-000000000000', 3, current_date + 730);`,
		// Two were bought, so unit 2 exists — the case the old one-row-per-line
		// design could not represent at all.
		accept: `INSERT INTO warranty_registrations (order_line_id, unit_no, expires_on)
		         VALUES ('66660001-0000-4000-8000-000000000000', 1, current_date + 730),
		                ('66660001-0000-4000-8000-000000000000', 2, current_date + 730);`,
	},
	{
		rule: "invoice_documents_only_void",
		reject: `UPDATE invoice_documents SET amount_cents = 1
		         WHERE id = '99990001-0000-4000-8000-000000000000';`,
		accept: `UPDATE invoice_documents SET status = 'voided', voided_at = now()
		         WHERE id = '99990001-0000-4000-8000-000000000000';`,
	},
	{
		rule: "payments_no_regression",
		// A late-arriving created event must not un-settle a capture.
		reject: `UPDATE payments SET status = 'processing'
		         WHERE id = '77770001-0000-4000-8000-000000000000';`,
		accept: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents)
		         VALUES ('11110002-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
		                 'pi_new', 'requires_payment', 3690000);
		         UPDATE payments SET status = 'processing'
		         WHERE id = '11110002-0000-4000-8000-000000000001';`,
	},
	{
		rule:   "refunds_within_capture",
		reject: refund("re_a", 4000000) + refund("re_b", 4000000),
		accept: refund("re_a", 3000000) + refund("re_b", 3000000),
	},
	{
		rule: "sale_campaign_needs_discount",
		// No variant of the fixture product is marked down.
		reject: `INSERT INTO sale_campaign_products (campaign_id, product_id)
		         VALUES ('aaaa1111-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333');`,
		accept: `UPDATE product_variants SET compare_at_price_cents = 3990000
		         WHERE id = '44444444-4444-4444-8444-444444444444';
		         INSERT INTO sale_campaign_products (campaign_id, product_id)
		         VALUES ('aaaa1111-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333');`,
	},
	{
		rule: "order_events_append_only",
		reject: `INSERT INTO order_events (id, order_id, kind)
		         VALUES ('11110003-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', 'placed');
		         UPDATE order_events SET kind = 'cancelled'
		         WHERE id = '11110003-0000-4000-8000-000000000001';`,
		acceptNote: "appending is the only permitted operation",
	},
	{
		rule: "audit_events_append_only",
		reject: `INSERT INTO audit_events (id, action, entity_table)
		         VALUES ('11110004-0000-4000-8000-000000000001', 'order.cancel', 'orders');
		         DELETE FROM audit_events WHERE id = '11110004-0000-4000-8000-000000000001';`,
		acceptNote: "appending is the only permitted operation",
	},
	{
		rule: "shipping_method_versions_append_only",
		reject: `UPDATE shipping_method_versions SET fee_cents = 1
		         WHERE id = 'ffff0002-0000-4000-8000-000000000000';`,
		accept: `INSERT INTO shipping_method_versions (method_id, name, fee_cents, effective_at)
		         VALUES ('ffff0001-0000-4000-8000-000000000000', '宅配到府(黑貓)', 10000, now() + interval '1 day');`,
	},
	{
		rule: "inventory_never_negative",
		// Fixture stock is 14, safety_stock 2, so a sale may take it to 2 but no
		// lower. -12 lands exactly on the floor; -13 breaks it.
		reject: `SELECT record_inventory_movement('44444444-4444-4444-8444-444444444444', -13, 'sale', 'k-over');`,
		accept: `SELECT record_inventory_movement('44444444-4444-4444-8444-444444444444', -12, 'sale', 'k-floor');`,
	},
	{
		rule: "payments_settled_is_history",
		// The fixture payment is succeeded; its captured amount is history.
		reject: `UPDATE payments SET captured_amount_cents = 1
		         WHERE id = '77770001-0000-4000-8000-000000000000';`,
		acceptNote: "a succeeded payment's amounts are frozen; there is no legal edit to them",
	},
	{
		rule: "refunds_no_regression",
		// A succeeded refund cannot be demoted to free its allowance.
		reject: `INSERT INTO refunds (id, payment_id, request_key, amount_cents, status, succeeded_at)
		         VALUES ('11110020-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
		                 'rk-regress', 100000, 'succeeded', now());
		         UPDATE refunds SET status = 'failed', succeeded_at = NULL, failed_at = now()
		         WHERE id = '11110020-0000-4000-8000-000000000001';`,
		accept: `INSERT INTO refunds (id, payment_id, request_key, amount_cents, status)
		         VALUES ('11110021-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
		                 'rk-pending', 100000, 'pending');
		         UPDATE refunds SET status = 'requires_action'
		         WHERE id = '11110021-0000-4000-8000-000000000001';`,
	},
	{
		rule: "refunds_settled_is_history",
		// A succeeded refund's amount is money that already moved; raising it
		// (still within capture, so refunds_guard would pass) misstates what was
		// returned. no_regression does not fire — the status is untouched, only
		// the amount — so this trigger is the one that must refuse it.
		reject: `INSERT INTO refunds (id, payment_id, request_key, amount_cents, status, succeeded_at)
		         VALUES ('11110023-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
		                 'rk-freeze', 50000, 'succeeded', now());
		         UPDATE refunds SET amount_cents = 60000
		         WHERE id = '11110023-0000-4000-8000-000000000001';`,
		// A pending refund is not yet history; its amount may still be corrected.
		accept: `INSERT INTO refunds (id, payment_id, request_key, amount_cents, status)
		         VALUES ('11110024-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
		                 'rk-freeze-ok', 40000, 'pending');
		         UPDATE refunds SET amount_cents = 45000
		         WHERE id = '11110024-0000-4000-8000-000000000001';`,
	},
	{
		rule: "payments_require_complete_order",
		// The unpaid fixture order has its line deleted, then a payment is taken.
		reject: `DELETE FROM order_lines WHERE order_id = '6666aaaa-6666-4666-8666-666666666666';
		         INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents, captured_amount_cents, paid_at)
		         VALUES ('6666aaaa-6666-4666-8666-666666666666', 'pi-empty', 'succeeded', 100000, 100000, now());`,
		accept: `INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents, captured_amount_cents, paid_at)
		         VALUES ('6666aaaa-6666-4666-8666-666666666666', 'pi-complete', 'succeeded', 3690000, 3690000, now());`,
	},
	{
		rule: "orders_start_pending",
		reject: `INSERT INTO orders (order_number, fulfillment_status, shipping_version_id, shipping_method_code, shipping_method_name)
		         VALUES ('GO-260721-000901', 'shipped', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');
		         SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
		accept: `INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		         VALUES ('11110022-0000-4000-8000-000000000001', 'GO-260721-000902', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');
		         INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		         VALUES ('11110022-0000-4000-8000-000000000001', 'X', '商品', 100000, 1);
		         INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
		         VALUES ('11110022-0000-4000-8000-000000000001', 'x@example.com', '王', '09', '110', '台北市', '信義區', '路 1 號');
		         SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		rule: "shipment_within_purchase",
		// The paid fixture order's line holds 2; shipping 3 is one too many.
		reject: `INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		         VALUES ('66666666-6666-4666-8666-666666666666', '66660002-0000-4000-8000-000000000000',
		                 '66660001-0000-4000-8000-000000000000', 3);`,
		accept: `INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		         VALUES ('66666666-6666-4666-8666-666666666666', '66660002-0000-4000-8000-000000000000',
		                 '66660001-0000-4000-8000-000000000000', 2);`,
	},
	{
		rule: "return_requests_legal_transition",
		reject: `UPDATE return_requests SET status = 'completed', decided_at = now()
		         WHERE id = '88880001-0000-4000-8000-000000000000';`,
		accept: `UPDATE return_requests SET status = 'approved', decided_at = now()
		         WHERE id = '88880001-0000-4000-8000-000000000000';`,
	},
	{
		rule: "orders_shipping_snapshot_matches",
		// The fixture version ffff0002 belongs to method home_delivery; an order
		// snapshotting a different code against it is the contradiction the FK
		// cannot catch. The neighbour carries the matching code.
		reject: `INSERT INTO orders (id, shipping_version_id, shipping_method_code, shipping_method_name)
		         VALUES ('11110030-0000-4000-8000-000000000001', 'ffff0002-0000-4000-8000-000000000000', 'store_pickup', '超商取貨');`,
		accept: `INSERT INTO orders (id, shipping_version_id, shipping_method_code, shipping_method_name)
		         VALUES ('11110030-0000-4000-8000-000000000001', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
	},
	{
		rule: "return_requests_start_requested",
		// Inserting straight into 'approved' skips the transition machine and its
		// quantity recount; only 'requested' is legal at birth.
		reject: `INSERT INTO return_requests (order_id, status, reason, decided_at)
		         VALUES ('66666666-6666-4666-8666-666666666666', 'approved', '退貨', now());`,
		accept: `INSERT INTO return_requests (order_id, reason)
		         VALUES ('66666666-6666-4666-8666-666666666666', '退貨');`,
	},
	{
		rule: "invoice_allowance_valid",
		// An allowance for more than the fixture invoice (6,788,000) was for.
		reject: `INSERT INTO invoice_documents (order_id, kind, original_id, number, amount_cents)
		         VALUES ('66666666-6666-4666-8666-666666666666', 'allowance',
		                 '99990001-0000-4000-8000-000000000000', 'AL-1', 9000000);`,
		accept: `INSERT INTO invoice_documents (order_id, kind, original_id, number, amount_cents)
		         VALUES ('66666666-6666-4666-8666-666666666666', 'allowance',
		                 '99990001-0000-4000-8000-000000000000', 'AL-2', 1000000);`,
	},
	{
		rule: "sale_campaign_needs_discount",
		// Adding a product to a campaign when none of its variants is marked down.
		reject: `INSERT INTO sale_campaign_products (campaign_id, product_id)
		         VALUES ('aaaa1111-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333');`,
		accept: `UPDATE product_variants SET compare_at_price_cents = 3990000
		         WHERE id = '44444444-4444-4444-8444-444444444444';
		         INSERT INTO sale_campaign_products (campaign_id, product_id)
		         VALUES ('aaaa1111-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333');`,
	},
	{
		rule: "sale_campaign_variant_still_valid",
		// A campaigned product's last discounted variant cannot lose its markdown.
		reject: `UPDATE product_variants SET compare_at_price_cents = 3990000
		         WHERE id = '44444444-4444-4444-8444-444444444444';
		         INSERT INTO sale_campaign_products (campaign_id, product_id)
		         VALUES ('aaaa1111-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333');
		         UPDATE product_variants SET compare_at_price_cents = NULL
		         WHERE id = '44444444-4444-4444-8444-444444444444';`,
		// A second variant keeps a discount, so this one may drop its own.
		accept: `UPDATE product_variants SET compare_at_price_cents = 3990000
		         WHERE id = '44444444-4444-4444-8444-444444444444';
		         UPDATE product_variants SET compare_at_price_cents = 4990000
		         WHERE id = '4444aaaa-4444-4444-8444-444444444444';
		         INSERT INTO sale_campaign_products (campaign_id, product_id)
		         VALUES ('aaaa1111-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333');
		         UPDATE product_variants SET compare_at_price_cents = NULL
		         WHERE id = '44444444-4444-4444-8444-444444444444';`,
	},
	{
		rule: "invoice_document_lines_append_only",
		reject: `INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type)
		         VALUES ('11110023-0000-4000-8000-000000000001', '99990001-0000-4000-8000-000000000000', '手機', 1, 6788000, 6788000, 'taxable');
		         UPDATE invoice_document_lines SET amount_cents = 1
		         WHERE id = '11110023-0000-4000-8000-000000000001';`,
		acceptNote: "appending is the only permitted operation on a filed document's lines",
	},
}

func creditEntry(amount int, key string) string {
	return fmt.Sprintf(
		`INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
		 VALUES ('a0000001-0000-4000-8000-000000000000', %d, 'spend', '%s');`, amount, key)
}

func refund(key string, amount int) string {
	return fmt.Sprintf(
		`INSERT INTO refunds (payment_id, request_key, amount_cents)
		 VALUES ('77770001-0000-4000-8000-000000000000', '%s', %d);`, key, amount)
}

// ---------------------------------------------------------------------------
// Concurrency
//
// The tests above run one statement at a time, and every guard here would pass
// them while still being wrong: a trigger that reads before it locks looks
// identical in a single session. These run two sessions against the same row
// and require that only one wins.
// ---------------------------------------------------------------------------

// raceOutcome runs two writers against the same row with a DETERMINISTIC
// interleaving, which is the only way these guards can be tested.
//
// The first attempt at this used two goroutines released by a channel and
// proved nothing: each statement finished in microseconds, so they never
// overlapped, and removing the lock from the guard left every test green.
//
// Here T1 opens, executes, and STAYS OPEN. T2 then executes while T1 holds
// whatever it holds — if the guard locks its aggregate root, T2 blocks; if it
// does not, T2 sails past on a stale read. Only then does T1 commit, letting
// T2 finish. The two errors are returned so a caller can require exactly one.
func raceOutcome(t *testing.T, stmt1, stmt2 string) (err1, err2 error) {
	t.Helper()
	ctx := t.Context()

	c1, err := schemaPool(t).Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer c1.Release()
	c2, err := schemaPool(t).Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer c2.Release()

	tx1, err := c1.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	tx2, err := c2.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	_, err1 = tx1.Exec(ctx, stmt1)

	// T2's backend pid, so its lock-wait state can be observed rather than
	// guessed at with a sleep.
	var pid2 int
	if err := c2.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid2); err != nil {
		t.Fatalf("backend pid: %v", err)
	}

	// T2 runs while T1 is still open. It either blocks on T1's lock or does
	// not; the goroutine exists so that blocking does not deadlock the test.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err2 = tx2.Exec(ctx, stmt2)
	}()

	// Advance only when T2 has reached a decided state: finished, or genuinely
	// waiting on a lock. Polling pg_stat_activity removes the timing guess a
	// fixed sleep depended on — on a slow runner the old 300ms could commit T1
	// before T2 had even started, and an unguarded T2 would then read fresh
	// data and pass while proving nothing.
	waitForDecision(t, pid2, done)

	if err1 == nil {
		if cerr := tx1.Commit(ctx); cerr != nil {
			err1 = cerr
		}
	} else {
		_ = tx1.Rollback(ctx)
	}

	<-done // T1 has released its lock; T2 can now finish either way

	if err2 == nil {
		if cerr := tx2.Commit(ctx); cerr != nil {
			err2 = cerr
		}
	} else {
		_ = tx2.Rollback(ctx)
	}
	return err1, err2
}

// waitForDecision blocks until T2 either finishes or is confirmed waiting on a
// lock, so the caller commits T1 at a point where the interleaving is real
// rather than assumed. It fails the test if T2 neither finishes nor blocks
// within a generous ceiling — that would mean the test proved nothing.
func waitForDecision(t *testing.T, pid int, done <-chan struct{}) {
	t.Helper()
	ctx := t.Context()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case <-done:
			return
		default:
		}
		var waiting bool
		if err := schemaPool(t).QueryRow(ctx, `
			SELECT wait_event_type = 'Lock'
			FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&waiting); err == nil && waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("second writer neither finished nor blocked on a lock; the interleaving did not happen")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// requireExactlyOne fails unless precisely one of the two writers won.
func requireExactlyOne(t *testing.T, what string, err1, err2 error) {
	t.Helper()
	switch {
	case err1 == nil && err2 == nil:
		t.Errorf("both writers succeeded; %s is not protected", what)
	case err1 != nil && err2 != nil:
		t.Errorf("neither writer succeeded (%v / %v); %s rejects legal work", err1, err2, what)
	}
}

// TestStockCannotOversell is the launch blocker the review named: two buyers
// take the last unit at the same moment, and exactly one may have it.
//
// Two mechanisms stand behind it — the conditional UPDATE inside
// record_inventory_movement() and product_variants_stock_non_negative — so
// removing either alone leaves this test green. That is defence in depth, not
// a gap in the test: taking BOTH away does turn it red, which is what proves
// it can see an oversell at all.
func TestStockCannotOversell(t *testing.T) {
	variant := "11110005-0000-4000-8000-000000000001"
	setup(t, `
		INSERT INTO brands (id, slug, name) VALUES ('11110006-0000-4000-8000-000000000001','oversell','O');
		INSERT INTO categories (id, slug, name) VALUES ('11110007-0000-4000-8000-000000000001','oversell','O');
		INSERT INTO products (id, brand_id, category_id, slug, name, status, published_at)
		VALUES ('11110008-0000-4000-8000-000000000001','11110006-0000-4000-8000-000000000001',
		        '11110007-0000-4000-8000-000000000001','oversell','O','active',now());
		INSERT INTO product_variants (id, product_id, sku, price_cents, stock_quantity)
		VALUES ('`+variant+`','11110008-0000-4000-8000-000000000001','OVERSELL-1',100000,1);`)
	t.Cleanup(func() { cleanupOversell(t, variant) })

	err1, err2 := raceOutcome(t,
		`SELECT record_inventory_movement('`+variant+`', -1, 'sale', 'race-1')`,
		`SELECT record_inventory_movement('`+variant+`', -1, 'sale', 'race-2')`)
	requireExactlyOne(t, "the last unit of stock", err1, err2)

	var stock int
	if err := schemaPool(t).QueryRow(t.Context(),
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, variant).Scan(&stock); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if stock != 0 {
		t.Errorf("stock settled at %d; want 0", stock)
	}
}

// TestRefundsCannotRacePastCapture issues two refunds of 60% of a capture at
// the same moment. Their sum is 120%, so one must be refused.
func TestRefundsCannotRacePastCapture(t *testing.T) {
	order := "11110009-0000-4000-8000-000000000001"
	payment := "1111000a-0000-4000-8000-000000000001"
	setup(t, `
		INSERT INTO shipping_methods (id, code) VALUES ('1111000f-0000-4000-8000-000000000001','race_home')
		ON CONFLICT DO NOTHING;
		INSERT INTO shipping_method_versions (id, method_id, name, fee_cents)
		VALUES ('11110010-0000-4000-8000-000000000001','1111000f-0000-4000-8000-000000000001','宅配',8000)
		ON CONFLICT DO NOTHING;
		INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		VALUES ('`+order+`','GO-260721-000900','11110010-0000-4000-8000-000000000001','race_home','宅配');
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ('`+order+`','R-1','商品',100000,1);
		INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
		VALUES ('`+order+`','r@example.com','王','09','110','台北市','信義區','路 1 號');
		INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, captured_amount_cents, paid_at)
		VALUES ('`+payment+`','`+order+`','pi_race','succeeded',100000,100000,now());`)
	t.Cleanup(func() {
		mustExec(t, `DELETE FROM refunds WHERE payment_id = $1`, payment)
		mustExec(t, `DELETE FROM payments WHERE id = $1`, payment)
		mustExec(t, `DELETE FROM order_private_data WHERE order_id = $1`, order)
		mustExec(t, `DELETE FROM order_lines WHERE order_id = $1`, order)
		mustExec(t, `DELETE FROM orders WHERE id = $1`, order)
	})

	err1, err2 := raceOutcome(t,
		`INSERT INTO refunds (payment_id, request_key, amount_cents)
		 VALUES ('`+payment+`', 'refund-race-1', 60000)`,
		`INSERT INTO refunds (payment_id, request_key, amount_cents)
		 VALUES ('`+payment+`', 'refund-race-2', 60000)`)
	requireExactlyOne(t, "a capture of NT$1,000 against two refunds of NT$600", err1, err2)

	var total int64
	if err := schemaPool(t).QueryRow(t.Context(),
		`SELECT coalesce(sum(amount_cents), 0) FROM refunds WHERE payment_id = $1`,
		payment).Scan(&total); err != nil {
		t.Fatalf("read refunds: %v", err)
	}
	if total > 100000 {
		t.Errorf("refunds total %d against a capture of 100000", total)
	}
}

// TestStoreCreditCannotRacePastBalance spends the same balance twice at once.
func TestStoreCreditCannotRacePastBalance(t *testing.T) {
	user := "1111000b-0000-4000-8000-000000000001"
	account := "1111000e-0000-4000-8000-000000000001"
	setup(t, `
		INSERT INTO users (id, email) VALUES ('`+user+`','race@example.com');
		INSERT INTO store_credit_accounts (id, user_id) VALUES ('`+account+`','`+user+`');
		INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
		VALUES ('`+account+`', 100000, 'grant', 'race-grant');`)
	t.Cleanup(func() {
		mustExec(t, `DELETE FROM store_credit_entries WHERE account_id = $1`, account)
		mustExec(t, `DELETE FROM store_credit_accounts WHERE id = $1`, account)
		mustExec(t, `DELETE FROM users WHERE id = $1`, user)
	})

	err1, err2 := raceOutcome(t,
		`INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
		 VALUES ('`+account+`', -80000, 'spend', 'credit-race-1')`,
		`INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
		 VALUES ('`+account+`', -80000, 'spend', 'credit-race-2')`)
	requireExactlyOne(t, "a balance of NT$1,000 against two spends of NT$800", err1, err2)

	var balance int64
	if err := schemaPool(t).QueryRow(t.Context(),
		`SELECT coalesce(sum(amount_cents), 0) FROM store_credit_entries WHERE account_id = $1`,
		account).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if balance < 0 {
		t.Errorf("balance settled at %d", balance)
	}
}

// TestOrderNumbersAreUniqueUnderConcurrency takes many numbers at once. The
// counter is the schema's answer to a MAX()+1 that hands two checkouts the
// same number.
func TestOrderNumbersAreUniqueUnderConcurrency(t *testing.T) {
	ctx := t.Context()
	p := schemaPool(t)

	const n = 20
	numbers := make([]string, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range numbers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = p.QueryRow(ctx, `SELECT next_order_number()`).Scan(&numbers[i])
		}(i)
	}
	close(start)
	wg.Wait()

	seen := make(map[string]int, n)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("next_order_number: %v", err)
		}
		seen[numbers[i]]++
	}
	if len(seen) != n {
		var dupes []string
		for num, count := range seen {
			if count > 1 {
				dupes = append(dupes, fmt.Sprintf("%s x%d", num, count))
			}
		}
		sort.Strings(dupes)
		t.Errorf("%d distinct numbers from %d calls; duplicates: %s",
			len(seen), n, strings.Join(dupes, ", "))
	}
}

// TestUpdatedAtIsMaintained covers the bookkeeping trigger excluded from the
// completeness gate above.
func TestUpdatedAtIsMaintained(t *testing.T) {
	ctx := t.Context()
	p := schemaPool(t)

	id := "1111000c-0000-4000-8000-000000000001"
	setup(t, `INSERT INTO brands (id, slug, name) VALUES ('`+id+`','touched','Touched');`)
	t.Cleanup(func() { mustExec(t, `DELETE FROM brands WHERE id = $1`, id) })

	var before, after string
	if err := p.QueryRow(ctx, `SELECT updated_at::text FROM brands WHERE id = $1`, id).Scan(&before); err != nil {
		t.Fatalf("read: %v", err)
	}
	mustExec(t, `UPDATE brands SET name = 'Touched again' WHERE id = $1`, id)
	if err := p.QueryRow(ctx, `SELECT updated_at::text FROM brands WHERE id = $1`, id).Scan(&after); err != nil {
		t.Fatalf("read: %v", err)
	}
	if before == after {
		t.Errorf("updated_at stayed at %s across an update", before)
	}
}

// TestRefundMustMatchOrder proves a refund cannot relieve one order's return
// against another order's capture: refunds_guard reads the payment's order and
// rejects a return request naming a different one.
func TestRefundMustMatchOrder(t *testing.T) {
	// A return request on the UNPAID order; a refund against the PAID order's
	// payment. The two name different orders.
	err := run(t, `
		INSERT INTO return_requests (id, order_id, reason)
		VALUES ('11110031-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', '不合用');
		INSERT INTO refunds (payment_id, return_request_id, request_key, amount_cents, status)
		VALUES ('77770001-0000-4000-8000-000000000000', '11110031-0000-4000-8000-000000000001', 'rk-xorder', 1000, 'pending');`)
	if err == nil {
		t.Fatal("a refund tied a paid order's capture to another order's return")
	}
	if _, name := constraintViolation(err); name != "refunds_same_order" {
		t.Fatalf("refused by %q, want refunds_same_order: %v", name, err)
	}
}

// TestReleaseReservationRefusesPaidOrder proves the expiry sweep cannot hand a
// paid order's held stock back to the shelf: release_reservation refuses a hold
// whose order has a succeeded payment. Its only exit is consume_reservation.
func TestReleaseReservationRefusesPaidOrder(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, fixtures); err != nil {
		t.Fatalf("fixtures: %v", err)
	}

	// A hold on the PAID fixture order (66666666 carries succeeded payment 77770001).
	var held string
	if err := tx.QueryRow(ctx,
		`SELECT hold_inventory('66666666-6666-4666-8666-666666666666',
			'44444444-4444-4444-8444-444444444444', 1, now() + interval '15 min', 'hold-paid-1')`).
		Scan(&held); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT release_reservation($1)`, held); err == nil {
		t.Fatal("released a hold on a paid order — sold stock returned to the shelf")
	} else if _, name := constraintViolation(err); name != "inventory_reservation_paid_no_release" {
		t.Fatalf("refused by %q, want inventory_reservation_paid_no_release: %v", name, err)
	}
}

// TestCaptureMustMatchOrderTotal proves a payment cannot mark an order paid for
// the wrong amount: an underpay (NT$1 against an NT$33,980 order) and an overpay
// are both refused, while the exact total is accepted. The fixture's paid order
// already carries a matching capture, so this exercises the unpaid order.
func TestCaptureMustMatchOrderTotal(t *testing.T) {
	// order 6666aaaa: one line of 3,690,000, no shipping/discount/tax → total 3,690,000.
	for _, tc := range []struct {
		name    string
		capture int64
		wantErr bool
	}{
		{"underpay", 100, true},
		{"overpay", 9000000, true},
		{"exact", 3690000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(t, `INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents, captured_amount_cents, paid_at)
				VALUES ('6666aaaa-6666-4666-8666-666666666666', 'pi-cap-`+tc.name+`', 'succeeded', `+
				itoa(tc.capture)+`, `+itoa(tc.capture)+`, now());`)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("capture %d accepted against a 3,690,000 order", tc.capture)
				}
				if _, name := constraintViolation(err); name != "payments_capture_matches_order" {
					t.Fatalf("refused by %q, want payments_capture_matches_order: %v", name, err)
				}
			} else if err != nil {
				t.Fatalf("exact capture refused: %v", err)
			}
		})
	}
}

// TestGoenAppCannotEraseWebhookLedger proves the app cannot delete or rewrite the
// webhook dedupe ledger (which would let a resent event be processed twice), but
// can still stamp when it processed one.
func TestGoenAppCannotEraseWebhookLedger(t *testing.T) {
	if goenAppHasTablePriv(t, "payment_webhook_events", "DELETE") {
		t.Error("goen_app can DELETE payment_webhook_events; a resent event could be replayed")
	}
	// UPDATE at table level is revoked; only the processed_at column is granted.
	var canProcessed, canPayload bool
	if err := schemaPool(t).QueryRow(t.Context(),
		`SELECT has_column_privilege('goen_app', 'payment_webhook_events', 'processed_at', 'UPDATE'),
		        has_column_privilege('goen_app', 'payment_webhook_events', 'payload', 'UPDATE')`).
		Scan(&canProcessed, &canPayload); err != nil {
		t.Fatalf("has_column_privilege: %v", err)
	}
	if !canProcessed {
		t.Error("goen_app cannot stamp processed_at; it needs to mark an event handled")
	}
	if canPayload {
		t.Error("goen_app can rewrite the webhook payload; the raw evidence must be immutable")
	}
}

// TestOrderCannotLeavePendingUnfunded encodes the owner's decision that goen
// does not ship what it has not collected: an unpaid order cannot move from
// pending to picking, a paid one can, and cancelling from pending is always
// allowed regardless of funding. A zero-owed order (none exists in fixtures) is
// funded without a payment — that path is covered by the checkout batch's tests
// when store credit lands.
func TestOrderCannotLeavePendingUnfunded(t *testing.T) {
	// Unpaid order (6666aaaa) → picking: refused.
	err := run(t, `UPDATE orders SET fulfillment_status = 'picking'
	               WHERE id = '6666aaaa-6666-4666-8666-666666666666';`)
	if err == nil {
		t.Fatal("an unpaid order left pending into fulfilment")
	}
	if _, name := constraintViolation(err); name != "orders_funded_to_leave_pending" {
		t.Fatalf("refused by %q, want orders_funded_to_leave_pending: %v", name, err)
	}

	// Paid order (66666666) → picking: allowed.
	if err := run(t, `UPDATE orders SET fulfillment_status = 'picking'
	                  WHERE id = '66666666-6666-4666-8666-666666666666';`); err != nil {
		t.Fatalf("a funded order was refused fulfilment: %v", err)
	}

	// Unpaid order → cancelled: always allowed.
	if err := run(t, `UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
	                  WHERE id = '6666aaaa-6666-4666-8666-666666666666';`); err != nil {
		t.Fatalf("cancelling an unpaid order from pending was refused: %v", err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func setup(t *testing.T, stmt string) {
	t.Helper()
	if _, err := schemaPool(t).Exec(t.Context(), stmt); err != nil {
		t.Fatalf("set up: %v", err)
	}
}

func mustExec(t *testing.T, stmt string, args ...any) {
	t.Helper()
	// Cleanups run after t.Context() is cancelled, so a plain t.Context() here
	// fails with "context canceled" and leaves rows behind for the next run.
	ctx := context.WithoutCancel(t.Context())
	if _, err := schemaPool(t).Exec(ctx, stmt, args...); err != nil {
		t.Logf("clean up %q: %v", stmt, err)
	}
}

func cleanupOversell(t *testing.T, variant string) {
	t.Helper()
	mustExec(t, `DELETE FROM inventory_movements WHERE variant_id = $1`, variant)
	mustExec(t, `DELETE FROM product_variants WHERE id = $1`, variant)
	mustExec(t, `DELETE FROM products WHERE slug = 'oversell'`)
	mustExec(t, `DELETE FROM categories WHERE slug = 'oversell'`)
	mustExec(t, `DELETE FROM brands WHERE slug = 'oversell'`)
}
