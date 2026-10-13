//go:build integration

package reports_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/reports"
	stockdesk "github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

func TestRevenueCountsOnlyCommittedOrders(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool)

	before, err := s.ReportAt(ctx, 30, time.Now())
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	unpaid := reportOrder(t, 100000, false)
	_ = unpaid
	mid, err := s.ReportAt(ctx, 30, time.Now())
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
	after, err := s.ReportAt(ctx, 30, time.Now())
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
		view, err := s.ReportAt(ctx, 30, time.Now())
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

// A refund before shipment cancels the sale, so the product leaves the best
// sellers while the order is still committed, as the order leaves the revenue.
func TestBestSellersLeaveOutAnOrderRefundedBeforeShipment(t *testing.T) {
	ctx := t.Context()
	// A week of its own, so no other test's sale outranks this one.
	now := shopMoment(t, "2021-06-20 15:20:00")
	placed := shopMoment(t, "2021-06-17 10:00:00")
	variantID, sku := newVariant(t, 20, 0)
	orderOnVariant(t, variantID, sku, 3, &placed)
	slug := productSlug(t, sku)

	_, staff := admintest.StaffContext(t, pool)
	backOffice := admintest.AdminRolePool(t, pool)
	s := reports.NewStore(backOffice)
	listed := func() bool {
		t.Helper()
		view, err := s.ReportAt(ctx, 7, now)
		if err != nil {
			t.Fatalf("ReportAt: %v", err)
		}
		return slices.ContainsFunc(view.Sellers, func(r admin.Seller) bool { return r.Slug == slug })
	}
	if !listed() {
		t.Fatal("the paid order's product is not a best seller before any refund")
	}

	if _, err := backOffice.Exec(ctx, `
		SELECT open_refund_before_shipment(o.order_number, 'best sellers', $2, 'best-sellers-' || $3)
		FROM orders o WHERE o.id = $1`, orderOf(t, sku, false), staff, sku); err != nil {
		t.Fatalf("refund before shipment: %v", err)
	}
	if listed() {
		t.Errorf("%s is still a best seller after its only order was refunded before shipment", slug)
	}
}

func TestStockCoverLeavesOutAnOrderRefundedBeforeShipment(t *testing.T) {
	ctx := t.Context()
	now := shopMoment(t, "2021-07-20 15:20:00")
	placed := shopMoment(t, "2021-07-17 10:00:00")
	_, staff := admintest.StaffContext(t, pool)
	backOffice := admintest.AdminRolePool(t, pool)
	for _, soldOut := range []bool{false, true} {
		t.Run(map[bool]string{false: "stock available", true: "sold out"}[soldOut], func(t *testing.T) {
			quantity := 20
			if soldOut {
				quantity = 0
			}
			variantID, sku := newVariant(t, quantity, 0)
			orderOnVariant(t, variantID, sku, 3, &placed)
			read := func() (db.StockAtRiskRow, bool) {
				t.Helper()
				rows, err := db.New(backOffice).StockAtRisk(ctx, db.StockAtRiskParams{FromAt: now.AddDate(0, 0, -30), ToAt: now})
				if err != nil {
					t.Fatal(err)
				}
				for i := range rows {
					row := &rows[i]
					if row.VariantID == variantID {
						return *row, true
					}
				}
				return db.StockAtRiskRow{}, false
			}
			if row, found := read(); !found || row.UnitsSold != 3 || row.OrdersSold != 1 {
				t.Fatalf("before refund: found=%v units=%d orders=%d, want true/3/1", found, row.UnitsSold, row.OrdersSold)
			}
			orderID := orderOf(t, sku, false)
			if _, err := backOffice.Exec(ctx, `
				SELECT open_refund_before_shipment(o.order_number, 'stock cover', $2, 'stock-cover-' || $3)
				FROM orders o WHERE o.id = $1`, orderID, staff, sku); err != nil {
				t.Fatal(err)
			}
			var committed bool
			if err := backOffice.QueryRow(ctx, `SELECT order_is_committed($1)`, orderID).Scan(&committed); err != nil || !committed {
				t.Fatalf("fixture must remain committed while payout is outstanding: %v, %v", committed, err)
			}
			if row, found := read(); found != soldOut || row.UnitsSold != 0 || row.OrdersSold != 0 {
				t.Errorf("after refund: found=%v units=%d orders=%d, want %v/0/0", found, row.UnitsSold, row.OrdersSold, soldOut)
			}
			rows, _, err := stockdesk.NewStore(backOffice).DaysCover(ctx, 30, now)
			if err != nil {
				t.Fatal(err)
			}
			for i := range rows {
				row := &rows[i]
				if row.SKU == sku && (row.Sold != 0 || row.Orders != 0) {
					t.Errorf("days cover still counts the cancelled sale: %+v", row)
				}
			}
		})
	}
}

func TestTheWindowIsAnAllowlist(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool)

	for _, days := range []int32{7, 30, 90} {
		view, err := s.ReportAt(ctx, days, time.Now())
		if err != nil {
			t.Fatalf("report(%d): %v", days, err)
		}
		if view.Days != int(days) {
			t.Errorf("report(%d) reported %d days", days, view.Days)
		}
	}
	for _, days := range []int32{0, 1, 31, 3650, -1} {
		view, err := s.ReportAt(ctx, days, time.Now())
		if err != nil {
			t.Fatalf("report(%d): %v", days, err)
		}
		if view.Days != int(reports.DefaultWindow) {
			t.Errorf("report(%d) reported %d days, want the default %d",
				days, view.Days, reports.DefaultWindow)
		}
	}

	view, err := s.ReportAt(ctx, reports.DefaultWindow, time.Now())
	if err != nil {
		t.Fatalf("report for window snapshot: %v", err)
	}
	wantWindows := []int32{7, 30, 90}
	if !slices.Equal(view.Windows, wantWindows) {
		t.Fatalf("report windows = %v, want %v", view.Windows, wantWindows)
	}
	view.Windows[0] = 3650

	fresh, err := s.ReportAt(ctx, 7, time.Now())
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

	before, err := s.ReportAt(ctx, 30, time.Now())
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

	after, err := s.ReportAt(ctx, 30, time.Now())
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

	// One more sold out SKU than the list holds, so the count is at least 1
	// whatever the seed leaves, and a SKU of ten orders that must still show.
	idle := make([]string, 0, 11)
	for range 11 {
		_, sku := newVariant(t, 2, 2)
		idle = append(idle, sku)
	}
	estimatedID, estimated := newVariant(t, 10, 5)
	for range 10 {
		orderOnVariant(t, estimatedID, estimated, 1, nil)
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

	view, err := s.ReportAt(ctx, 7, now)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if view.StockDays != 30 {
		t.Errorf("a 7 day report reads stock over %d days, want 30", view.StockDays)
	}
	var soldOut int
	afterSoldOut := true
	for i, r := range view.AtRisk {
		if r.Estimate().State != admin.CoverSoldOut {
			continue
		}
		soldOut++
		if i > 0 && view.AtRisk[i-1].Estimate().State != admin.CoverSoldOut {
			afterSoldOut = false
		}
	}
	if soldOut != 10 || view.MoreSoldOut < 1 {
		t.Errorf("%d sold out rows listed with %d left off, want 10 and the rest counted", soldOut, view.MoreSoldOut)
	}
	if !afterSoldOut {
		t.Error("a sold out row follows a row that is not sold out")
	}
	row, ok := stockRow(&view, estimated)
	switch {
	case !ok:
		t.Errorf("report lacks %s: sold out rows must not push the estimates off the list", estimated)
	case row.Estimate().State != admin.CoverEstimated:
		t.Errorf("ten orders read as state %d, want CoverEstimated %d", row.Estimate().State, admin.CoverEstimated)
	case row.Sellable != 5 || row.Sold != 10 || row.Orders != 10:
		t.Errorf("stock 10, safety 5, ten orders of one: sellable %d sold %d orders %d, want 5, 10 and 10",
			row.Sellable, row.Sold, row.Orders)
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
	row, ok := stockRow(&view, sku)
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
	row, ok := stockRow(&view, inStock)
	if !ok {
		t.Fatalf("report lacks %s", inStock)
	}
	if want := 14*24*time.Hour + 15*time.Hour + 20*time.Minute; row.InStock != want {
		t.Errorf("received on 02-25 into a window from 02-10 to 03-10 15:20: in stock %v, want %v", row.InStock, want)
	}
	laterAt, laterOK := indexOf(&view, later)
	earlierAt, earlierOK := indexOf(&view, earlier)
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

func indexOf(view *admin.ReportView, sku string) (int, bool) {
	for i := range view.AtRisk {
		if view.AtRisk[i].SKU == sku {
			return i, true
		}
	}
	return 0, false
}

func stockRow(view *admin.ReportView, sku string) (admin.StockRisk, bool) {
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

// newVariant makes an active variant holding stock with the given safety level.
func newVariant(t *testing.T, stock, safety int) (id uuid.UUID, sku string) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer pgtx.Rollback(ctx, tx)

	var productID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, 'runway-' || gen_random_uuid(), '庫存天數商品', 'draft', now()
		FROM brands b CROSS JOIN categories c
		WHERE c.parent_id IS NULL ORDER BY b.id, c.id LIMIT 1
		RETURNING id`).Scan(&productID); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, position, stock_quantity, safety_stock)
		VALUES ($1, 'RUNWAY-' || upper(replace(gen_random_uuid()::text, '-', '')), 1, 0, $2, $3)
		RETURNING id, sku`, productID, stock, safety).Scan(&id, &sku); err != nil {
		t.Fatalf("create variant: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE products SET status = 'active' WHERE id = $1`, productID); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return id, sku
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

// Seven shop days up to Sunday 2023-11-12 15:20 start on 11-06, the seven
// before them on 10-30 and stop at 11-05 15:20. The orders that are left out
// are each of a different kind, so that a day count that took them in would add
// to more than the tile.
func TestPaidByShopDayAddsUpToRevenue(t *testing.T) {
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
	now := at("2023-11-12 15:20:00")

	for _, order := range []struct {
		placed string
		cents  int64
		paid   bool
	}{
		{"2023-11-12 15:19:00", 1_00, true},
		{"2023-11-12 15:21:00", 2_00, true},    // after now
		{"2023-11-10 23:59:00", 4_00, true},    // the last minute of its shop day
		{"2023-11-11 00:01:00", 8_00, true},    // the first minute of the next
		{"2023-11-08 12:00:00", 16_00, false},  // never paid
		{"2023-11-06 00:01:00", 32_00, true},   // this period's first day
		{"2023-11-05 15:19:00", 64_00, true},   // previous period, before its cut
		{"2023-11-05 15:21:00", 128_00, true},  // after the cut
		{"2023-10-30 00:01:00", 256_00, true},  // previous period's first day
		{"2023-10-29 23:59:00", 512_00, true},  // before it
		{"2023-09-15 10:00:00", 1024_00, true}, // only the 90 days reach it
	} {
		moment := at(order.placed)
		reportOrderAt(t, order.cents, order.paid, &moment)
	}
	// Refunded in full before it shipped: no revenue, so on no day.
	moment := at("2023-11-09 10:00:00")
	refunded := reportOrderAt(t, 2048_00, true, &moment)
	if _, err := pool.Exec(ctx,
		`INSERT INTO return_requests (order_id, reason, before_shipment) VALUES ($1, '', true)`, refunded); err != nil {
		t.Fatalf("refund before shipment: %v", err)
	}

	for _, days := range []int32{7, 30, 90} {
		view, err := s.ReportAt(ctx, days, now)
		if err != nil {
			t.Fatalf("report of %d days: %v", days, err)
		}
		daily := view.Daily
		for _, c := range []struct {
			name   string
			series chart.Series
			want   int64
		}{
			{"this period", daily.Current, view.RevenueCents},
			{"previous period", daily.Previous, view.Previous.RevenueCents},
		} {
			var sum int64
			zeroDays := 0
			for _, b := range c.series.Buckets {
				sum += b.Value
				if b.Value == 0 {
					zeroDays++
				}
			}
			if sum != c.want {
				t.Errorf("%d days, %s: the days add up to %d cents, want the tile's %d", days, c.name, sum, c.want)
			}
			if len(c.series.Buckets) != int(days) {
				t.Errorf("%d days, %s: %d days drawn, want %d", days, c.name, len(c.series.Buckets), days)
			}
			if zeroDays == 0 {
				t.Errorf("%d days, %s: no day without orders, want the empty days present as zeros", days, c.name)
			}
			if !c.series.Partial {
				t.Errorf("%d days, %s: not marked as ending mid-day", days, c.name)
			}
		}
		if daily.Cut != "15:20" {
			t.Errorf("%d days: cut at %q, want 15:20", days, daily.Cut)
		}
	}

	week, err := s.ReportAt(ctx, 7, now)
	if err != nil {
		t.Fatalf("report of 7 days: %v", err)
	}
	daily := week.Daily
	if got, want := daily.Current.Buckets[0].Value, int64(32_00); got != want {
		t.Errorf("the first shop day (11-06) holds %d cents, want %d", got, want)
	}
	if got, want := daily.Current.Buckets[4].Value, int64(4_00); got != want {
		t.Errorf("11-10 holds %d cents, want %d: 23:59 is still that day", got, want)
	}
	if got, want := daily.Current.Buckets[5].Value, int64(8_00); got != want {
		t.Errorf("11-11 holds %d cents, want %d", got, want)
	}
}

func TestReturnedProductsCountOnlyDecidedUnitsOfOrdersPlacedInThePeriod(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool)

	countedID, counted := newVariant(t, 20, 0)
	orderOnVariant(t, countedID, counted, 12, nil)
	returnUnits(t, orderOf(t, counted, false), 3, "approved")
	// The same product, sold before the window and returned: it is neither in
	// the units sold nor in the units returned.
	before := time.Now().AddDate(0, 0, -60)
	orderOnVariant(t, countedID, counted, 8, &before)
	returnUnits(t, orderOf(t, counted, true), 4, "approved")
	undecidedID, undecided := newVariant(t, 20, 0)
	orderOnVariant(t, undecidedID, undecided, 5, nil)
	returnUnits(t, orderOf(t, undecided, false), 2, "requested")
	declinedID, declined := newVariant(t, 20, 0)
	orderOnVariant(t, declinedID, declined, 6, nil)
	returnUnits(t, orderOf(t, declined, false), 2, "rejected")

	view, err := s.ReportAt(ctx, 30, time.Now())
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if view.ReturnedErr != nil {
		t.Fatalf("returned products: %v", view.ReturnedErr)
	}
	var got *admin.ReturnedProduct
	for i, p := range view.Returned {
		switch p.Slug {
		case productSlug(t, undecided):
			t.Errorf("an open return listed %q with %d returned", p.Slug, p.Returned)
		case productSlug(t, declined):
			t.Errorf("a declined return listed %q with %d returned", p.Slug, p.Returned)
		case productSlug(t, counted):
			got = &view.Returned[i]
		}
	}
	if got == nil {
		t.Fatal("an approved return is missing from the products returned most")
	}
	if got.Returned != 3 || got.Sold != 12 {
		t.Errorf("returned/sold = %d/%d, want 3/12", got.Returned, got.Sold)
	}
}

// orderOf is the newest order of sku, or the oldest.
func orderOf(t *testing.T, sku string, oldest bool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		SELECT ol.order_id FROM order_lines ol JOIN orders o ON o.id = ol.order_id
		WHERE ol.sku = $1
		ORDER BY CASE WHEN $2 THEN o.placed_at END, o.placed_at DESC LIMIT 1`, sku, oldest).Scan(&id); err != nil {
		t.Fatalf("read the order of %s: %v", sku, err)
	}
	return id
}

func productSlug(t *testing.T, sku string) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(), `
		SELECT p.slug FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.sku = $1`, sku).Scan(&slug); err != nil {
		t.Fatalf("read slug of %s: %v", sku, err)
	}
	return slug
}

// returnUnits ships the paid order and asks for qty of its one line's units
// back, then moves the request to status.
func returnUnits(t *testing.T, orderID uuid.UUID, qty int, status string) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer pgtx.Rollback(ctx, tx)

	var lineID uuid.UUID
	var sold int
	if err := tx.QueryRow(ctx, `
		SELECT id, quantity FROM order_lines WHERE order_id = $1`, orderID).
		Scan(&lineID, &sold); err != nil {
		t.Fatalf("read the sale on order %s: %v", orderID, err)
	}
	admintest.MoveOrderToShipped(t, tx, orderID)
	var shipmentID, requestID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1::uuid, 'black_cat', 'T-' || $1::uuid::text) RETURNING id`, orderID).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, $4)`, orderID, shipmentID, lineID, sold); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason) VALUES ($1, '不合用') RETURNING id`,
		orderID).Scan(&requestID); err != nil {
		t.Fatalf("create return request: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, $4)`, orderID, requestID, lineID, qty); err != nil {
		t.Fatalf("create return line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if status == "requested" {
		return
	}
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests SET status = $2, decided_at = now(), resolution = '核准'
		WHERE id = $1`, requestID, status); err != nil {
		t.Fatalf("move the return to %s: %v", status, err)
	}
}

// The same orders as the tile counts, a day at a time: the unpaid order, the
// one refunded before shipment and the ones outside the period are on no day.
func TestPaidOrdersPerDayAddUpToThePaidOrderTile(t *testing.T) {
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
	now := at("2023-03-12 15:20:00")

	for _, order := range []struct {
		placed string
		paid   bool
	}{
		{"2023-03-12 15:19:00", true},
		{"2023-03-12 10:00:00", true},
		{"2023-03-12 15:21:00", true}, // after now
		{"2023-03-10 23:59:00", true},
		{"2023-03-11 00:01:00", true},
		{"2023-03-08 12:00:00", false}, // never paid
		{"2023-03-06 00:01:00", true},
		{"2023-03-05 23:59:00", true}, // the day before the period
	} {
		moment := at(order.placed)
		reportOrderAt(t, 100_00, order.paid, &moment)
	}
	moment := at("2023-03-09 10:00:00")
	refunded := reportOrderAt(t, 100_00, true, &moment)
	if _, err := pool.Exec(ctx,
		`INSERT INTO return_requests (order_id, reason, before_shipment) VALUES ($1, '', true)`, refunded); err != nil {
		t.Fatalf("refund before shipment: %v", err)
	}

	view, err := s.ReportAt(ctx, 7, now)
	if err != nil {
		t.Fatalf("ReportAt: %v", err)
	}
	var sum int64
	perDay := map[string]int64{}
	for _, b := range view.Paid.Days.Buckets {
		sum += b.Value
		perDay[b.Day.Format("01-02")] = b.Value
	}
	if sum != view.Orders {
		t.Errorf("the days hold %d paid orders, the tile %d", sum, view.Orders)
	}
	if len(view.Paid.Days.Buckets) != 7 {
		t.Errorf("%d days, want the 7 shop days with the empty ones present", len(view.Paid.Days.Buckets))
	}
	for day, want := range map[string]int64{"03-06": 1, "03-07": 0, "03-08": 0, "03-09": 0, "03-10": 1, "03-11": 1, "03-12": 2} {
		if got := perDay[day]; got != want {
			t.Errorf("%s holds %d paid orders, want %d", day, got, want)
		}
	}
	if !view.Paid.Days.Partial {
		t.Error("the days are not marked as ending mid-day")
	}
}

func TestCampaignsAreThoseOnDuringThePeriod(t *testing.T) {
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
	now := at("2023-11-12 15:20:00") // seven shop days from 11-06

	for _, c := range []struct {
		slug, title      string
		starts, ends     string
		active           bool
		titleEnglish     string
		wantFrom, wantTo string
	}{
		{"rep-ended-before", "早已結束", "2023-10-01 00:00:00", "2023-11-05 23:59:00", true, "", "", ""},
		{"rep-ends-at-the-start", "剛好結束", "2023-10-01 00:00:00", "2023-11-06 00:00:00", true, "", "", ""},
		{"rep-ends-after-midnight", "隔日零點結束", "2023-11-01 00:00:00", "2023-11-07 00:00:00", true, "", "2023-11-01", "2023-11-06"},
		{"rep-overlaps-the-start", "跨過期初", "2023-11-04 10:00:00", "2023-11-07 23:59:00", true, "Crosses the start", "2023-11-04", "2023-11-07"},
		{"rep-running", "進行中", "2023-11-10 00:00:00", "2023-11-20 23:59:00", true, "", "2023-11-10", "2023-11-20"},
		{"rep-switched-off", "已停用", "2023-11-08 00:00:00", "2023-11-09 23:59:00", false, "", "", ""},
		{"rep-not-yet", "尚未開始", "2023-11-12 16:00:00", "2023-11-14 23:59:00", true, "", "", ""},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO sale_campaigns (slug, title, title_en, starts_at, ends_at, is_active)
			VALUES ($1, $2, nullif($3, ''), $4, $5, $6)`,
			c.slug, c.title, c.titleEnglish, at(c.starts), at(c.ends), c.active); err != nil {
			t.Fatalf("campaign %s: %v", c.slug, err)
		}
	}

	view, err := s.ReportAt(ctx, 7, now)
	if err != nil {
		t.Fatalf("ReportAt: %v", err)
	}
	var got []string
	for _, c := range view.Paid.Campaigns {
		got = append(got, c.Label+" "+c.From.Format("2006-01-02")+" "+c.To.Format("2006-01-02"))
	}
	want := []string{
		"隔日零點結束 2023-11-01 2023-11-06",
		"跨過期初 2023-11-04 2023-11-07",
		"進行中 2023-11-10 2023-11-20",
	}
	if !slices.Equal(got, want) {
		t.Errorf("campaigns = %q, want %q", got, want)
	}

	english, err := s.ReportAt(i18n.WithLocale(ctx, i18n.En), 7, now)
	if err != nil {
		t.Fatalf("ReportAt in English: %v", err)
	}
	if got := english.Paid.Campaigns[1].Label; got != "Crosses the start" {
		t.Errorf("an English reader gets the title %q, want the English one", got)
	}
}

func TestAnEmptyPeriodNamesTheLatestPaidOrdersDay(t *testing.T) {
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
	now := at("2022-05-20 15:20:00") // seven shop days from 05-14

	for _, order := range []struct {
		placed string
		paid   bool
	}{
		{"2022-05-01 23:59:00", true},
		{"2022-05-03 09:00:00", true},
		{"2022-05-09 09:00:00", false}, // unpaid, so not the latest
	} {
		moment := at(order.placed)
		reportOrderAt(t, 100_00, order.paid, &moment)
	}
	// Paid, then refunded in full before it shipped: it is no revenue, so it is
	// not the latest paid order either.
	latest := at("2022-05-12 09:00:00")
	refunded := reportOrderAt(t, 100_00, true, &latest)
	if _, err := pool.Exec(ctx,
		`INSERT INTO return_requests (order_id, reason, before_shipment) VALUES ($1, '', true)`, refunded); err != nil {
		t.Fatalf("refund before shipment: %v", err)
	}

	view, err := s.ReportAt(ctx, 7, now)
	if err != nil {
		t.Fatalf("ReportAt: %v", err)
	}
	if got := view.Paid.Days.Density(); got != chart.DensityNone {
		t.Fatalf("the period has density %d, want none", got)
	}
	if view.Paid.Latest == nil {
		t.Fatal("no latest paid order's day, want 2022-05-03")
	}
	if got := *view.Paid.Latest; got.Month != time.May || got.Day != 3 {
		t.Errorf("the latest paid order was on %d-%d, want 5-3", got.Month, got.Day)
	}
}

// A deeper category counts toward its root. An unpaid order and one refunded
// before shipment count toward none. Together the departments are the revenue
// figure, since the orders carry no discount, shipping or tax.
func TestDepartmentsAddUpToTheRevenueFigure(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool)

	suffix := uuid.NewString()
	rootA := departmentName("A", suffix)
	rootB := departmentName("B", suffix)
	idA := newCategory(t, rootA, nil)
	idB := newCategory(t, rootB, nil)
	idChild := newCategory(t, departmentName("child", suffix), &idA)

	before, err := s.ReportAt(ctx, 30, time.Now())
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	categoryOrder(t, idA, 30000, true)
	categoryOrder(t, idChild, 20000, true)
	categoryOrder(t, idB, 5000, true)
	categoryOrder(t, idB, 99999, false)
	refunded := categoryOrder(t, idB, 77777, true)
	var staff uuid.UUID
	if err = pool.QueryRow(ctx, `
		INSERT INTO users (email, role) VALUES ('dept-staff-' || gen_random_uuid() || '@goen.invalid', 'staff')
		RETURNING id`).Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	if _, err = pool.Exec(ctx, `SELECT open_refund_before_shipment($1, 'department', $2, $3)`,
		refunded, staff, "dept-"+refunded); err != nil {
		t.Fatalf("refund before shipment: %v", err)
	}
	after, err := s.ReportAt(ctx, 30, time.Now())
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	byName := func(v admin.ReportView) (map[string]int64, int64) {
		got := map[string]int64{}
		var sum int64
		for _, d := range v.Departments {
			got[d.Name] = d.SalesCents
			sum += d.SalesCents
		}
		return got, sum
	}
	was, wasSum := byName(before)
	now, nowSum := byName(after)
	if got := now[rootA] - was[rootA]; got != 50000 {
		t.Errorf("department %q moved by %d, want 50000 (its own line and its child's)", rootA, got)
	}
	if got := now[rootB] - was[rootB]; got != 5000 {
		t.Errorf("department %q moved by %d, want 5000 (neither the unpaid order nor the one refunded before shipment is revenue)", rootB, got)
	}
	if _, child := now[departmentName("child", suffix)]; child {
		t.Error("a child category is listed as a department")
	}
	if got, want := nowSum-wasSum, after.RevenueCents-before.RevenueCents; got != want {
		t.Errorf("departments moved by %d, the revenue figure by %d", got, want)
	}
}

func departmentName(part, suffix string) string { return "部門" + part + " " + suffix }

func newCategory(t *testing.T, name string, parent *uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO categories (parent_id, slug, name, position)
		SELECT $1::uuid, 'dept-' || gen_random_uuid(), $2,
		       coalesce(max(position), 0) + 1 FROM categories WHERE parent_id IS NOT DISTINCT FROM $1::uuid
		RETURNING id`, parent, name).Scan(&id); err != nil {
		t.Fatalf("create category: %v", err)
	}
	return id
}

// categoryOrder places an order of one line of a new product in the category.
func categoryOrder(t *testing.T, categoryID uuid.UUID, cents int64, paid bool) (number string) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer pgtx.Rollback(ctx, tx)

	var productID, orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, $1, 'dept-' || gen_random_uuid(), '館別商品', 'draft', now()
		FROM brands b ORDER BY b.id LIMIT 1
		RETURNING id`, categoryID).Scan(&productID); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, product_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, $2, 'DEPT-SKU', '館別商品', $3, 1)`, orderID, productID, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'r@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("delivery details: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if paid {
		ref := "dept_" + orderID.String()
		if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, $3::bigint)`, orderID, ref, cents); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		if _, err := pool.Exec(ctx, `SELECT capture_payment($1, $2::bigint, NULL, NULL)`, ref, cents); err != nil {
			t.Fatalf("capture: %v", err)
		}
	}
	return number
}
