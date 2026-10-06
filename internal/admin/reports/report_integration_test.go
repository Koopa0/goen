//go:build integration

package reports_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/reports"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
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
	return reportOrderAt(t, cents, paid, nil)
}

// reportOrderAt places the order at placedAt, or now when it is nil.
func reportOrderAt(t *testing.T, cents int64, paid bool, placedAt *time.Time) uuid.UUID {
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
		                    shipping_method_name, shipping_cents, placed_at)
		SELECT next_order_number(), v.id, sm.code, v.name, 0, coalesce($1::timestamptz, now())
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id`, placedAt).Scan(&orderID); err != nil {
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

func TestStockRowsCapSoldOutAndKeepEstimatesListed(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool)

	roomy := soldVariant(t, 9, 5, 10)
	var idle []string
	for range 4 {
		_, sku := newVariant(t, 2, 2)
		idle = append(idle, sku)
	}

	now := time.Now()
	raw, err := db.New(pool).StockAtRisk(ctx, db.StockAtRiskParams{
		FromAt: now.AddDate(0, 0, -30), ToAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("read stock at risk: %v", err)
	}
	for _, sku := range idle {
		if !slices.ContainsFunc(raw, func(r db.StockAtRiskRow) bool { return r.SKU == sku }) {
			t.Errorf("StockAtRisk lacks %s: sold out with no orders, it must still be a candidate", sku)
		}
	}

	view, err := s.Report(ctx, 7)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if view.StockDays != 30 {
		t.Errorf("a 7 day report reads stock over %d days, want 30", view.StockDays)
	}
	var soldOut int
	roomyAt, afterSoldOut := -1, true
	for i, r := range view.AtRisk {
		state := r.Estimate().State
		switch {
		case state == admin.CoverSoldOut:
			soldOut++
			if i > 0 && view.AtRisk[i-1].Estimate().State != admin.CoverSoldOut {
				afterSoldOut = false
			}
		case r.SKU == roomy:
			roomyAt = i
		}
	}
	if soldOut > 3 || view.MoreSoldOut < 1 {
		t.Errorf("%d sold out rows listed with %d left off, want at most 3 and the rest counted", soldOut, view.MoreSoldOut)
	}
	if !afterSoldOut {
		t.Error("a sold out row follows a row that is not sold out")
	}
	if roomyAt < 0 {
		t.Errorf("report lacks %s: sold out rows must not push the others off the list", roomy)
	} else if got := view.AtRisk[roomyAt]; got.Sellable != 4 || got.Sold != 10 || got.Orders != 1 {
		t.Errorf("stock 9, safety 5, one order of 10: sellable %d sold %d orders %d, want 4, 10 and 1",
			got.Sellable, got.Sold, got.Orders)
	}
}

func TestStockWindowIsWholeShopDays(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool)
	now := shopMoment(t, "2024-03-10 15:20:00")

	variantID, sku := newVariant(t, 20, 2)
	for _, placed := range []string{
		"2024-02-09 23:59:00", // before the first of 30 shop days
		"2024-02-10 00:01:00", // inside
		"2024-03-10 15:21:00", // after now
	} {
		moment := shopMoment(t, placed)
		orderOnVariant(t, variantID, sku, 1, &moment)
	}

	view, err := s.ReportAt(ctx, 7, now)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	row, ok := stockRow(view, sku)
	if !ok {
		t.Fatalf("report lacks %s", sku)
	}
	if row.Orders != 1 || row.Sold != 1 {
		t.Errorf("orders at 02-09 23:59, 02-10 00:01 and 03-10 15:21 over 30 shop days to 03-10 15:20: %d orders, %d units, want 1 and 1",
			row.Orders, row.Sold)
	}
}

func TestStockTimeAndSoldOutComeFromTheLedger(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool)
	now := shopMoment(t, "2024-03-10 15:20:00")

	// Stock 10 over safety 2, received on 02-25: nothing to sell before, so the
	// 30 shop days from 02-10 hold 14 days 15 hours 20 minutes of stock.
	inStockID, inStock := newVariant(t, 10, 2)
	receipt := shopMoment(t, "2024-02-25 00:00:00")
	ledger(t, inStockID, 10, "receipt", receipt)
	sold := shopMoment(t, "2024-03-09 10:00:00")
	orderOnVariant(t, inStockID, inStock, 1, &sold)

	// Two sold out variants that ran out on different days: the later is first.
	earlierID, earlier := newVariant(t, 2, 2)
	ledger(t, earlierID, 5, "receipt", shopMoment(t, "2024-03-01 00:00:00"))
	ledger(t, earlierID, -5, "adjustment", shopMoment(t, "2024-03-05 12:00:00"))
	laterID, later := newVariant(t, 2, 2)
	ledger(t, laterID, 5, "receipt", shopMoment(t, "2024-03-01 00:00:00"))
	ledger(t, laterID, -5, "adjustment", shopMoment(t, "2024-03-08 12:00:00"))

	view, err := s.ReportAt(ctx, 30, now)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	row, ok := stockRow(view, inStock)
	if !ok {
		t.Fatalf("report lacks %s", inStock)
	}
	if want := 14*24*time.Hour + 15*time.Hour + 20*time.Minute; row.InStock != want {
		t.Errorf("received on 02-25 into a window from 02-10 to 03-10 15:20: in stock %v, want %v", row.InStock, want)
	}
	laterAt, laterOK := indexOf(view, later)
	earlierAt, earlierOK := indexOf(view, earlier)
	if !laterOK || !earlierOK {
		t.Fatalf("report lacks a sold out variant: %s listed %v, %s listed %v", later, laterOK, earlier, earlierOK)
	}
	if laterAt > earlierAt {
		t.Errorf("the SKU that ran out on 03-08 is row %d, after the one that ran out on 03-05 at row %d", laterAt, earlierAt)
	}
	if got := view.AtRisk[laterAt].SoldOutAt; !got.Equal(shopMoment(t, "2024-03-08 12:00:00")) {
		t.Errorf("ran out at %v, want 2024-03-08 12:00", got)
	}
}

func indexOf(view admin.ReportView, sku string) (int, bool) {
	for i, r := range view.AtRisk {
		if r.SKU == sku {
			return i, true
		}
	}
	return 0, false
}

func stockRow(view admin.ReportView, sku string) (admin.StockRisk, bool) {
	i, ok := indexOf(view, sku)
	if !ok {
		return admin.StockRisk{}, false
	}
	return view.AtRisk[i], true
}

func shopMoment(t *testing.T, wallClock string) time.Time {
	t.Helper()
	moment, err := shoptime.ParseSecond(wallClock)
	if err != nil {
		t.Fatalf("parse %q: %v", wallClock, err)
	}
	return moment
}

// ledger writes a movement as the ledger would have it at the time, without
// touching the stock the variant already holds.
func ledger(t *testing.T, variantID uuid.UUID, delta int, reason string, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO inventory_movements (variant_id, delta, reason, source_type, idempotency_key, created_at)
		VALUES ($1, $2, $3, 'admin', 'report-' || gen_random_uuid(), $4)`,
		variantID, delta, reason, at); err != nil {
		t.Fatalf("write movement: %v", err)
	}
}

// soldVariant makes an active variant holding stock with the given safety level
// and one paid order of sold units, and returns its SKU.
func soldVariant(t *testing.T, stock, safety, sold int) string {
	t.Helper()
	variantID, sku := newVariant(t, stock, safety)
	orderOnVariant(t, variantID, sku, sold, nil)
	return sku
}

// newVariant makes an active variant holding stock with the given safety level.
func newVariant(t *testing.T, stock, safety int) (uuid.UUID, string) {
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
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return variantID, sku
}

// orderOnVariant places one paid order of units on the variant at placedAt, or
// now when it is nil.
func orderOnVariant(t *testing.T, variantID uuid.UUID, sku string, units int, placedAt *time.Time) {
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
		SELECT next_order_number(), v.id, sm.code, v.name, 0, coalesce($1::timestamptz, now())
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id`, placedAt).Scan(&orderID); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, $2, $3, '庫存天數商品', 1, $4)`, orderID, variantID, sku, units); err != nil {
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
	if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, $3::bigint)`, orderID, ref, int64(units)); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT capture_payment($1, $2::bigint, NULL, NULL)`, ref, int64(units)); err != nil {
		t.Fatalf("capture: %v", err)
	}
}

func TestAPeriodIsWholeShopDaysAndThePreviousStopsAtTheSameHour(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool)
	at := func(wallClock string) time.Time {
		t.Helper()
		moment, err := shoptime.ParseSecond(wallClock)
		if err != nil {
			t.Fatalf("parse %q: %v", wallClock, err)
		}
		return moment
	}
	now := at("2024-03-10 15:20:00")
	before, err := s.ReportAt(ctx, 7, now)
	if err != nil {
		t.Fatalf("report before: %v", err)
	}

	for _, order := range []struct {
		placed string
		cents  int64
	}{
		{"2024-03-10 15:19:00", 1},   // this period, just before now
		{"2024-03-10 15:21:00", 2},   // after now: neither
		{"2024-03-04 00:01:00", 4},   // this period: the first shop day's first minutes
		{"2024-03-03 23:59:00", 8},   // neither: before the first day, after the previous period's cut
		{"2024-03-03 15:19:00", 16},  // previous period, a minute before its cut
		{"2024-03-03 15:21:00", 32},  // neither: after the cut
		{"2024-02-26 00:01:00", 64},  // previous period, its first minutes
		{"2024-02-25 23:59:00", 128}, // neither: before the previous period
	} {
		moment := at(order.placed)
		reportOrderAt(t, order.cents, true, &moment)
	}

	// Money going back is windowed by when it landed: a minute either side of
	// the previous period's cut, the earlier one counts and the later does not.
	earlier := at("2024-02-20 10:00:00")
	paid := reportOrderAt(t, 100000, true, &earlier)
	for _, refund := range []struct {
		succeeded string
		cents     int64
	}{
		{"2024-03-03 15:19:00", 256},
		{"2024-03-03 15:21:00", 512},
	} {
		if _, insertErr := pool.Exec(ctx, `
			INSERT INTO refunds (payment_id, request_key, status, amount_cents, provider_ref, succeeded_at)
			SELECT id, 'window-' || $2::bigint::text, 'succeeded', $2::bigint, 're_window_' || $2::bigint::text, $1::timestamptz
			FROM payments WHERE order_id = $3 AND status = 'succeeded'`,
			at(refund.succeeded), refund.cents, paid); insertErr != nil {
			t.Fatalf("refund at %s: %v", refund.succeeded, insertErr)
		}
	}
	var account uuid.UUID
	if accountErr := pool.QueryRow(ctx, `
		WITH u AS (INSERT INTO users (email, role) VALUES ('window-credit@goen.invalid', 'customer') RETURNING id)
		INSERT INTO store_credit_accounts (user_id) SELECT id FROM u RETURNING id`).Scan(&account); accountErr != nil {
		t.Fatalf("credit account: %v", accountErr)
	}
	for _, credit := range []struct {
		created string
		cents   int64
	}{
		{"2024-03-03 15:19:00", 1024},
		{"2024-03-03 15:21:00", 2048},
	} {
		if _, insertErr := pool.Exec(ctx, `
			INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key, order_id, created_at)
			VALUES ($1, $2::bigint, '折讓', 'window-' || $2::bigint::text, $3, $4::timestamptz)`,
			account, credit.cents, paid, at(credit.created)); insertErr != nil {
			t.Fatalf("credit at %s: %v", credit.created, insertErr)
		}
	}

	after, err := s.ReportAt(ctx, 7, now)
	if err != nil {
		t.Fatalf("report after: %v", err)
	}
	if got, want := after.Previous.RefundedCents-before.Previous.RefundedCents, int64(256+1024); got != want {
		t.Errorf("the previous period gained %d refunded cents, want %d: refunds and credits count when they landed, up to the cut", got, want)
	}
	if got, want := after.RevenueCents-before.RevenueCents, int64(1+4); got != want {
		t.Errorf("this period gained %d cents, want %d: it takes 15:19 today and 00:01 on the first day only", got, want)
	}
	if got, want := after.Placed-before.Placed, int64(2); got != want {
		t.Errorf("this period gained %d placed orders, want %d", got, want)
	}
	if got, want := after.Previous.RevenueCents-before.Previous.RevenueCents, int64(16+64); got != want {
		t.Errorf("the previous period gained %d cents, want %d: it ends at 15:20 on 03-03 and starts at 02-26 00:00", got, want)
	}
	if got, want := after.Previous.Placed-before.Previous.Placed, int64(2); got != want {
		t.Errorf("the previous period gained %d placed orders, want %d", got, want)
	}
}
