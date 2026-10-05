//go:build integration

package reports_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/reports"
	"github.com/koopa0/goen/internal/pgtx"
)

func TestRevenueCountsOnlyCommittedOrders(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool)

	before, err := s.Report(ctx, 30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	unpaid := reportOrder(t, 100000, false)
	_ = unpaid
	mid, err := s.Report(ctx, 30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if mid.RevenueCents != before.RevenueCents {
		t.Errorf("an unpaid order moved revenue from %d to %d",
			before.RevenueCents, mid.RevenueCents)
	}
	if mid.Placed != before.Placed+1 {
		t.Errorf("placed went %d → %d, want one more", before.Placed, mid.Placed)
	}

	reportOrder(t, 100000, true)
	after, err := s.Report(ctx, 30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if after.RevenueCents != before.RevenueCents+100000 {
		t.Errorf("revenue went %d → %d, want +100000",
			before.RevenueCents, after.RevenueCents)
	}
}

func TestBestSellerHistorySurvivesRetirementOfAPurchasedVariant(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	slug := "report-retired-" + uuid.NewString()
	var productID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, $1, '退役規格報表商品', 'draft', now()
		FROM brands b CROSS JOIN categories c
		WHERE c.parent_id IS NULL ORDER BY b.id, c.id LIMIT 1
		RETURNING id`, slug).Scan(&productID); err != nil {
		t.Fatalf("create product: %v", err)
	}
	var purchasedVariant uuid.UUID
	for pos := range 2 {
		var variantID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO product_variants (product_id, sku, price_cents, position)
			VALUES ($1, 'REPORT-' || upper(replace(gen_random_uuid()::text, '-', '')), 1, $2)
			RETURNING id`, productID, pos).Scan(&variantID); err != nil {
			t.Fatalf("create variant %d: %v", pos, err)
		}
		if pos == 0 {
			purchasedVariant = variantID
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE products SET status = 'active' WHERE id = $1`, productID); err != nil {
		t.Fatalf("publish: %v", err)
	}
	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id`).Scan(&orderID); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines
			(order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, 1, 999 FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $2`,
		orderID, purchasedVariant); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'report@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	providerRef := "report_retired_" + orderID.String()
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 999)`, orderID, providerRef); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 999, NULL, NULL)`, providerRef); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	assertSeller := func(stage string) {
		t.Helper()
		view, err := s.Report(ctx, 30)
		if err != nil {
			t.Fatalf("%s report: %v", stage, err)
		}
		for _, seller := range view.Sellers {
			if seller.Slug == slug {
				if seller.Units != 999 || seller.RevenueCents != 999 {
					t.Errorf("%s seller totals = %d/%d, want 999/999",
						stage, seller.Units, seller.RevenueCents)
				}
				return
			}
		}
		t.Fatalf("%s report lost seller %q", stage, slug)
	}
	assertSeller("before retirement")
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET is_active=false WHERE id=$1`, purchasedVariant); err != nil {
		t.Fatalf("retire purchased variant: %v", err)
	}
	var retainedVariant, retainedProduct uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT variant_id, product_id FROM order_lines
		WHERE order_id=$1`, orderID).
		Scan(&retainedVariant, &retainedProduct); err != nil {
		t.Fatalf("read durable purchased identity: %v", err)
	}
	if retainedVariant != purchasedVariant || retainedProduct != productID {
		t.Fatalf("retirement changed durable identity to variant=%s product=%s, want %s/%s",
			retainedVariant, retainedProduct, purchasedVariant, productID)
	}
	var active bool
	if err := pool.QueryRow(ctx,
		`SELECT is_active FROM product_variants WHERE id=$1`, purchasedVariant).Scan(&active); err != nil {
		t.Fatalf("read retired variant: %v", err)
	}
	if active {
		t.Fatal("purchased variant remained active after retirement")
	}
	assertSeller("after retirement")
}

func TestTheWindowIsAnAllowlist(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool)

	for _, days := range []int32{7, 30, 90} {
		view, err := s.Report(ctx, days)
		if err != nil {
			t.Fatalf("report(%d): %v", days, err)
		}
		if view.Days != int(days) {
			t.Errorf("report(%d) reported %d days", days, view.Days)
		}
	}
	for _, days := range []int32{0, 1, 31, 3650, -1} {
		view, err := s.Report(ctx, days)
		if err != nil {
			t.Fatalf("report(%d): %v", days, err)
		}
		if view.Days != int(reports.DefaultWindow) {
			t.Errorf("report(%d) reported %d days, want the default %d",
				days, view.Days, reports.DefaultWindow)
		}
	}

	view, err := s.Report(ctx, reports.DefaultWindow)
	if err != nil {
		t.Fatalf("report for window snapshot: %v", err)
	}
	wantWindows := []int32{7, 30, 90}
	if !slices.Equal(view.Windows, wantWindows) {
		t.Fatalf("report windows = %v, want %v", view.Windows, wantWindows)
	}
	view.Windows[0] = 3650

	fresh, err := s.Report(ctx, 7)
	if err != nil {
		t.Fatalf("report after mutating prior view: %v", err)
	}
	if fresh.Days != 7 {
		t.Errorf("mutating a report view changed validation: report(7) used %d days", fresh.Days)
	}
	if !slices.Equal(fresh.Windows, wantWindows) {
		t.Errorf("mutating a report view changed the next windows to %v, want %v",
			fresh.Windows, wantWindows)
	}
}

func reportOrder(t *testing.T, cents int64, paid bool) uuid.UUID {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id`).Scan(&orderID); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'r@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("delivery details: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'REP-SKU', '報表測試', $2, 1)`, orderID, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if paid {
		ref := "rep_" + orderID.String()
		if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, $3::bigint)`,
			orderID, ref, cents); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		if _, err := pool.Exec(ctx, `SELECT capture_payment($1, $2::bigint, NULL, NULL)`,
			ref, cents); err != nil {
			t.Fatalf("capture: %v", err)
		}
	}
	return orderID
}

func TestCommittedCoversAnOrderWithNoPaymentRow(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool)

	before, err := s.Report(ctx, 30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	orderID := reportOrder(t, 0, false)
	if _, advErr := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); advErr != nil {
		t.Fatalf("advance: %v", advErr)
	}

	var payments int
	if countErr := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE order_id = $1`, orderID).Scan(&payments); countErr != nil {
		t.Fatalf("count payments: %v", countErr)
	}
	if payments != 0 {
		t.Fatalf("the fixture wrote %d payments; this case is about an order with none", payments)
	}

	after, err := s.Report(ctx, 30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if after.Committed != before.Committed+1 {
		t.Errorf("committed went %d → %d; an order with no payment row was not "+
			"counted, which is the store-credit hole", before.Committed, after.Committed)
	}
	if after.RevenueCents != before.RevenueCents {
		t.Errorf("a zero-owed order moved revenue from %d to %d",
			before.RevenueCents, after.RevenueCents)
	}
}

func TestRunwayDividesWhatASaleMayTake(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool)

	roomy := soldVariant(t, 9, 5, 10)
	atSafety := soldVariant(t, 5, 5, 10)

	view, err := s.Report(ctx, 30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	rowOf := func(sku string) (int, bool) {
		for i, r := range view.AtRisk {
			if r.SKU == sku {
				return i, true
			}
		}
		return 0, false
	}
	roomyAt, ok := rowOf(roomy)
	if !ok {
		t.Fatalf("report lacks %s", roomy)
	}
	atSafetyAt, ok := rowOf(atSafety)
	if !ok {
		t.Fatalf("report lacks %s", atSafety)
	}
	if got := view.AtRisk[roomyAt]; got.DaysCover != 12 || got.Sellable != 4 {
		t.Errorf("stock 9, safety 5, 10 sold in 30 days: days %d sellable %d, want 12 and 4",
			got.DaysCover, got.Sellable)
	}
	if got := view.AtRisk[atSafetyAt]; got.DaysCover != 0 || got.Sellable != 0 {
		t.Errorf("stock at its safety level: days %d sellable %d, want 0 and 0",
			got.DaysCover, got.Sellable)
	}
	if atSafetyAt > roomyAt {
		t.Errorf("a SKU at its safety level is row %d, after the SKU with days to spare at row %d",
			atSafetyAt, roomyAt)
	}
}

// soldVariant makes an active variant holding stock with the given safety level
// and one paid order of sold units, and returns its SKU.
func soldVariant(t *testing.T, stock, safety, sold int) string {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer pgtx.Rollback(ctx, tx)

	var productID, variantID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, 'runway-' || gen_random_uuid(), '庫存天數商品', 'draft', now()
		FROM brands b CROSS JOIN categories c
		WHERE c.parent_id IS NULL ORDER BY b.id, c.id LIMIT 1
		RETURNING id`).Scan(&productID); err != nil {
		t.Fatalf("create product: %v", err)
	}
	var sku string
	if err := tx.QueryRow(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, position, stock_quantity, safety_stock)
		VALUES ($1, 'RUNWAY-' || upper(replace(gen_random_uuid()::text, '-', '')), 1, 0, $2, $3)
		RETURNING id, sku`, productID, stock, safety).Scan(&variantID, &sku); err != nil {
		t.Fatalf("create variant: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE products SET status = 'active' WHERE id = $1`, productID); err != nil {
		t.Fatalf("publish: %v", err)
	}
	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id`).Scan(&orderID); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, $2, $3, '庫存天數商品', 1, $4)`, orderID, variantID, sku, sold); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'report@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	ref := "runway_" + orderID.String()
	if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, $3::bigint)`, orderID, ref, int64(sold)); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT capture_payment($1, $2::bigint, NULL, NULL)`, ref, int64(sold)); err != nil {
		t.Fatalf("capture: %v", err)
	}
	return sku
}
