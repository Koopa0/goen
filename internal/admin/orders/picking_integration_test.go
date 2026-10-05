//go:build integration

package orders_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

func TestPickingTotalsSpanEveryPageAndSubtractRecordedShipments(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, staff := admintest.StaffContext(t, owner)
	wantNumbers := make([]string, 0, 55)
	wantTotals := map[string]int64{"A-PICK": 4, "Z-PICK": 156}
	partial := make(map[string]string)
	for i := range 53 {
		sku, quantity := "Z-PICK", int32(3)
		if i == 52 {
			sku, quantity = "A-PICK", 4
		}
		id := pickingFixtureOrder(t, owner, sku, quantity, i, true)
		var number string
		if err := owner.QueryRow(ctx, `SELECT order_number FROM orders WHERE id=$1`, id).Scan(&number); err != nil {
			t.Fatal(err)
		}
		wantNumbers = append(wantNumbers, number)
	}
	pickingFixtureOrder(t, owner, "Z-PICK", 99, 56, false)
	writer := admintest.OrderStore(owner, admintest.Refunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	for i, status := range []string{"shipped", "delivered", "fully-shipped"} {
		number, id, lines, _ := admintest.TwoLineOrderWithStock(t, owner, "batch-"+status)
		if _, err := owner.Exec(ctx, `UPDATE orders SET placed_at=$2, customer_note='Gift <note>: no price inside' WHERE id=$1`, id,
			time.Date(2020, 1, 1, 0, 53+i, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
		quantities := map[uuid.UUID]int32{lines[0]: 1, lines[1]: 3}
		if status == "fully-shipped" {
			quantities[lines[0]] = 3
		}
		if err := writer.Ship(ctx, number, orders.Dispatch{
			Carrier: "black_cat", Tracking: "BATCH-" + number, Lines: quantities,
		}, actor); err != nil {
			t.Fatal(err)
		}
		if status == "delivered" {
			if _, err := writer.Advance(ctx, number, "delivered", actor); err != nil {
				t.Fatal(err)
			}
		}
		if status == "fully-shipped" {
			continue
		}
		var actual, sku string
		if err := owner.QueryRow(ctx, `SELECT o.fulfillment_status, ol.sku FROM orders o JOIN order_lines ol ON ol.order_id=o.id WHERE o.id=$1 AND ol.id=$2`, id, lines[0]).Scan(&actual, &sku); err != nil {
			t.Fatal(err)
		}
		if actual != status {
			t.Fatalf("partially dispatched order status = %q, want %q", actual, status)
		}
		partial[number] = sku
		wantTotals[sku] = 2
		wantNumbers = append(wantNumbers, number)
	}

	cfg, err := pgxpool.ParseConfig(owner.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["role"] = "admin"
	reader, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reader.Close)
	var auditsBefore, movesBefore int
	if err = owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events), (SELECT count(*) FROM inventory_movements)`).Scan(&auditsBefore, &movesBefore); err != nil {
		t.Fatal(err)
	}
	s := admintest.OrderStore(reader, admintest.Refunder{}, nil, nil)
	view, err := s.Picking(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Slips) != 50 || view.Next == "" {
		t.Fatalf("first batch slips=%d next=%q", len(view.Slips), view.Next)
	}
	gotTotals := make(map[string]int64)
	var previousSKU string
	for _, total := range view.Totals {
		gotTotals[total.SKU] = total.Remaining
		if previousSKU != "" && previousSKU >= total.SKU {
			t.Errorf("totals are not in SKU order: %q before %q", previousSKU, total.SKU)
		}
		previousSKU = total.SKU
	}
	if len(gotTotals) != len(wantTotals) {
		t.Errorf("all-queue totals = %+v, want %+v", gotTotals, wantTotals)
	}
	for sku, quantity := range wantTotals {
		if gotTotals[sku] != quantity {
			t.Errorf("all-queue total for %q = %d, want %d", sku, gotTotals[sku], quantity)
		}
	}
	u, err := url.Parse(view.Next)
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.Picking(ctx, u.Query().Get(web.KeysetParam))
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Slips) != 5 || next.Next != "" || next.First == "" || !slices.Equal(next.Totals, view.Totals) {
		t.Fatalf("next page slips=%d first=%q next=%q totals=%+v", len(next.Slips), next.First, next.Next, next.Totals)
	}
	seen := make(map[string]bool)
	gotNumbers := make([]string, 0, 55)
	for _, slip := range append(view.Slips, next.Slips...) {
		if seen[slip.Number] {
			t.Fatalf("slip %q appeared more than once, want one occurrence", slip.Number)
		}
		if slip.CustomerNote != "Gift <note>: no price inside" {
			t.Errorf("slip %q lost its customer note: %q", slip.Number, slip.CustomerNote)
		}
		if len(slip.Lines) != 1 {
			t.Fatalf("slip %q lines = %d, want 1 outstanding line", slip.Number, len(slip.Lines))
		}
		if sku, ok := partial[slip.Number]; ok && (slip.Lines[0].SKU != sku || slip.Lines[0].Quantity != 2) {
			t.Errorf("partially dispatched slip %q = %+v, want %q quantity 2", slip.Number, slip.Lines, sku)
		}
		gotNumbers = append(gotNumbers, slip.Number)
		seen[slip.Number] = true
	}
	if !slices.Equal(gotNumbers, wantNumbers) {
		t.Errorf("slips in oldest-first order = %v, want %v", gotNumbers, wantNumbers)
	}
	for number := range partial {
		if !seen[number] {
			t.Errorf("partially dispatched order %q is missing", number)
		}
	}
	h := admintest.OrderDesk(s)
	mux := http.NewServeMux()
	h.Routes(mux, access.New(slog.New(slog.DiscardHandler), nil))
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		req := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodGet, "/admin/orders/picking/slips", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s staff GET=%d", locale, w.Code)
		}
	}
	for _, current := range []user.User{{}, {ID: uuid.NewString(), Role: user.RoleCustomer}} {
		req := httptest.NewRequestWithContext(user.NewContext(t.Context(), current), http.MethodGet, "/admin/orders/picking/slips", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("role=%q GET=%d, want 404", current.Role, w.Code)
		}
	}
	var picking, audited, moved int
	if err := owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM orders WHERE fulfillment_status='picking'), (SELECT count(*) FROM audit_events), (SELECT count(*) FROM inventory_movements)`).Scan(&picking, &audited, &moved); err != nil {
		t.Fatal(err)
	}
	if picking != 53 || audited != auditsBefore || moved != movesBefore {
		t.Fatalf("read-only GET changed orders/audits/inventory: %d/%d/%d", picking, audited, moved)
	}
}

func TestPickingReadsTheCustomerNote(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, owner)
	pickingFixtureOrder(t, owner, "NOTE-PICK", 1, 0, true)
	view, err := admintest.OrderStore(admintest.AdminRolePool(t, owner), admintest.Refunder{}, nil, nil).Picking(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Slips) != 1 {
		t.Fatalf("Picking() slips = %d, want 1", len(view.Slips))
	}
	if got := view.Slips[0].CustomerNote; got != "Gift <note>: no price inside" {
		t.Errorf("Picking() customer note = %q, want %q", got, "Gift <note>: no price inside")
	}
}

func TestPickingSlipsStartWithTheOldestOrder(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, owner)
	want := make([]string, 0, 3)
	for position := range 3 {
		id := pickingFixtureOrder(t, owner, "AGE-PICK", 1, position, true)
		var number string
		if err := owner.QueryRow(ctx, `SELECT order_number FROM orders WHERE id=$1`, id).Scan(&number); err != nil {
			t.Fatal(err)
		}
		want = append(want, number)
	}
	view, err := admintest.OrderStore(admintest.AdminRolePool(t, owner), admintest.Refunder{}, nil, nil).Picking(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(view.Slips))
	for _, slip := range view.Slips {
		got = append(got, slip.Number)
	}
	if !slices.Equal(got, want) {
		t.Errorf("Picking() order = %v, want oldest first %v", got, want)
	}
}

func pickingFixtureOrder(t *testing.T, owner *pgxpool.Pool, sku string, quantity int32, position int, picking bool) (orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pgtx.Rollback(ctx, tx)
	if err := tx.QueryRow(ctx, `INSERT INTO orders (order_number, placed_at, shipping_version_id, shipping_method_code, shipping_method_name, shipping_cents, customer_note)
SELECT next_order_number(), $1, v.id, sm.code, v.name, 0, 'Gift <note>: no price inside'
FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id=v.method_id ORDER BY v.effective_at LIMIT 1 RETURNING id`, time.Date(2020, 1, 1, 0, position, 0, 0, time.UTC)).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO order_lines (order_id, sku, product_name, variant_label, unit_price_cents, quantity)
VALUES ($1, $2, '揀貨測試', '256 GB', 0, $3)`, orderID, sku, quantity); err != nil {
		t.Fatal(err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
VALUES ($1, 'picking@example.com', '揀貨收件人', '0912345678', '110', '臺北市', '信義區', $2)`, orderID, fmt.Sprintf("測試路 %d 號", position)); err != nil {
		t.Fatal(err)
	}
	if picking {
		if _, err := tx.Exec(ctx, `UPDATE orders SET fulfillment_status='picking' WHERE id=$1`, orderID); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return orderID
}
