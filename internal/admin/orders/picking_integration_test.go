//go:build integration

package orders_test

import (
	"context"
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

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

func TestPickingTotalsSpanEveryPageAndSubtractRecordedShipments(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, owner)
	firstOrder, firstLine := uuid.Nil, uuid.Nil
	for i := range 53 {
		sku, quantity := "Z-PICK", int32(3)
		if i == 52 {
			sku, quantity = "A-PICK", 4
		}
		id, line := pickingFixtureOrder(t, owner, sku, quantity, i, true)
		if i == 0 {
			firstOrder, firstLine = id, line
		}
	}
	pickingFixtureOrder(t, owner, "Z-PICK", 99, 54, false)
	var shipment uuid.UUID
	if err := owner.QueryRow(ctx, `INSERT INTO order_shipments (order_id, carrier, tracking_number) VALUES ($1, $2, $3) RETURNING id`, firstOrder, string(carrier.BlackCat), "PICK-"+uuid.NewString()).Scan(&shipment); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity) VALUES ($1, $2, $3, 2)`, firstOrder, shipment, firstLine); err != nil {
		t.Fatal(err)
	}
	var dispatchedLine uuid.UUID
	if err := owner.QueryRow(ctx, `SELECT id FROM order_lines WHERE order_id = $1 AND sku = 'DONE-PICK'`, firstOrder).Scan(&dispatchedLine); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
 VALUES ($1, $2, $3, 2)`, firstOrder, shipment, dispatchedLine); err != nil {
		t.Fatal(err)
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
	if len(view.Totals) != 2 || view.Totals[0].SKU != "A-PICK" || view.Totals[0].Remaining != 4 || view.Totals[1].SKU != "Z-PICK" || view.Totals[1].Remaining != 154 {
		t.Errorf("all-queue totals = %+v, want sorted A=4 and Z=154", view.Totals)
	}
	u, err := url.Parse(view.Next)
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.Picking(ctx, u.Query().Get(web.KeysetParam))
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Slips) != 3 || next.Next != "" || next.First == "" || !slices.Equal(next.Totals, view.Totals) {
		t.Fatalf("next page slips=%d first=%q next=%q totals=%+v", len(next.Slips), next.First, next.Next, next.Totals)
	}
	seen := make(map[string]bool)
	for _, slip := range append(view.Slips, next.Slips...) {
		if seen[slip.Number] {
			t.Fatalf("slip %q appeared more than once, want one occurrence", slip.Number)
		}
		if len(slip.Lines) != 1 {
			t.Fatalf("slip %q lines = %d, want 1 outstanding line", slip.Number, len(slip.Lines))
		}
		seen[slip.Number] = true
	}
	one, err := db.New(owner).ShippableLines(ctx, firstOrder)
	if err != nil || len(one) != 1 || one[0].Remaining != 1 {
		t.Fatalf("authoritative shippable lines = %+v, %v", one, err)
	}
	if view.Slips[0].Lines[0].Quantity != one[0].Remaining {
		t.Fatalf("partially dispatched slip quantity=%d, want %d", view.Slips[0].Lines[0].Quantity, one[0].Remaining)
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
	for _, user := range []account.User{{}, {ID: uuid.NewString(), Role: account.RoleCustomer}} {
		req := httptest.NewRequestWithContext(account.WithUser(t.Context(), user), http.MethodGet, "/admin/orders/picking/slips", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("role=%q GET=%d, want 404", user.Role, w.Code)
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

func pickingFixtureOrder(t *testing.T, owner *pgxpool.Pool, sku string, quantity int32, position int, picking bool) (orderID, lineID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := tx.QueryRow(ctx, `INSERT INTO orders (order_number, placed_at, shipping_version_id, shipping_method_code, shipping_method_name, shipping_cents)
SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id=v.method_id ORDER BY v.effective_at LIMIT 1 RETURNING id`, time.Now().Add(-time.Duration(position)*time.Minute)).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO order_lines (order_id, sku, product_name, variant_label, unit_price_cents, quantity)
VALUES ($1, $2, '揀貨測試', '256 GB', 0, $3) RETURNING id`, orderID, sku, quantity).Scan(&lineID); err != nil {
		t.Fatal(err)
	}
	if position == 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
   VALUES ($1, 'DONE-PICK', 'Fully dispatched fixture', 0, 2, 1)`, orderID); err != nil {
			t.Fatal(err)
		}
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
	return orderID, lineID
}
