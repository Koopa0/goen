//go:build integration

package admintest

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

// TwoLineOrder is a customer's paid order of two lines. Line A is one unit at
// NT$1,200 and line B three at NT$500; a NT$200 discount and an NT$80 delivery
// fee make the discount share and the delivery rule visible. Each variant has
// exactly the stock the order holds and no safety stock, so after placing it
// both read zero on sale.
type TwoLineOrder struct {
	Number             string
	ID, User           uuid.UUID
	LineA, LineB       uuid.UUID
	VariantA, VariantB uuid.UUID
}

// TwoLineOrderTotal is 120000 + 3 × 50000 − 20000 discount + 8000 delivery.
const TwoLineOrderTotal = 258000

// PartlyShippedOrder is a TwoLineOrder whose first parcel carried A and one
// unit of B, so two units of B are still owed and still held. cardCents of the
// total went by card and the rest by store credit.
func PartlyShippedOrder(t *testing.T, pool *pgxpool.Pool, cardCents int64) TwoLineOrder {
	t.Helper()
	return PaidTwoLineOrder(t, pool, cardCents, true)
}

// PaidTwoLineOrder is a TwoLineOrder in picking, with or without its first
// parcel.
func PaidTwoLineOrder(t *testing.T, pool *pgxpool.Pool, cardCents int64, shipped bool) TwoLineOrder {
	t.Helper()
	ctx := t.Context()
	creditCents := TwoLineOrderTotal - cardCents
	o := TwoLineOrder{User: CreditedAccount(t, pool, creditCents)}
	suffix := uuid.NewString()[:8]

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer db.Rollback(ctx, tx)
	exec := func(stmt string, args ...any) {
		t.Helper()
		if _, execErr := tx.Exec(ctx, stmt, args...); execErr != nil {
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}
	row := func(dest any, stmt string, args ...any) {
		t.Helper()
		if scanErr := tx.QueryRow(ctx, stmt, args...).Scan(dest); scanErr != nil {
			t.Fatalf("%s: %v", stmt, scanErr)
		}
	}
	variant := func(code, name string, price int64, stock int) (uuid.UUID, string) {
		t.Helper()
		slug := "unsent-" + code + "-" + suffix
		var product, id uuid.UUID
		row(&product, `INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		               SELECT b.id, c.id, $1, $2, 'active', now()
		               FROM brands b, categories c WHERE b.slug = 'pixelight' AND c.slug = 'phones'
		               RETURNING id`, slug, name)
		row(&id, `INSERT INTO product_variants (product_id, sku, price_cents, safety_stock, position)
		          VALUES ($1, upper($2), $3, 0, 0) RETURNING id`, product, slug, price)
		exec(`SELECT record_inventory_movement($1, $2, 'receipt', 'seed:' || $3, 'admin', NULL, NULL)`,
			id, stock, slug)
		return id, slug
	}
	var slugA, slugB string
	o.VariantA, slugA = variant("a", "鋼筆", 120000, 1)
	o.VariantB, slugB = variant("b", "點線筆記本", 50000, 3)

	row(&o.ID, `INSERT INTO orders (order_number, user_id, shipping_version_id, shipping_method_code,
	                                shipping_method_name, shipping_cents, discount_cents)
	            SELECT next_order_number(), $1, v.id, sm.code, v.name, 8000, 20000
	            FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
	            WHERE sm.destination_kind = 'address'
	            ORDER BY v.effective_at LIMIT 1
	            RETURNING id`, o.User)
	row(&o.Number, `SELECT order_number FROM orders WHERE id = $1`, o.ID)
	line := func(variantID uuid.UUID, slug, name string, price int64, quantity, position int) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		row(&id, `INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents,
		                                   quantity, position)
		          VALUES ($1, $2, upper($3), $4, $5, $6, $7) RETURNING id`,
			o.ID, variantID, slug, name, price, quantity, position)
		exec(`SELECT hold_inventory($1, $2, $3, interval '30 minutes', 'hold:' || $4)`,
			o.ID, variantID, quantity, slug)
		return id
	}
	o.LineA = line(o.VariantA, slugA, "鋼筆", 120000, 1, 0)
	o.LineB = line(o.VariantB, slugB, "點線筆記本", 50000, 3, 1)
	exec(`INSERT INTO order_private_data (order_id, email, recipient_name, phone,
	                                      postal_code, city, district, street)
	      VALUES ($1, 'unsent-' || $2 || '@example.com', '收件人', '0912345678',
	              '110', '台北市', '信義區', '路 1 號')`, o.ID, suffix)
	payUnshippedOrder(t, tx, o.ID, o.Number, cardCents, creditCents)
	exec(`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, o.ID)
	exec(`SELECT award_loyalty_points($1)`, o.ID)

	if shipped {
		var parcel uuid.UUID
		row(&parcel, `INSERT INTO order_shipments (order_id, carrier, tracking_number)
		              VALUES ($1, 'black_cat', 'UNSENT-' || $2) RETURNING id`, o.ID, suffix)
		for _, sent := range []struct{ line, variant uuid.UUID }{{o.LineA, o.VariantA}, {o.LineB, o.VariantB}} {
			exec(`INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
			      VALUES ($1, $2, $3, 1)`, o.ID, parcel, sent.line)
			exec(`SELECT consume_reservation_partial(r.id, 1) FROM inventory_reservations r
			      WHERE r.order_id = $1 AND r.variant_id = $2 AND r.state = 'held'`, o.ID, sent.variant)
		}
		exec(`UPDATE orders SET fulfillment_status = 'shipped' WHERE id = $1`, o.ID)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the two-line order: %v", err)
	}
	return o
}
