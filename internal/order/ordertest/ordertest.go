//go:build integration

// Package ordertest is what the integration suites of the order's packages
// share: orders in a stated state, written with SQL, and the variants they hold.
// Every file carries the integration tag, so no production build and no untagged
// tool sees it, and only test files import it.
//
// An order placed through the real checkout is checkouttest's.
package ordertest

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

// HeldOrder writes an order holding one unit of vid whose hold expired `ago` in
// the past. paid decides whether it is funded.
func HeldOrder(t *testing.T, pool *pgxpool.Pool, vid uuid.UUID, ago time.Duration, paid bool) (orderID uuid.UUID) {
	t.Helper()
	return HeldOrderFor(t, pool, uuid.NullUUID{}, vid, ago, paid)
}

// HeldOrderFor is HeldOrder placed by owner, or by a guest when owner is not
// valid.
func HeldOrderFor(t *testing.T, pool *pgxpool.Pool, owner uuid.NullUUID, vid uuid.UUID, ago time.Duration, paid bool) (orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer db.Rollback(ctx, tx)

	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, owner).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, 100000, 1 FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $2`, orderID, vid); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'w@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	// Held normally, then aged — BOTH timestamps move, because expires_at >
	// created_at is a CHECK and a hold that expired an hour ago was taken before it.
	if _, err := tx.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		orderID, vid, "sweep:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_reservations
		SET created_at = now() - $2::interval - interval '30 minutes',
		    expires_at = now() - $2::interval
		WHERE order_id = $1`, orderID, ago.String()); err != nil {
		t.Fatalf("age the hold: %v", err)
	}
	if paid {
		if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`,
			orderID, "cs_sweep_"+number); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`,
			"cs_sweep_"+number); err != nil {
			t.Fatalf("capture: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return orderID
}

// CreditFundedHeldOrder writes an order holding one unit of vid, paid entirely
// from store credit, whose hold expired `ago` in the past.
func CreditFundedHeldOrder(t *testing.T, pool *pgxpool.Pool, vid uuid.UUID, ago time.Duration) (orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	// The line price IS the order total, which is what makes order_amount_after_credit come
	// to exactly zero once the credit is spent.
	const cents = 100000
	userID := CreditedCustomer(t, pool, cents)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer db.Rollback(ctx, tx)

	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, $3, 1 FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $2`, orderID, vid, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'credit-sweep@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		orderID, vid, "sweep-credit:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_reservations
		SET created_at = now() - $2::interval - interval '30 minutes',
		    expires_at = now() - $2::interval
		WHERE order_id = $1`, orderID, ago.String()); err != nil {
		t.Fatalf("age the hold: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT post_store_credit($1, $2, NULL, $3, $4, NULL)`,
		userID, -int64(cents), orderID, "order:"+orderID.String()); err != nil {
		t.Fatalf("spend credit: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the fixture: %v", err)
	}
	return orderID
}

// CreditedCustomer makes a user with a store-credit balance and returns their id.
func CreditedCustomer(t *testing.T, pool *pgxpool.Pool, cents int64) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('credit-'||gen_random_uuid()||'@example.com', 'customer', '額度測試')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if cents > 0 {
		if _, err := pool.Exec(ctx,
			`SELECT post_store_credit($1, $2, '測試發放', NULL, $3, NULL)`,
			id, cents, "grant:"+id.String()); err != nil {
			t.Fatalf("grant credit: %v", err)
		}
	}
	return id
}

// NumberOf is an order's customer-facing number.
func NumberOf(t *testing.T, pool *pgxpool.Pool, orderID uuid.UUID) string {
	t.Helper()
	var number string
	if err := pool.QueryRow(t.Context(),
		`SELECT order_number FROM orders WHERE id = $1`, orderID).Scan(&number); err != nil {
		t.Fatalf("read order number: %v", err)
	}
	return number
}

// StockOf is a variant's shelf quantity.
func StockOf(t *testing.T, pool *pgxpool.Pool, vid uuid.UUID) int32 {
	t.Helper()
	var n int32
	if err := pool.QueryRow(t.Context(),
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&n); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return n
}

// OrderOfVariants places a committed order holding the given variants.
func OrderOfVariants(t *testing.T, pool *pgxpool.Pool, want map[uuid.UUID]int32) string {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer db.Rollback(ctx, tx)

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	position := 0
	for vid, qty := range want {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_lines (order_id, variant_id, sku, product_name,
			                         unit_price_cents, quantity, position)
			SELECT $1, pv.id, pv.sku, p.name, pv.price_cents, $3, $4
			FROM product_variants pv JOIN products p ON p.id = pv.product_id
			WHERE pv.id = $2`, orderID, vid, qty, position); err != nil {
			t.Fatalf("create line: %v", err)
		}
		position++
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'reorder@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

// FreshVariant creates a product of this test's own with one sellable variant.
// Tests that hold, retire or empty stock must not share one: test-integration
// shuffles, so a shared variant fails whenever the order changes.
func FreshVariant(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	a, _, _ := VariantsOf(t, pool, slug, 1)
	return a
}

// ThreeVariants is FreshVariant with three.
func ThreeVariants(t *testing.T, pool *pgxpool.Pool, slug string) (a, b, c uuid.UUID) {
	t.Helper()
	return VariantsOf(t, pool, slug, 3)
}

// VariantsOf creates one product with n sellable variants.
func VariantsOf(t *testing.T, pool *pgxpool.Pool, name string, n int) (a, b, c uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	// A unique slug per CALL, not per test: products_slug_key would refuse the
	// second product of a test that needs two.
	slug := name + "-" + uuid.NewString()[:8]

	var productID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, $1, $1, 'draft', NULL
		FROM brands b, categories c
		ORDER BY b.id, c.id LIMIT 1
		RETURNING id`, slug).Scan(&productID); err != nil {
		t.Fatalf("create product %s: %v", slug, err)
	}

	ids := make([]uuid.UUID, 3)
	for i := range n {
		var vid uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO product_variants (product_id, sku, price_cents, is_active, position)
			VALUES ($1, $2, 199900, true, $3) RETURNING id`,
			// product_variants_sku_format wants upper case and hyphens.
			productID, strings.ToUpper(slug)+"-"+strconv.Itoa(i), i).Scan(&vid); err != nil {
			t.Fatalf("create variant: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`SELECT record_inventory_movement($1, 10, 'adjustment', $2, 'admin', NULL, NULL)`,
			vid, "fixture:"+vid.String()); err != nil {
			t.Fatalf("stock the variant: %v", err)
		}
		ids[i] = vid
	}
	// Published only now: products_active_has_variant is DEFERRED and fires from
	// both sides.
	if _, err := pool.Exec(ctx,
		`UPDATE products SET status = 'active', published_at = now() WHERE id = $1`,
		productID); err != nil {
		t.Fatalf("publish %s: %v", slug, err)
	}
	return ids[0], ids[1], ids[2]
}

// RefundedAndCancelled refunds a paid order in full before shipment and cancels
// it, the one way the schema admits: the card refund has settled.
func RefundedAndCancelled(t *testing.T, pool *pgxpool.Pool, orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	var staff, returnID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role)
		VALUES ('refund-' || gen_random_uuid() || '@goen.invalid', 'staff') RETURNING id`).Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT open_refund_before_shipment(order_number, 'test', $2, 'test')
		FROM orders WHERE id = $1`, orderID, staff).Scan(&returnID); err != nil {
		t.Fatalf("refund before shipment: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO refunds (payment_id, return_request_id, request_key, status, amount_cents,
		                      reason, provider_ref, succeeded_at)
		 SELECT p.id, r.id, 'return:' || r.id, 'succeeded', r.card_refund_cents,
		        r.resolution, 're_' || r.id, now()
		 FROM return_requests r JOIN payments p ON p.order_id = r.order_id AND p.status = 'succeeded'
		 WHERE r.id = $1`,
		`INSERT INTO order_events (order_id, kind, note, return_request_id)
		 SELECT order_id, 'refunded', 're_' || id, id FROM return_requests WHERE id = $1`,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE id = (SELECT order_id FROM return_requests WHERE id = $1)`,
	} {
		if _, err := pool.Exec(ctx, stmt, returnID); err != nil {
			t.Fatalf("settle the refund and cancel: %v", err)
		}
	}
}

// PendingOrderHoldingStock places an unpaid order that holds one unit of a
// variant with stock to spare, and returns the order and that variant.
func PendingOrderHoldingStock(t *testing.T, pool *pgxpool.Pool) (number string, orderID, variantID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.is_active AND p.status = 'active' AND pv.stock_quantity - pv.safety_stock > 2
		LIMIT 1`).Scan(&variantID); err != nil {
		t.Fatalf("find variant: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer db.Rollback(ctx, tx)

	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		SELECT next_order_number(), v.id, sm.code, v.name
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, 100000, 1 FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $2`, orderID, variantID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 's@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		orderID, variantID, "ship-test:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, orderID, variantID
}

// Customer makes a customer with no orders and no store credit and returns their
// id.
func Customer(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('cancel-points-' || gen_random_uuid() || '@goen.invalid', 'customer', '取消點數')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return userID
}

// PaidPickingOrderForUser writes a paid order of one line worth cents for
// userID, with its loyalty points awarded and its fulfilment moved to picking.
func PaidPickingOrderForUser(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, cents int64) (number string, orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer db.Rollback(ctx, tx)

	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'CANCEL-POINTS', '點數取消', $2, 1)`, orderID, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'cancel-points@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`,
		orderID, "cs_cancel_pts_"+number, cents); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`,
		"cs_cancel_pts_"+number, cents); err != nil {
		t.Fatalf("capture: %v", err)
	}
	var awarded int64
	if err := tx.QueryRow(ctx, `SELECT award_loyalty_points($1)`, orderID).Scan(&awarded); err != nil {
		t.Fatalf("award: %v", err)
	}
	if awarded != cents/10000 {
		t.Fatalf("awarded = %d, want %d", awarded, cents/10000)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("to picking: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, orderID
}
