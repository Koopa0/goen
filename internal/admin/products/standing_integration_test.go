//go:build integration

package products_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/shoptime"
)

// standingProduct is a product with one variant, and the ids to order it by.
func standingProduct(t *testing.T) (slug string, productID, variantID uuid.UUID, sku string) {
	t.Helper()
	ctx := t.Context()
	slug = "standing-" + uuid.NewString()
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, $1, '銷售評價商品', 'draft', now()
		FROM brands b CROSS JOIN categories c
		WHERE c.parent_id IS NULL ORDER BY b.id, c.id LIMIT 1
		RETURNING id`, slug).Scan(&productID); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, position, stock_quantity, safety_stock)
		VALUES ($1, 'STAND-' || upper(replace(gen_random_uuid()::text, '-', '')), 1, 0, 100, 0)
		RETURNING id, sku`, productID).Scan(&variantID, &sku); err != nil {
		t.Fatalf("create variant: %v", err)
	}
	return slug, productID, variantID, sku
}

// standingOrder places an order of units of the product at placedAt, paid or not.
func standingOrder(t *testing.T, productID, variantID uuid.UUID, sku string, units int, placedAt time.Time, paid bool) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer pgtx.Rollback(ctx, tx)
	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents, placed_at)
		SELECT next_order_number(), v.id, sm.code, v.name, 0, $1::timestamptz
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id`, placedAt).Scan(&orderID); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'standing@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("delivery details: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, product_id, variant_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, $2, $3, $4, '銷售評價商品', 1, $5)`, orderID, productID, variantID, sku, units); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !paid {
		return orderID
	}
	ref := "standing_" + orderID.String()
	if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, $3::bigint)`, orderID, ref, int64(units)); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT capture_payment($1, $2::bigint, NULL, NULL)`, ref, int64(units)); err != nil {
		t.Fatalf("capture: %v", err)
	}
	return orderID
}

func TestWeeklyUnitsAreThisProductsCommittedLinesOverWholeShopDays(t *testing.T) {
	slug, productID, variantID, sku := standingProduct(t)
	_, otherProduct, otherVariant, otherSKU := standingProduct(t)

	now := time.Now()
	today := shoptime.Midnight(now)
	first := today.AddDate(0, 0, -90)

	early := today.Add(time.Minute)
	if early.After(now) {
		early = now.Add(-time.Second)
	}

	// Counted: today's, and the first day's earliest minutes.
	standingOrder(t, productID, variantID, sku, 3, early, true)
	standingOrder(t, productID, variantID, sku, 2, first.Add(30*time.Minute), true)
	// Not counted: unpaid, another product's, refunded in full before it shipped,
	// and the day before the first.
	standingOrder(t, productID, variantID, sku, 5, early, false)
	standingOrder(t, otherProduct, otherVariant, otherSKU, 11, early, true)
	refunded := standingOrder(t, productID, variantID, sku, 13, early, true)
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO return_requests (order_id, reason, before_shipment) VALUES ($1, '', true)`, refunded); err != nil {
		t.Fatalf("refund before shipment: %v", err)
	}
	standingOrder(t, productID, variantID, sku, 7, first.Add(-time.Minute), true)

	view, err := products.NewStore(pool).Product(t.Context(), slug)
	if err != nil {
		t.Fatalf("Product(%q): %v", slug, err)
	}
	days := view.Sales.Days.Buckets
	if len(days) != 91 {
		t.Fatalf("days = %d, want 91, 13 whole weeks", len(days))
	}
	if got := days[0].Value; got != 2 {
		t.Errorf("first day = %d units, want 2 (a minute after the shop day began)", got)
	}
	if got := days[90].Value; got != 3 {
		t.Errorf("today = %d units, want 3 (unpaid, refunded and other products left out)", got)
	}
	if got := days[90].Day.Format("2006-01-02"); got != shoptime.Day(now) {
		t.Errorf("last day = %s, want today %s", got, shoptime.Day(now))
	}
	var total int64
	for _, d := range days {
		total += d.Value
	}
	if total != 5 {
		t.Errorf("total = %d units, want 5; the day before the window or an uncommitted order was counted", total)
	}
}

func TestTheRatingSpreadCountsOnlyVisibleReviews(t *testing.T) {
	slug, productID, _, _ := standingProduct(t)
	for _, r := range []struct {
		rating int
		hidden bool
	}{{5, false}, {5, false}, {4, false}, {1, true}, {5, true}} {
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO product_reviews (product_id, rating, body, hidden_at)
			VALUES ($1, $2, '評價', CASE WHEN $3 THEN now() END)`, productID, r.rating, r.hidden); err != nil {
			t.Fatalf("create review: %v", err)
		}
	}
	view, err := products.NewStore(pool).Product(t.Context(), slug)
	if err != nil {
		t.Fatalf("Product(%q): %v", slug, err)
	}
	if got := view.Ratings.Count; got != 3 {
		t.Errorf("count = %d, want 3 visible reviews", got)
	}
	if got, want := view.Ratings.Stars, [5]int64{2, 1, 0, 0, 0}; got != want {
		t.Errorf("stars = %v, want %v", got, want)
	}
}
