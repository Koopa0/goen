//go:build integration

package db_test

import (
	"fmt"
	"sort"
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
		reject: `UPDATE orders SET fulfillment_status = 'shipped'
		         WHERE order_number = 'GO-260721-000388';`,
		accept: `UPDATE orders SET fulfillment_status = 'picking'
		         WHERE order_number = 'GO-260721-000388';`,
	},
	{
		rule: "orders_have_lines",
		reject: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name)
		         VALUES ('11110001-0000-4000-8000-000000000001', 'GO-260721-000999', 'home_delivery', '宅配');
		         SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
		accept: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name)
		         VALUES ('11110001-0000-4000-8000-000000000001', 'GO-260721-000999', 'home_delivery', '宅配');
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
		reject: `INSERT INTO return_request_lines (return_request_id, order_line_id, quantity)
		         VALUES ('88880001-0000-4000-8000-000000000000', '66660001-0000-4000-8000-000000000000', 3);`,
		accept: `INSERT INTO return_request_lines (return_request_id, order_line_id, quantity)
		         VALUES ('88880001-0000-4000-8000-000000000000', '66660001-0000-4000-8000-000000000000', 2);`,
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
		rule:   "inventory_never_negative",
		reject: `SELECT record_inventory_movement('44444444-4444-4444-8444-444444444444', -15, 'sale', 'k-over');`,
		accept: `SELECT record_inventory_movement('44444444-4444-4444-8444-444444444444', -14, 'sale', 'k-exact');`,
	},
}

func creditEntry(amount int, key string) string {
	return fmt.Sprintf(
		`INSERT INTO store_credit_entries (user_id, amount_cents, reason, idempotency_key)
		 VALUES ('55555555-5555-4555-8555-555555555555', %d, 'spend', '%s');`, amount, key)
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

	// T2 runs while T1 is still open. It either blocks on T1's lock or does
	// not; the goroutine exists so that blocking does not deadlock the test.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err2 = tx2.Exec(ctx, stmt2)
	}()

	// Long enough that an unblocked T2 has certainly finished, so a test that
	// sees T2 still running knows it is genuinely waiting on a lock.
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
	}

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
		INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name)
		VALUES ('`+order+`','GO-260721-000900','home_delivery','宅配');
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
	setup(t, `
		INSERT INTO users (id, email) VALUES ('`+user+`','race@example.com');
		INSERT INTO store_credit_accounts (user_id) VALUES ('`+user+`');
		INSERT INTO store_credit_entries (user_id, amount_cents, reason, idempotency_key)
		VALUES ('`+user+`', 100000, 'grant', 'race-grant');`)
	t.Cleanup(func() {
		mustExec(t, `DELETE FROM store_credit_entries WHERE user_id = $1`, user)
		mustExec(t, `DELETE FROM store_credit_accounts WHERE user_id = $1`, user)
		mustExec(t, `DELETE FROM users WHERE id = $1`, user)
	})

	err1, err2 := raceOutcome(t,
		`INSERT INTO store_credit_entries (user_id, amount_cents, reason, idempotency_key)
		 VALUES ('`+user+`', -80000, 'spend', 'credit-race-1')`,
		`INSERT INTO store_credit_entries (user_id, amount_cents, reason, idempotency_key)
		 VALUES ('`+user+`', -80000, 'spend', 'credit-race-2')`)
	requireExactlyOne(t, "a balance of NT$1,000 against two spends of NT$800", err1, err2)

	var balance int64
	if err := schemaPool(t).QueryRow(t.Context(),
		`SELECT coalesce(sum(amount_cents), 0) FROM store_credit_entries WHERE user_id = $1`,
		user).Scan(&balance); err != nil {
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

func setup(t *testing.T, stmt string) {
	t.Helper()
	if _, err := schemaPool(t).Exec(t.Context(), stmt); err != nil {
		t.Fatalf("set up: %v", err)
	}
}

func mustExec(t *testing.T, stmt string, args ...any) {
	t.Helper()
	if _, err := schemaPool(t).Exec(t.Context(), stmt, args...); err != nil {
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
