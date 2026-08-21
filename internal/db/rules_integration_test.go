//go:build integration

package db_test

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// These rules span rows, so a CHECK cannot express them. Each is enforced by a trigger that
// locks its aggregate root before it reads, which only the concurrency tests below can prove.

// coveredByNamedTest is the triggers whose cases do not fit the rule table's shape, mapped to
// the test that does cover them. A trigger named in neither place still fails.
var coveredByNamedTest = map[string]string{
	"product_variants_keep_product_sellable": "TestDeactivatingTheLastVariantIsRefused, TestDeletingTheLastVariantIsRefused",
	// Exercised where the send is: proving it needs an issue that has actually been sent.
	"newsletter_issues_frozen_once_sent": "TestASentIssueCannotBeRewritten (internal/newsletter)",
}

// TestEveryRuleTriggerIsExercised requires a case for every rule trigger in the catalog.
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

	covered := make(map[string]bool, len(ruleCases)+len(coveredByNamedTest))
	for _, c := range ruleCases {
		covered[c.rule] = true
	}
	for name := range coveredByNamedTest {
		covered[name] = true
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
		rule: "loyalty_never_negative",
		// The account is locked before the balance is read, or two concurrent spends both pass.
		reject: `INSERT INTO loyalty_entries (account_id, points, reason, idempotency_key, expires_on) VALUES ('a0000001-0000-4000-8000-000000000000', 100, 'seed', 'rule-seed', current_date + 365);
		         INSERT INTO loyalty_entries (account_id, points, reason, idempotency_key, expires_on) VALUES ('a0000001-0000-4000-8000-000000000000', -500, 'over', 'rule-over', NULL);`,
		accept: `INSERT INTO loyalty_entries (account_id, points, reason, idempotency_key, expires_on) VALUES ('a0000001-0000-4000-8000-000000000000', 100, 'seed', 'rule-seed', current_date + 365);
		         INSERT INTO loyalty_entries (account_id, points, reason, idempotency_key, expires_on) VALUES ('a0000001-0000-4000-8000-000000000000', -100, 'within', 'rule-within', NULL);`,
	},
	{
		rule: "loyalty_entries_append_only",
		reject: `INSERT INTO loyalty_entries (account_id, points, reason, idempotency_key, expires_on) VALUES ('a0000001-0000-4000-8000-000000000000', 100, 'seed', 'rule-seed', current_date + 365);
		         UPDATE loyalty_entries SET points = 9999 WHERE idempotency_key = 'rule-seed';`,
		accept: `INSERT INTO loyalty_entries (account_id, points, reason, idempotency_key, expires_on) VALUES ('a0000001-0000-4000-8000-000000000000', 100, 'seed', 'rule-seed', current_date + 365);`,
	},
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
		rule: "media_objects_immutable",
		// Changing the bytes under a digest makes every cached copy wrong, and the year-long
		// immutable Cache-Control makes that unfixable — so nobody may edit the row, owner included.
		reject: `INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size) VALUES (repeat('7', 64), 'image/png', '\x89504e47'::bytea, 10, 10, 4) ON CONFLICT DO NOTHING;
		         UPDATE media_objects SET width = 11 WHERE digest = repeat('7', 64);`,
		// Deleting IS allowed: that is how an upload nothing references is reclaimed.
		accept: `INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size) VALUES (repeat('7', 64), 'image/png', '\x89504e47'::bytea, 10, 10, 4) ON CONFLICT DO NOTHING;
		         DELETE FROM media_objects WHERE digest = repeat('7', 64);`,
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
		// The reject uses the unpaid order — the legal-transition check fires first, so it still names
		// this rule — while the accept must use the PAID one or orders_funded_to_leave_pending refuses.
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
		rule: "order_lines_frozen_once_committed",
		reject: `UPDATE order_lines SET unit_price_cents = 1
		         WHERE order_id = '66666666-6666-4666-8666-666666666666';`,
		accept: `UPDATE order_lines SET unit_price_cents = 1
		         WHERE order_id = '6666aaaa-6666-4666-8666-666666666666';`,
	},
	{
		rule: "orders_money_frozen_once_committed",
		reject: `UPDATE orders SET discount_cents = 50000
		         WHERE id = '66666666-6666-4666-8666-666666666666';`,
		accept: `UPDATE orders SET discount_cents = 50000
		         WHERE id = '6666aaaa-6666-4666-8666-666666666666';`,
	},
	{
		rule: "coupon_redemptions_append_only",
		reject: `UPDATE coupon_redemptions SET amount_cents = 999
		         WHERE id = 'cccc000a-0000-4000-8000-00000000000a';`,
		accept: `INSERT INTO coupon_redemptions (id, coupon_id, order_id, amount_cents)
		         VALUES ('cccc000b-0000-4000-8000-00000000000b',
		                 'cccc0009-0000-4000-8000-000000000009',
		                 '6666aaaa-6666-4666-8666-666666666666', 0);`,
	},
	{
		rule: "coupon_redemption_matches_order",
		// SET CONSTRAINTS IMMEDIATE: this one is deferred, and `run` rolls back without committing,
		// so a deferred check would never run at all.
		reject: `SET CONSTRAINTS coupon_redemption_matches_order IMMEDIATE;
		         INSERT INTO coupon_redemptions (id, coupon_id, order_id, amount_cents)
		         VALUES ('cccc000c-0000-4000-8000-00000000000c',
		                 'cccc0009-0000-4000-8000-000000000009',
		                 '6666aaaa-6666-4666-8666-666666666666', 12345);`,
		accept: `SET CONSTRAINTS coupon_redemption_matches_order IMMEDIATE;
		         INSERT INTO coupon_redemptions (id, coupon_id, order_id, amount_cents)
		         VALUES ('cccc000c-0000-4000-8000-00000000000c',
		                 'cccc0009-0000-4000-8000-000000000009',
		                 '6666aaaa-6666-4666-8666-666666666666', 0);`,
	},
	{
		rule: "products_active_has_variant",
		// SET CONSTRAINTS IMMEDIATE, because this one is DEFERRABLE INITIALLY DEFERRED and `run` rolls
		// back without ever committing. The ids are the fixture's own, spelled out rather than selected:
		// an INSERT ... SELECT matching nothing writes nothing and raises nothing.
		reject: `SET CONSTRAINTS products_active_has_variant IMMEDIATE;
		         INSERT INTO products (brand_id, category_id, slug, name, description, status, published_at)
		         VALUES ('11111111-1111-4111-8111-111111111111',
		                 '22222222-2222-4222-8222-222222222222',
		                 'nothing-to-sell', '無貨可賣', '', 'active', now());`,
		accept: `SET CONSTRAINTS products_active_has_variant IMMEDIATE;
		         WITH p AS (
		             INSERT INTO products (brand_id, category_id, slug, name, description, status, published_at)
		             VALUES ('11111111-1111-4111-8111-111111111111',
		                     '22222222-2222-4222-8222-222222222222',
		                     'has-something', '有貨可賣', '', 'active', now())
		             RETURNING id)
		         INSERT INTO product_variants (product_id, sku, price_cents, position)
		         SELECT id, 'HAS-SOMETHING-1', 100000, 0 FROM p;`,
	},
	{
		rule: "return_within_shipment",
		// The line was ORDERED 2 and SHIPPED 1. It is the SHIPPED ceiling that refuses asking for 2
		// back: a rule bounded by what was ordered accepts the same statement.
		reject: `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		         VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660003-0000-4000-8000-000000000000', 2);`,
		accept: `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		         VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660003-0000-4000-8000-000000000000', 1);`,
	},
	{
		rule: "warranty_unit_within_purchase",
		reject: `INSERT INTO warranty_registrations (order_line_id, unit_no, expires_on)
		         VALUES ('66660001-0000-4000-8000-000000000000', 3, current_date + 730);`,
		// Two were bought, so unit 2 exists. Registration is per UNIT, which is why the accept
		// registers both: one row per order line could not represent the second unit at all.
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
		// Clearing the key on an ISSUED allowance takes the row out of
		// invoice_documents_request_key and lets the same refund be filed a
		// second time at the 財政部, which is what that index exists to stop.
		rule: "invoice_documents_only_void",
		// The document must HOLD a key first: the fixture's invoice carries none,
		// so clearing it is NULL to NULL and changes nothing — a statement that
		// cannot violate the rule it is meant to prove.
		reject: `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents, original_id, request_key)
		         VALUES ('11110025-0000-4000-8000-000000000001',
		                 '66666666-6666-4666-8666-666666666666', 'allowance', 'GD-ALLOW-01', 100,
		                 '99990001-0000-4000-8000-000000000000', 'allowance:key');
		         UPDATE invoice_documents SET request_key = NULL
		         WHERE id = '11110025-0000-4000-8000-000000000001';`,
		acceptNote: "the void above is the legal neighbour; an issued document's key never moves",
	},
	{
		// A filed document is filed. A PENDING claim is a reservation with no
		// number and nothing at the 加值中心, and releasing one is the only door
		// out of a 折讓 the provider refused.
		rule: "invoice_documents_only_void",
		reject: `DELETE FROM invoice_documents
		         WHERE id = '99990001-0000-4000-8000-000000000000';`,
		accept: `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents, status, request_key)
		         VALUES ('11110024-0000-4000-8000-000000000001',
		                 '6666aaaa-6666-4666-8666-666666666666', 'invoice', '', 100, 'pending', 'released');
		         DELETE FROM invoice_documents WHERE id = '11110024-0000-4000-8000-000000000001';`,
	},
	{
		rule: "payments_no_regression",
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
		// Fixture stock is 14, safety_stock 2: -12 lands exactly on the floor, -13 breaks it.
		reject: `SELECT record_inventory_movement('44444444-4444-4444-8444-444444444444', -13, 'sale', 'k-over');`,
		accept: `SELECT record_inventory_movement('44444444-4444-4444-8444-444444444444', -12, 'sale', 'k-floor');`,
	},
	{
		rule: "payments_settled_is_history",
		reject: `UPDATE payments SET captured_amount_cents = 1
		         WHERE id = '77770001-0000-4000-8000-000000000000';`,
		acceptNote: "a succeeded payment's amounts are frozen; there is no legal edit to them",
	},
	{
		rule: "refunds_no_regression",
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
		// Raising a succeeded refund's amount misstates what was returned. no_regression does not
		// fire — the status is untouched, only the amount — so this trigger must be the one to refuse.
		reject: `INSERT INTO refunds (id, payment_id, request_key, amount_cents, status, succeeded_at)
		         VALUES ('11110023-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
		                 'rk-freeze', 50000, 'succeeded', now());
		         UPDATE refunds SET amount_cents = 60000
		         WHERE id = '11110023-0000-4000-8000-000000000001';`,
		accept: `INSERT INTO refunds (id, payment_id, request_key, amount_cents, status)
		         VALUES ('11110024-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
		                 'rk-freeze-ok', 40000, 'pending');
		         UPDATE refunds SET amount_cents = 45000
		         WHERE id = '11110024-0000-4000-8000-000000000001';`,
	},
	{
		rule: "payments_require_complete_order",
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
		// The fixture version ffff0002 belongs to method home_delivery; an order snapshotting a
		// different code against it is the contradiction the FK cannot catch.
		reject: `INSERT INTO orders (id, shipping_version_id, shipping_method_code, shipping_method_name)
		         VALUES ('11110030-0000-4000-8000-000000000001', 'ffff0002-0000-4000-8000-000000000000', 'store_pickup', '超商取貨');`,
		accept: `INSERT INTO orders (id, shipping_version_id, shipping_method_code, shipping_method_name)
		         VALUES ('11110030-0000-4000-8000-000000000001', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
	},
	{
		rule: "return_requests_start_requested",
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
		reject: `INSERT INTO sale_campaign_products (campaign_id, product_id)
		         VALUES ('aaaa1111-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333');`,
		accept: `UPDATE product_variants SET compare_at_price_cents = 3990000
		         WHERE id = '44444444-4444-4444-8444-444444444444';
		         INSERT INTO sale_campaign_products (campaign_id, product_id)
		         VALUES ('aaaa1111-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333');`,
	},
	{
		rule: "orders_history_frozen",
		// This is the only guard that reaches cancelled_at: orders_check_transition fires on
		// fulfillment_status alone and orders_freeze_money looks only at money and the snapshot.
		reject: `UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		         WHERE id = '6666aaaa-6666-4666-8666-666666666666';
		         UPDATE orders SET cancelled_at = now() + interval '1 day'
		         WHERE id = '6666aaaa-6666-4666-8666-666666666666';`,
		accept: `UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		         WHERE id = '6666aaaa-6666-4666-8666-666666666666';`,
	},
	{
		rule: "product_reviews_verified_is_real",
		// The second fixture user has no order at all, so the badge would be pure assertion — and the
		// displayed rating is computed live from these rows. The buyer's claim is real.
		reject: `INSERT INTO product_reviews (product_id, user_id, rating, body, is_verified_purchase)
		         VALUES ('33333333-3333-4333-8333-333333333333',
		                 '5555aaaa-5555-4555-8555-555555555555', 5, '沒買過', true);`,
		accept: `INSERT INTO product_reviews (product_id, user_id, rating, body, is_verified_purchase)
		         VALUES ('33333333-3333-4333-8333-333333333333',
		                 '55555555-5555-4555-8555-555555555555', 5, '真的買了', true);`,
	},
	{
		rule: "sale_campaign_variant_still_valid",
		reject: `UPDATE product_variants SET compare_at_price_cents = 3990000
		         WHERE id = '44444444-4444-4444-8444-444444444444';
		         INSERT INTO sale_campaign_products (campaign_id, product_id)
		         VALUES ('aaaa1111-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333');
		         UPDATE product_variants SET compare_at_price_cents = NULL
		         WHERE id = '44444444-4444-4444-8444-444444444444';`,
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

	// Rules raised inside a function body rather than by a trigger of their own
	// name. TestEveryRuleTriggerIsExercised cannot see these: deleting one branch
	// removes no trigger, so the trigger that carries it still reads as covered.
	{
		rule: "coupon_exists",
		reject: `SELECT redeem_coupon('cccc0009-0000-4000-8000-00000000dead',
		         '6666aaaa-6666-4666-8666-666666666666', NULL, 20000);`,
		accept: `SELECT redeem_coupon('cccc0009-0000-4000-8000-000000000009',
		         '6666aaaa-6666-4666-8666-666666666666', NULL, 20000);`,
	},
	{
		rule: "coupon_is_current",
		reject: `UPDATE coupons SET is_active = false WHERE id = 'cccc0009-0000-4000-8000-000000000009';
		         SELECT redeem_coupon('cccc0009-0000-4000-8000-000000000009',
		         '6666aaaa-6666-4666-8666-666666666666', NULL, 20000);`,
		// The same coupon inside its window, so the two runs differ only in the rule.
		accept: `SELECT redeem_coupon('cccc0009-0000-4000-8000-000000000009',
		         '6666aaaa-6666-4666-8666-666666666666', NULL, 20000);`,
	},
	{
		rule: "coupon_within_total_limit",
		// The fixture already holds one redemption against a live order, so a cap
		// of one is spent. The limit counts rows, never a counter two checkouts read.
		reject: `UPDATE coupons SET max_redemptions = 1 WHERE id = 'cccc0009-0000-4000-8000-000000000009';
		         SELECT redeem_coupon('cccc0009-0000-4000-8000-000000000009',
		         '6666aaaa-6666-4666-8666-666666666666', NULL, 20000);`,
		accept: `UPDATE coupons SET max_redemptions = 2 WHERE id = 'cccc0009-0000-4000-8000-000000000009';
		         SELECT redeem_coupon('cccc0009-0000-4000-8000-000000000009',
		         '6666aaaa-6666-4666-8666-666666666666', NULL, 20000);`,
	},
	{
		rule: "coupon_within_customer_limit",
		// The fixture's redemption belongs to 王小明, and it is the SAME customer
		// asking again: a guest would meet the total cap instead.
		reject: `UPDATE coupons SET per_customer_limit = 1 WHERE id = 'cccc0009-0000-4000-8000-000000000009';
		         INSERT INTO coupon_redemptions (coupon_id, order_id, user_id, amount_cents)
		         VALUES ('cccc0009-0000-4000-8000-000000000009',
		                 '6666bbbb-6666-4666-8666-666666666666',
		                 '55555555-5555-4555-8555-555555555555', 100000);
		         SELECT redeem_coupon('cccc0009-0000-4000-8000-000000000009',
		         '6666aaaa-6666-4666-8666-666666666666',
		         '55555555-5555-4555-8555-555555555555', 20000);`,
		accept: `UPDATE coupons SET per_customer_limit = 2 WHERE id = 'cccc0009-0000-4000-8000-000000000009';
		         INSERT INTO coupon_redemptions (coupon_id, order_id, user_id, amount_cents)
		         VALUES ('cccc0009-0000-4000-8000-000000000009',
		                 '6666bbbb-6666-4666-8666-666666666666',
		                 '55555555-5555-4555-8555-555555555555', 100000);
		         SELECT redeem_coupon('cccc0009-0000-4000-8000-000000000009',
		         '6666aaaa-6666-4666-8666-666666666666',
		         '55555555-5555-4555-8555-555555555555', 20000);`,
	},
	{
		rule: "inventory_reservation_state",
		reject: `SELECT hold_inventory('6666aaaa-6666-4666-8666-666666666666',
		             '44444444-4444-4444-8444-444444444444', 2, now() + interval '1 hour', 'rule-hold');
		         SELECT consume_reservation(id) FROM inventory_reservations
		         WHERE order_id = '6666aaaa-6666-4666-8666-666666666666';
		         SELECT consume_reservation(id) FROM inventory_reservations
		         WHERE order_id = '6666aaaa-6666-4666-8666-666666666666' AND state = 'consumed';`,
		accept: `SELECT hold_inventory('6666aaaa-6666-4666-8666-666666666666',
		             '44444444-4444-4444-8444-444444444444', 2, now() + interval '1 hour', 'rule-hold');
		         SELECT consume_reservation(id) FROM inventory_reservations
		         WHERE order_id = '6666aaaa-6666-4666-8666-666666666666';`,
	},
	{
		rule: "inventory_reservation_consume_within_hold",
		reject: `SELECT hold_inventory('6666aaaa-6666-4666-8666-666666666666',
		             '44444444-4444-4444-8444-444444444444', 2, now() + interval '1 hour', 'rule-hold');
		         SELECT consume_reservation_partial(id, 3) FROM inventory_reservations
		         WHERE order_id = '6666aaaa-6666-4666-8666-666666666666';`,
		accept: `SELECT hold_inventory('6666aaaa-6666-4666-8666-666666666666',
		             '44444444-4444-4444-8444-444444444444', 2, now() + interval '1 hour', 'rule-hold');
		         SELECT consume_reservation_partial(id, 1) FROM inventory_reservations
		         WHERE order_id = '6666aaaa-6666-4666-8666-666666666666';`,
	},
	{
		rule:       "payments_provider_ref_known",
		reject:     `SELECT capture_payment('pi_no_such_session', 6790000, 'visa', '4242');`,
		acceptNote: "capture_payment's happy path is covered where the webhook is, in internal/payment",
	},
	{
		rule:       "refunds_request_key_known",
		reject:     `SELECT settle_refund('rk_no_such_refund', 're_x', 'succeeded');`,
		acceptNote: "settle_refund's happy path needs a refund row a decision wrote, which internal/admin does",
	},
	{
		rule: "audit_events_actor_required",
		reject: `SELECT record_audit_event(NULL, 'product.published', 'products',
		             '33333333-3333-4333-8333-333333333333');`,
		accept: `SELECT record_audit_event('55555555-5555-4555-8555-555555555555', 'product.published',
		             'products', '33333333-3333-4333-8333-333333333333');`,
	},
	{
		rule: "return_requests_completed_is_inspected",
		// The fixture's request has one line nobody has opened, which is what
		// separates "not looked at yet" from "looked at, nothing arrived".
		reject: shippedReturnLine + `
		         UPDATE return_requests SET status = 'approved', decided_at = now()
		         WHERE id = '88880001-0000-4000-8000-000000000000';
		         UPDATE return_requests SET status = 'completed'
		         WHERE id = '88880001-0000-4000-8000-000000000000';`,
		accept: shippedReturnLine + `
		         UPDATE return_requests SET status = 'approved', decided_at = now()
		         WHERE id = '88880001-0000-4000-8000-000000000000';
		         UPDATE return_request_lines SET received_quantity = 1, restocked_quantity = 1
		         WHERE return_request_id = '88880001-0000-4000-8000-000000000000';
		         UPDATE return_requests SET status = 'completed'
		         WHERE id = '88880001-0000-4000-8000-000000000000';`,
	},
	{
		rule: "orders_have_delivery",
		// Deferred to commit, so the whole statement group is the case: an order
		// with lines and no destination is one nobody can deliver.
		reject: `INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		         VALUES ('6666cccc-6666-4666-8666-666666666666', 'GO-260721-000390',
		                 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');
		         INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
		         VALUES ('6666cccc-6666-4666-8666-666666666666', 'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', 3390000, 1, 0);
		         SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
		acceptNote: "the fixture's own orders are the legal neighbour, and every other case commits over them",
	},
	{
		rule: "orders_total_non_negative",
		// A discount larger than the lines: the coupon cap exists to stop this,
		// and this is the rule underneath it that makes the cap more than advice.
		reject: `INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code,
		                 shipping_method_name, discount_cents)
		         VALUES ('6666dddd-6666-4666-8666-666666666666', 'GO-260721-000391',
		                 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府', 500000);
		         INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
		         VALUES ('6666dddd-6666-4666-8666-666666666666', 'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', 100000, 1, 0);
		         INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                 postal_code, city, district, street)
		         VALUES ('6666dddd-6666-4666-8666-666666666666', 'neg@example.com', '負數', '0900000002',
		                 '110', '台北市', '信義區', '松高路 1 號');
		         SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
		acceptNote: "the fixture's orders all total above zero and every other case commits over them",
	},
}

// shippedReturnLine puts the fixture's order line in a parcel and claims one of
// it. return_within_shipment reads the shipment, so without this the statement
// meant to prove the inspection rule is refused by a different one (#8).
const shippedReturnLine = `
	INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
	VALUES ('66666666-6666-4666-8666-666666666666',
	        '66660002-0000-4000-8000-000000000000',
	        '66660001-0000-4000-8000-000000000000', 1);
	INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
	VALUES ('66666666-6666-4666-8666-666666666666',
	        '88880001-0000-4000-8000-000000000000',
	        '66660001-0000-4000-8000-000000000000', 1);`

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

// Concurrency. Every guard here would pass the single-statement tests above while still being
// wrong: a trigger that reads before it locks looks identical in one session.

// raceOutcome runs two writers against the same row with a DETERMINISTIC interleaving. T1 opens,
// executes and STAYS OPEN; T2 then executes while T1 holds whatever it holds, and only then does
// T1 commit. Two goroutines on a start channel finish microseconds apart and never overlap.
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

	// T2's backend pid, so its lock-wait state can be observed rather than guessed at with a sleep.
	var pid2 int
	if err := c2.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid2); err != nil {
		t.Fatalf("backend pid: %v", err)
	}

	// T2 runs while T1 is still open; the goroutine is so that blocking does not deadlock the test.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err2 = tx2.Exec(ctx, stmt2)
	}()

	// Advance only once T2 is finished or genuinely waiting on a lock: on a slow runner a fixed
	// sleep commits T1 before T2 has started, and an unguarded T2 then passes on fresh data.
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

// waitForDecision blocks until T2 finishes or is confirmed waiting on a lock, and fails the
// test if neither happens within a generous ceiling.
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

// TestStockCannotOversell: two buyers take the last unit at once, and exactly one may have it.
// Two mechanisms stand behind it, so removing either alone leaves this green; both away is red.
func TestStockCannotOversell(t *testing.T) {
	variant := "11110005-0000-4000-8000-000000000001"
	setup(t, `
		INSERT INTO brands (id, slug, name) VALUES ('11110006-0000-4000-8000-000000000001','oversell','O');
		-- setup COMMITS, because the oversell proof needs two real transactions —
		-- so this row outlives the test and every later fixture load meets it,
		-- and categories_position_key refuses a second root at the same position.
		-- A literal outside the fixtures' range rather than max(position) + 1:
		-- the fixtures live only inside rolled-back transactions, so a maximum
		-- read here sees an empty table and computes the very 0 they take.
		INSERT INTO categories (id, slug, name, position)
		VALUES ('11110007-0000-4000-8000-000000000001','oversell','O',900);
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

// TestRefundsCannotRacePastCapture issues two refunds of 60% of a capture at once.
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

// TestOrderNumbersAreUniqueUnderConcurrency takes many numbers at once.
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

// TestUpdatedAtIsMaintained covers the bookkeeping trigger the completeness gate excludes.
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

// TestRefundMustMatchOrder proves a refund cannot relieve one order's return against another's capture.
func TestRefundMustMatchOrder(t *testing.T) {
	// A return request on the UNPAID order against the PAID order's payment: two different orders.
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

// TestReleaseReservationRefusesPaidOrder proves the expiry sweep cannot return a paid order's stock.
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
	} else if _, name := constraintViolation(err); name != "inventory_reservation_committed_no_release" {
		t.Fatalf("refused by %q, want inventory_reservation_committed_no_release: %v", name, err)
	}
}

// TestACancelledOrderIsSettledButNotCommitted holds the line between the two views from the side
// where they disagree: a cancelled order's money is frozen while its held stock must come back,
// and one predicate answering both leaves those units with no door to the shelf.
func TestACancelledOrderIsSettledButNotCommitted(t *testing.T) {
	const pending = "6666aaaa-6666-4666-8666-666666666666"
	cancel := `UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
	           WHERE id = '` + pending + `';`

	t.Run("cancelling still works", func(t *testing.T) {
		if err := run(t, cancel); err != nil {
			t.Fatalf("an order can no longer be cancelled: %v", err)
		}
	})

	t.Run("money is frozen afterwards", func(t *testing.T) {
		// A value that actually differs: shipping_cents defaults to 0 and orders_freeze_money returns
		// early when every guarded column is unchanged, so "SET shipping_cents = 0" proves nothing.
		err := run(t, cancel+`UPDATE orders SET shipping_cents = 12000 WHERE id = '`+pending+`';`)
		if err == nil {
			t.Fatal("rewrote the totals of a cancelled order")
		}
		if _, name := constraintViolation(err); name != "orders_money_frozen_once_committed" {
			t.Fatalf("refused by %q, want orders_money_frozen_once_committed: %v", name, err)
		}
	})

	t.Run("its stock is releasable", func(t *testing.T) {
		if err := run(t, cancel+`SELECT 1 FROM committed_orders WHERE id = '`+pending+`';`); err != nil {
			t.Fatalf("read committed_orders: %v", err)
		}
		var committed bool
		if err := pool.QueryRow(t.Context(),
			`SELECT EXISTS (SELECT 1 FROM committed_orders WHERE id = $1)`,
			pending).Scan(&committed); err != nil {
			t.Fatalf("read committed_orders: %v", err)
		}
		if committed {
			t.Error("a cancelled order reads as committed; its stock can never be released")
		}
	})

	t.Run("lines are frozen afterwards", func(t *testing.T) {
		err := run(t, cancel+`UPDATE order_lines SET quantity = 5 WHERE order_id = '`+pending+`';`)
		if err == nil {
			t.Fatal("edited the lines of a cancelled order")
		}
		if _, name := constraintViolation(err); name != "order_lines_frozen_once_committed" {
			t.Fatalf("refused by %q, want order_lines_frozen_once_committed: %v", name, err)
		}
	})
}

// TestCampaignDiscountSurvivesBulkEdits covers the two doors a per-row form of the rule leaves:
// one statement clearing every discount (a BEFORE-row trigger reads the pre-statement snapshot,
// so each row sees a sibling still discounted), and a DELETE, which UPDATE OF never fires on.
func TestCampaignDiscountSurvivesBulkEdits(t *testing.T) {
	const (
		product = "33333333-3333-4333-8333-333333333333"
		// 44444444 is on the settled order's line; 4444aaaa is on none. The DELETE case has to use the
		// unsold one, or the cascade trips order_lines_frozen_once_committed first.
		sold   = "44444444-4444-4444-8444-444444444444"
		unsold = "4444aaaa-4444-4444-8444-444444444444"
	)
	join := `INSERT INTO sale_campaign_products (campaign_id, product_id)
	         VALUES ('aaaa1111-0000-4000-8000-000000000000', '` + product + `');`

	for _, tc := range []struct {
		name string
		stmt string
	}{
		{
			"one statement clears every discount",
			`UPDATE product_variants SET compare_at_price_cents = 3990000 WHERE id = '` + sold + `';
			 UPDATE product_variants SET compare_at_price_cents = 4990000 WHERE id = '` + unsold + `';` +
				join + `UPDATE product_variants SET compare_at_price_cents = NULL
			 WHERE product_id = '` + product + `';`,
		},
		{
			// Only the unsold variant carries the discount, so deleting it is what empties the campaign.
			"the last discounted variant is deleted",
			`UPDATE product_variants SET compare_at_price_cents = NULL WHERE id = '` + sold + `';
			 UPDATE product_variants SET compare_at_price_cents = 4990000 WHERE id = '` + unsold + `';` +
				join + `DELETE FROM product_variants WHERE id = '` + unsold + `';`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(t, tc.stmt)
			if err == nil {
				t.Fatal("the campaign kept a featured product with no discount left")
			}
			if _, name := constraintViolation(err); name != "sale_campaign_variant_still_valid" {
				t.Fatalf("refused by %q, want sale_campaign_variant_still_valid: %v", name, err)
			}
		})
	}
}

// TestReleaseReservationLocksVariantBeforeOrder pins the lock ORDER: hold_inventory takes the
// variant then the order, and a release taking them the other way round closes a cycle that
// deadlocks at 40P01. Read from the source, because a deadlock race passes by luck.
func TestReleaseReservationLocksVariantBeforeOrder(t *testing.T) {
	var src string
	if err := schemaPool(t).QueryRow(t.Context(),
		`SELECT prosrc FROM pg_proc WHERE proname = 'release_reservation'`).Scan(&src); err != nil {
		t.Fatalf("read release_reservation source: %v", err)
	}

	variant := strings.Index(src, "product_variants")
	order := strings.Index(src, "FROM orders")
	switch {
	case variant < 0:
		t.Fatal("release_reservation does not lock the variant at all; hold_inventory does, so the two disagree")
	case order < 0:
		t.Fatal("release_reservation no longer locks the order")
	case variant > order:
		t.Error("release_reservation locks the order before the variant; hold_inventory takes them " +
			"the other way round, which closes an ABBA cycle (40P01) on a concurrent re-hold and release")
	}
}

// TestEraseUserLeavesNoPersonalData asserts each table is clear afterwards rather than that the
// function ran. invoice_preferences is keyed by ORDER, so its invoice carrier is easiest to miss.
func TestEraseUserLeavesNoPersonalData(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, fixtures); err != nil {
		t.Fatalf("fixtures: %v", err)
	}

	const user = "55555555-5555-4555-8555-555555555555"

	// A letter to this customer, waiting to go out. Without it the JSON half of
	// the sweep below has no subject: it would report a clean outbox because
	// the outbox was empty, which is a check over data that needs the data
	// seeded. The payload is the shape enqueueBulk and the order producers
	// write — the address frozen in, because the worker serves nobody and
	// cannot look one up.
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (topic, dedupe_key, payload)
		VALUES ('order.paid', 'erasure-probe',
		        jsonb_build_object('email', 'Ming@Example.com', 'order_number', 'GO-260721-000387'))`); err != nil {
		t.Fatalf("enqueue a letter to the customer being erased: %v", err)
	}

	if _, err := tx.Exec(ctx, `SELECT erase_user($1)`, user); err != nil {
		t.Fatalf("erase_user: %v", err)
	}

	for _, probe := range []struct {
		what  string
		query string
	}{
		{"the account", `SELECT count(*) FROM users WHERE id = '` + user + `'`},
		// Keyed by order, not by user: after erasure orders.user_id is NULL, so a probe joining on the
		// user id could never come back non-zero and could never fail.
		{"delivery details", `SELECT count(*) FROM order_private_data pd JOIN orders o ON o.id = pd.order_id
			WHERE o.order_number = 'GO-260721-000387' AND pd.email IS NOT NULL`},
		{"the invoice carrier", `SELECT count(*) FROM invoice_preferences ip JOIN orders o ON o.id = ip.order_id
			WHERE o.order_number = 'GO-260721-000387'`},
		// By ADDRESS and never by user_id: stock_notifications.user_id is ON
		// DELETE SET NULL, so a probe asking for the account reads zero whether
		// or not a single row was deleted — the shape the comment four lines
		// above warns about, committed on the line under it. Both addresses,
		// because a signed-in customer may ask using any address they type.
		{"restock notifications", `SELECT count(*) FROM stock_notifications
			WHERE lower(email) IN ('ming@example.com', 'ming.work@example.com')`},
	} {
		var n int
		if err := tx.QueryRow(ctx, probe.query).Scan(&n); err != nil {
			t.Fatalf("probe %s: %v", probe.what, err)
		}
		if n != 0 {
			t.Errorf("%s survived erasure (%d row(s) left)", probe.what, n)
		}
	}

	// Derived from information_schema, so a future table with an email column is covered by
	// existing rather than by somebody remembering this test.
	assertNoTableHoldsTheAddress(ctx, t, tx, "Ming@Example.com")

	// The order is a financial record and must outlive its customer.
	var total int64
	if err := tx.QueryRow(ctx,
		`SELECT shipping_cents FROM orders WHERE order_number = 'GO-260721-000387'`).Scan(&total); err != nil {
		t.Fatalf("the erased customer's order disappeared with them: %v", err)
	}
	if total != 8000 {
		t.Errorf("order shipping is %d, want 8000 — erasure altered the financial record", total)
	}
}

// TestZeroOwedOrderIsCommitted holds one criterion at three exits. A zero-owed order can never
// carry a succeeded payment, so a guard reading "exists a succeeded payment" as "committed"
// leaves its lines, totals and held stock editable after it has left pending.
func TestZeroOwedOrderIsCommitted(t *testing.T) {
	const zeroOwed = "6666bbbb-6666-4666-8666-666666666666"

	t.Run("lines frozen", func(t *testing.T) {
		err := run(t, `INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ('`+zeroOwed+`', 'SNEAK-1', '事後追加', 500000, 1, 9);`)
		if err == nil {
			t.Fatal("appended a priced line to a committed zero-owed order")
		}
		if _, name := constraintViolation(err); name != "order_lines_frozen_once_committed" {
			t.Fatalf("refused by %q, want order_lines_frozen_once_committed: %v", name, err)
		}
	})

	t.Run("money frozen", func(t *testing.T) {
		err := run(t, `UPDATE orders SET discount_cents = 0, tax_cents = 999
			WHERE id = '`+zeroOwed+`';`)
		if err == nil {
			t.Fatal("rewrote the money on a committed zero-owed order")
		}
		if _, name := constraintViolation(err); name != "orders_money_frozen_once_committed" {
			t.Fatalf("refused by %q, want orders_money_frozen_once_committed: %v", name, err)
		}
	})

	t.Run("held stock not released", func(t *testing.T) {
		ctx := t.Context()
		tx, err := schemaPool(t).Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, fixtures); err != nil {
			t.Fatalf("fixtures: %v", err)
		}

		var held string
		if err := tx.QueryRow(ctx,
			`SELECT hold_inventory($1, '44444444-4444-4444-8444-444444444444',
				1, now() + interval '15 min', 'hold-zero-owed-1')`, zeroOwed).Scan(&held); err != nil {
			t.Fatalf("hold: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT release_reservation($1)`, held); err == nil {
			t.Fatal("released the hold on a committed zero-owed order — sold stock back on the shelf")
		} else if _, name := constraintViolation(err); name != "inventory_reservation_committed_no_release" {
			t.Fatalf("refused by %q, want inventory_reservation_committed_no_release: %v", name, err)
		}
	})
}

// TestCaptureMustMatchOrderTotal refuses an underpay and an overpay and accepts the exact total.
// The fixture's paid order already carries a matching capture, so this uses the unpaid one.
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

// TestGoenAppCannotEraseWebhookLedger proves the app cannot rewrite the dedupe ledger, which
// would let a resent event be processed twice, but can still stamp when it processed one.
func TestGoenAppCannotEraseWebhookLedger(t *testing.T) {
	if goenAppHasTablePriv(t, "payment_webhook_events", "DELETE") {
		t.Error("store can DELETE payment_webhook_events; a resent event could be replayed")
	}
	// UPDATE at table level is revoked; only the processed_at column is granted.
	var canProcessed, canPayload bool
	if err := schemaPool(t).QueryRow(t.Context(),
		`SELECT has_column_privilege('store', 'payment_webhook_events', 'processed_at', 'UPDATE'),
		        has_column_privilege('store', 'payment_webhook_events', 'payload', 'UPDATE')`).
		Scan(&canProcessed, &canPayload); err != nil {
		t.Fatalf("has_column_privilege: %v", err)
	}
	if !canProcessed {
		t.Error("store cannot stamp processed_at; it needs to mark an event handled")
	}
	if canPayload {
		t.Error("store can rewrite the webhook payload; the raw evidence must be immutable")
	}
}

// creditedOrder builds SQL for a pending order of `total` funded entirely by store credit. It
// must run inside one transaction, so the deferred orders_have_lines check is never reached.
func creditedOrder(order, account, spend string, total int) string {
	return fmt.Sprintf(`
		INSERT INTO shipping_methods (id, code) VALUES ('11110f00-0000-4000-8000-000000000001','cc_home')
		ON CONFLICT DO NOTHING;
		INSERT INTO shipping_method_versions (id, method_id, name, fee_cents)
		VALUES ('11110f01-0000-4000-8000-000000000001','11110f00-0000-4000-8000-000000000001','宅配到府',0)
		ON CONFLICT DO NOTHING;
		INSERT INTO orders (id, shipping_version_id, shipping_method_code, shipping_method_name, shipping_cents)
		VALUES ('%[1]s','11110f01-0000-4000-8000-000000000001','cc_home','宅配到府',0);
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ('%[1]s','X','商品',%[4]d,1);
		INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
		VALUES ('%[1]s','r@example.com','王','0912345678','106','台北市','大安區','路 1 號');
		INSERT INTO store_credit_accounts (id) VALUES ('%[2]s');
		INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
		VALUES ('%[2]s',%[4]d,'grant','%[3]s-grant');
		INSERT INTO store_credit_entries (id, account_id, amount_cents, reason, idempotency_key, order_id)
		VALUES ('%[3]s','%[2]s',-%[4]d,'spent at checkout','%[3]s-spend','%[1]s');`,
		order, account, spend, total)
}

// TestReversedCreditDoesNotFundOrder: a spend later reversed must not count toward funding, and
// a capture net of that ghost must be refused. The formula pairs each spend with its reversal.
func TestReversedCreditDoesNotFundOrder(t *testing.T) {
	order := "11110040-0000-4000-8000-000000000001"
	account := "11110041-0000-4000-8000-000000000001"
	spend := "11110042-0000-4000-8000-000000000001"
	build := creditedOrder(order, account, spend, 100000) + `
		INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key, reverses_id)
		VALUES ('` + account + `',100000,'checkout abandoned','` + spend + `-rev','` + spend + `');`

	t.Run("cannot leave pending", func(t *testing.T) {
		err := run(t, build+`UPDATE orders SET fulfillment_status='picking' WHERE id='`+order+`';`)
		if err == nil {
			t.Fatal("a reversed-credit order left pending as if funded")
		}
		if _, name := constraintViolation(err); name != "orders_funded_to_leave_pending" {
			t.Fatalf("refused by %q, want orders_funded_to_leave_pending: %v", name, err)
		}
	})

	t.Run("capture net of the ghost is refused", func(t *testing.T) {
		// The credit is back on the account, so a capture of 1 assuming the ghost must be refused.
		err := run(t, build+`INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents, captured_amount_cents, paid_at)
			VALUES ('`+order+`','pi_ghost','succeeded',1,1,now());`)
		if err == nil {
			t.Fatal("a capture net of the ghost credit was accepted")
		}
		if _, name := constraintViolation(err); name != "payments_capture_matches_order" {
			t.Fatalf("refused by %q, want payments_capture_matches_order: %v", name, err)
		}
	})
}

// TestCreditPostingRespectsOrderState proves store credit cannot be posted to an order out of turn.
func TestCreditPostingRespectsOrderState(t *testing.T) {
	order := "11110045-0000-4000-8000-000000000001"
	account := "11110046-0000-4000-8000-000000000001"
	spend := "11110047-0000-4000-8000-000000000001"

	// The credit fully funds the order, so it moves into fulfilment with no payment; reversing the
	// spend afterwards would un-fund a picked order.
	err := run(t, creditedOrder(order, account, spend, 100000)+`
		UPDATE orders SET fulfillment_status='picking' WHERE id='`+order+`';
		INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key, reverses_id)
		VALUES ('`+account+`',100000,'support reversal','`+spend+`-rev','`+spend+`');`)
	if err == nil {
		t.Fatal("store credit funding a picked order was reversed, un-funding it")
	}
	if _, name := constraintViolation(err); name != "store_credit_posting_matches_order" {
		t.Fatalf("refused by %q, want store_credit_posting_matches_order: %v", name, err)
	}
}

// TestCreditReversalCannotRacePastFulfilment: one writer moves a store-credited order into
// fulfilment while another reverses the credit. They share no lock unless the reversal takes it.
func TestCreditReversalCannotRacePastFulfilment(t *testing.T) {
	order := "11110050-0000-4000-8000-000000000001"
	account := "11110051-0000-4000-8000-000000000001"
	spend := "11110052-0000-4000-8000-000000000001"
	setup(t, creditedOrder(order, account, spend, 100000))
	t.Cleanup(func() {
		mustExec(t, `DELETE FROM store_credit_entries WHERE account_id = $1`, account)
		mustExec(t, `DELETE FROM store_credit_accounts WHERE id = $1`, account)
		mustExec(t, `DELETE FROM order_private_data WHERE order_id = $1`, order)
		mustExec(t, `DELETE FROM order_lines WHERE order_id = $1`, order)
		mustExec(t, `DELETE FROM orders WHERE id = $1`, order)
	})

	err1, err2 := raceOutcome(t,
		`UPDATE orders SET fulfillment_status='picking' WHERE id='`+order+`'`,
		`INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key, reverses_id)
		 VALUES ('`+account+`',100000,'checkout abandoned','`+spend+`-rev','`+spend+`')`)
	requireExactlyOne(t, "a picked order whose funding credit was reversed", err1, err2)
}

// TestReversalCannotBeReversed keeps reversals one layer deep: reversing a reversal nets back to
// the spend while escaping the order-state rules, since a reversal carries no order_id.
func TestReversalCannotBeReversed(t *testing.T) {
	account := "11110055-0000-4000-8000-000000000001"
	spend := "11110056-0000-4000-8000-000000000001"
	rev := "11110057-0000-4000-8000-000000000001"
	err := run(t, `
		INSERT INTO store_credit_accounts (id) VALUES ('`+account+`');
		INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
		VALUES ('`+account+`',100000,'grant','1l-grant');
		INSERT INTO store_credit_entries (id, account_id, amount_cents, reason, idempotency_key)
		VALUES ('`+spend+`','`+account+`',-50000,'spend','1l-spend');
		INSERT INTO store_credit_entries (id, account_id, amount_cents, reason, idempotency_key, reverses_id)
		VALUES ('`+rev+`','`+account+`',50000,'reverse the spend','1l-rev','`+spend+`');
		INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key, reverses_id)
		VALUES ('`+account+`',-50000,'reverse the reversal','1l-rev2','`+rev+`');`)
	if err == nil {
		t.Fatal("a reversal was itself reversed")
	}
	if _, name := constraintViolation(err); name != "store_credit_reversal_single_layer" {
		t.Fatalf("refused by %q, want store_credit_reversal_single_layer: %v", name, err)
	}
}

// TestOrderCannotLeavePendingUnfunded: an unpaid order cannot move from pending to picking, a
// paid one can, and cancelling from pending is always allowed. The zero-owed third case is
// funded with no payment row at all and is held by [TestZeroOwedOrderIsCommitted].
func TestOrderCannotLeavePendingUnfunded(t *testing.T) {
	err := run(t, `UPDATE orders SET fulfillment_status = 'picking'
	               WHERE id = '6666aaaa-6666-4666-8666-666666666666';`)
	if err == nil {
		t.Fatal("an unpaid order left pending into fulfilment")
	}
	if _, name := constraintViolation(err); name != "orders_funded_to_leave_pending" {
		t.Fatalf("refused by %q, want orders_funded_to_leave_pending: %v", name, err)
	}

	if err := run(t, `UPDATE orders SET fulfillment_status = 'picking'
	                  WHERE id = '66666666-6666-4666-8666-666666666666';`); err != nil {
		t.Fatalf("a funded order was refused fulfilment: %v", err)
	}

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
	// Cleanups run after t.Context() is cancelled, so a plain one fails and leaves rows behind.
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

// TestDeactivatingTheLastVariantIsRefused reaches the same bad state from the variant side, the
// direction a back office actually reaches it from. The rule table covers the publish side.
func TestDeactivatingTheLastVariantIsRefused(t *testing.T) {
	err := run(t, `SET CONSTRAINTS product_variants_keep_product_sellable IMMEDIATE;
		UPDATE product_variants SET is_active = false
		WHERE product_id = '33333333-3333-4333-8333-333333333333';`)
	if err == nil {
		t.Fatal("the last variant of a published product was deactivated; the " +
			"product is now active with no price and every listing drops it")
	}
	if _, name := constraintViolation(err); name != "products_active_has_variant" {
		t.Fatalf("refused by %q, want products_active_has_variant: %v", name, err)
	}

	// The control: a guard refusing every deactivation would pass the assertion above.
	if err := run(t, `SET CONSTRAINTS product_variants_keep_product_sellable IMMEDIATE;
		INSERT INTO product_variants (product_id, sku, price_cents, position)
		VALUES ('33333333-3333-4333-8333-333333333333', 'SPARE-1', 100000, 90);
		UPDATE product_variants SET is_active = false
		WHERE sku = 'SPARE-1';`); err != nil {
		t.Errorf("deactivating one of two variants was refused: %v", err)
	}
}

// TestDeletingTheLastVariantIsRefused covers the DELETE arm, where the trigger has no NEW record
// at all: referring to NEW there is a runtime error plpgsql cannot catch at definition time.
func TestDeletingTheLastVariantIsRefused(t *testing.T) {
	// A product of its own, because deleting the fixture's variant is refused by
	// order_lines_frozen_once_committed first, and any-refusal would report the wrong guard.
	err := run(t, `SET CONSTRAINTS ALL IMMEDIATE;
		INSERT INTO products (id, brand_id, category_id, slug, name, description, status, published_at)
		VALUES ('3333dddd-3333-4333-8333-333333333333',
		        '11111111-1111-4111-8111-111111111111',
		        '22222222-2222-4222-8222-222222222222',
		        'delete-probe', '刪除探針', '', 'draft', NULL);
		INSERT INTO product_variants (product_id, sku, price_cents, position)
		VALUES ('3333dddd-3333-4333-8333-333333333333', 'DELETE-PROBE-1', 100000, 0);
		UPDATE products SET status = 'active', published_at = now()
		WHERE id = '3333dddd-3333-4333-8333-333333333333';
		DELETE FROM product_variants WHERE sku = 'DELETE-PROBE-1';`)
	if err == nil {
		t.Fatal("every variant of a published product was deleted and the product " +
			"stayed active")
	}
	if _, name := constraintViolation(err); name != "products_active_has_variant" {
		t.Fatalf("refused by %q, want products_active_has_variant: %v", name, err)
	}
}

// TestEveryRoleCanReadWhatItsQueriesRead proves a missing GRANT cannot hide behind the owner,
// who is subject to no REVOKE — the hole that made `store` unable to read committed_orders,
// which order_lines' trigger calls, so every checkout returned 500.
func TestEveryRoleCanReadWhatItsQueriesRead(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		role string
		stmt string
		why  string
	}{
		{"store", `SELECT count(*) FROM committed_orders`,
			"order_lines' trigger calls order_is_committed(), which reads this view"},
		{"admin", `SELECT count(*) FROM committed_orders`,
			"the back office's reports join it"},
		{"reporting", `SELECT count(*) FROM committed_orders`,
			"a dashboard reads order history through it"},
		{"store", `SELECT count(*) FROM loyalty_balances`,
			"the points page reads a customer's balance"},
		{"store", `SELECT count(*) FROM product_copurchases`,
			"the PDP reads its recommendations"},
		{"admin", `SELECT count(*) FROM product_copurchases`,
			"the back office shows what sells together"},
		{"store", `SELECT count(*) FROM media_objects`,
			"the storefront serves uploaded images"},
	}
	for _, tt := range tests {
		t.Run(tt.role+" "+firstRelation(tt.stmt), func(t *testing.T) {
			conn, err := schemaPool(t).Acquire(ctx)
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			defer conn.Release()

			if _, err := conn.Exec(ctx, `SET ROLE `+pgx.Identifier{tt.role}.Sanitize()); err != nil {
				t.Fatalf("set role %s: %v", tt.role, err)
			}
			// RESET before release, or the pooled connection hands the role to whatever runs next.
			defer func() {
				if _, resetErr := conn.Exec(ctx, `RESET ROLE`); resetErr != nil {
					t.Errorf("reset role: %v", resetErr)
				}
			}()

			if _, err := conn.Exec(ctx, tt.stmt); err != nil {
				t.Errorf("%s cannot run %q: %v\n  it needs to, because %s",
					tt.role, tt.stmt, err, tt.why)
			}
		})
	}
}

// firstRelation names the table or view a statement reads, for the subtest.
func firstRelation(stmt string) string {
	fields := strings.Fields(stmt)
	for i, f := range fields {
		if strings.EqualFold(f, "FROM") && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return "query"
}

// TestEveryRaisedRuleIsAssertedByName asks the question TestEveryRuleTriggerIsExercised
// cannot. That one keys on the TRIGGER, and a trigger function raises as many
// distinct rules as it has branches: store_credit_guard alone raises four.
// Deleting one branch removes no trigger, so the coverage guard stays green while
// the rule it names stops being enforced — and a case that asserts a row was
// refused, without asking which rule refused it, cannot tell the difference (#8).
//
// The corpus is pg_proc, not a list: a rule added to a function body is covered
// the moment it is written. What it asks for is the name inside a Go string
// literal in a test, because a name in a comment is how a guard comes to be
// satisfied by nothing.
func TestEveryRaisedRuleIsAssertedByName(t *testing.T) {
	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT DISTINCT m[1]
		FROM pg_proc p
		CROSS JOIN LATERAL regexp_matches(p.prosrc, $$CONSTRAINT\s*=\s*'([a-z_]+)'$$, 'g') AS m
		WHERE p.pronamespace = 'public'::regnamespace
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("read the raised rules: %v", err)
	}
	defer rows.Close()

	var raised []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		raised = append(raised, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(raised) == 0 {
		t.Fatal("no raised rules found, so this test is asking nothing")
	}

	asserted := make(map[string]bool, len(raised))
	root := filepath.Join("..", "..")
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, readErr := os.ReadFile(path) //nolint:gosec // G304: paths come from walking this repository
		if readErr != nil {
			return readErr
		}
		for _, name := range raised {
			if bytes.Contains(body, []byte(`"`+name+`"`)) {
				asserted[name] = true
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk the tests: %v", walkErr)
	}

	var missing []string
	for _, name := range raised {
		if !asserted[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d rules are raised by a function body and asserted by no test:\n  %s\n"+
			"Each is a branch that can be deleted with every suite still green.",
			len(missing), strings.Join(missing, "\n  "))
	}
}
