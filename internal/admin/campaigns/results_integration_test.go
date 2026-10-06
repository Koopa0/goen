//go:build integration

package campaigns_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/shoptime"
)

// soldAt places an order of units of the product at the shop-clock moment, paid
// unless unpaid.
func soldAt(t *testing.T, productID uuid.UUID, units int, moment string, unpaid bool) {
	t.Helper()
	ctx := t.Context()
	placed, err := shoptime.ParseSecond(moment)
	if err != nil {
		t.Fatalf("parse %q: %v", moment, err)
	}
	var orderID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents, placed_at)
		SELECT next_order_number(), v.id, sm.code, v.name, 0, $1
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id`, placed).Scan(&orderID); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'daily@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_lines (order_id, product_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, $2, 'DAILY-SKU', '每日件數商品', 1, $3)`, orderID, productID, units); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if unpaid {
		return
	}
	ref := "daily_" + orderID.String()
	if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, $3::bigint)`, orderID, ref, int64(units)); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT capture_payment($1, $2::bigint, NULL, NULL)`, ref, int64(units)); err != nil {
		t.Fatalf("capture: %v", err)
	}
}

func newProduct(t *testing.T) (id uuid.UUID, slug string) {
	t.Helper()
	ctx := t.Context()
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, 'daily-' || gen_random_uuid(), '每日件數商品', 'draft', now()
		FROM brands b CROSS JOIN categories c
		WHERE c.parent_id IS NULL ORDER BY b.id, c.id LIMIT 1
		RETURNING id, slug`).Scan(&id, &slug); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, position, stock_quantity, safety_stock)
		VALUES ($1, 'DAILY-' || upper(replace(gen_random_uuid()::text, '-', '')), 1, 0, 5, 0)`, id); err != nil {
		t.Fatalf("create variant: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET status = 'active' WHERE id = $1`, id); err != nil {
		t.Fatalf("activate product: %v", err)
	}
	return id, slug
}

func TestResultsCountWholeShopDaysAroundTheStartOfTheCampaign(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := campaigns.NewStore(pool)
	slug := admintest.CampaignSlug(t)
	if _, err := s.Create(ctx, &campaigns.Form{Slug: slug, Title: "秋日選物", Days: 7}); err != nil {
		t.Fatal(err)
	}
	if errs, err := s.SetWindow(ctx, slug, "2026-10-05T09:00", "2026-10-12T23:59"); err != nil || len(errs) > 0 {
		t.Fatalf("SetWindow: %v %v", err, errs)
	}
	listed, listedSlug := newProduct(t)
	unlisted, _ := newProduct(t)
	if err := s.FeatureProduct(ctx, slug, listedSlug); err != nil {
		t.Fatal(err)
	}

	soldAt(t, listed, 5, "2026-10-02 00:00:30", false) // the first day counted
	soldAt(t, listed, 7, "2026-10-01 23:59:00", false) // the day before it: not counted
	soldAt(t, listed, 2, "2026-10-04 23:59:00", false) // the day before the start, to the last minute
	soldAt(t, listed, 3, "2026-10-05 00:01:00", false) // the start day, before the campaign's 09:00: still its day
	soldAt(t, listed, 1, "2026-10-05 10:00:00", false)
	soldAt(t, listed, 2, "2026-10-07 15:19:00", false)
	soldAt(t, listed, 9, "2026-10-07 15:21:00", false) // after now
	soldAt(t, unlisted, 4, "2026-10-06 12:00:00", false)
	soldAt(t, listed, 6, "2026-10-06 12:00:00", true) // never paid

	now, err := shoptime.ParseSecond("2026-10-07 15:20:00")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := s.Detail(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Results(ctx, slug, detail, 1, now)
	if err != nil || got == nil {
		t.Fatalf("Results = %v, %v, want the days", got, err)
	}
	var units []int64
	for _, b := range got.Units.Buckets {
		units = append(units, b.Value)
	}
	if want := []int64{5, 0, 2, 4, 0, 2}; !slices.Equal(units, want) || got.Days != 3 || !got.Units.Partial {
		t.Errorf("units a day from Oct 2 = %v over %d days, partial %v; want %v over 3, partial", units, got.Days, got.Units.Partial, want)
	}
	if first := got.Units.Buckets[0].Day.Format(time.DateOnly); first != "2026-10-02" {
		t.Errorf("first day = %s, want 2026-10-02", first)
	}
}
