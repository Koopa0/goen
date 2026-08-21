//go:build integration

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/loyalty"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/site"
	"github.com/koopa0/goen/internal/twofactor"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/warranty"
	"github.com/koopa0/goen/internal/web"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p

	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		slog.Error("read seed", "error", err)
		os.Exit(1)
	}
	if _, err := pool.Exec(context.Background(), string(seed)); err != nil {
		slog.Error("load seed", "error", err)
		os.Exit(1)
	}

	code := m.Run()
	stop()
	os.Exit(code)
}

func staffID(t *testing.T) string {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role) VALUES ($1, 'admin') RETURNING id`,
		"staff-"+uuid.NewString()+"@example.com").Scan(&id); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	return id.String()
}

func TestStockMovesOnlyThroughTheLedger(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := staffID(t)

	var sku string
	var before int32
	if err := pool.QueryRow(ctx, `
		SELECT sku, stock_quantity FROM product_variants
		WHERE is_active ORDER BY position LIMIT 1`).Scan(&sku, &before); err != nil {
		t.Fatalf("read variant: %v", err)
	}

	key := "test-" + uuid.NewString()
	if err := s.AdjustStock(ctx, sku, 7, actor, key); err != nil {
		t.Fatalf("adjust: %v", err)
	}

	var after int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE sku = $1`, sku).Scan(&after); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if after != before+7 {
		t.Errorf("stock went %d -> %d, want %d", before, after, before+7)
	}

	var delta int32
	var reason string
	var hasActor bool
	if err := pool.QueryRow(ctx, `
		SELECT m.delta, m.reason, m.actor_user_id IS NOT NULL
		FROM inventory_movements m WHERE m.idempotency_key = $1`, key).
		Scan(&delta, &reason, &hasActor); err != nil {
		t.Fatalf("the adjustment left no movement row: %v", err)
	}
	if delta != 7 || reason != "adjustment" || !hasActor {
		t.Errorf("movement = %d/%q/actor:%v, want 7/adjustment/actor:true", delta, reason, hasActor)
	}
}

func TestAdjustmentIsIdempotent(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := staffID(t)

	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT sku FROM product_variants WHERE is_active ORDER BY position DESC LIMIT 1`).
		Scan(&sku); err != nil {
		t.Fatalf("read variant: %v", err)
	}

	key := "dup-" + uuid.NewString()
	if err := s.AdjustStock(ctx, sku, 5, actor, key); err != nil {
		t.Fatalf("first adjust: %v", err)
	}
	var afterFirst int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE sku = $1`, sku).Scan(&afterFirst); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	if err := s.AdjustStock(ctx, sku, 5, actor, key); err == nil {
		t.Error("the same idempotency key adjusted stock twice")
	}
	var afterSecond int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE sku = $1`, sku).Scan(&afterSecond); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if afterSecond != afterFirst {
		t.Errorf("stock moved again on the repeat: %d -> %d", afterFirst, afterSecond)
	}
}

func TestAdvanceRefusesAnUnfundedOrder(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	number := placeUnpaidOrder(t)
	_, err := s.Advance(ctx, number, "picking", uuid.NullUUID{})
	if err == nil {
		t.Fatal("an unpaid order was moved into fulfilment")
	}
	if !errors.Is(err, admin.ErrRefused) {
		t.Errorf("refused with %v, want ErrRefused", err)
	}
	if _, err := s.Advance(ctx, number, "cancelled", uuid.NullUUID{}); err != nil {
		t.Errorf("cancelling a pending order was refused: %v", err)
	}
}

func TestAdvanceRefusesAnIllegalTransition(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number := placeUnpaidOrder(t)

	if _, err := s.Advance(ctx, number, "shipped", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
		t.Errorf("pending -> shipped gave %v, want ErrRefused", err)
	}
	if _, err := s.Advance(ctx, number, "teleported", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
		t.Errorf("an unknown status gave %v, want ErrRefused", err)
	}
}

func placeUnpaidOrder(t *testing.T) string {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		SELECT next_order_number(), v.id, sm.code, v.name
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'ADMIN-TEST', '測試商品', 500000, 1)`, orderID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'x@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

func TestRetiringTheLastDiscountedVariantIsRefused(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	var sku string
	if err := pool.QueryRow(ctx, `
		WITH one AS (
			SELECT pv.id, pv.sku, pv.product_id FROM product_variants pv
			WHERE pv.is_active AND pv.compare_at_price_cents IS NOT NULL
			ORDER BY pv.position LIMIT 1
		)
		SELECT sku FROM one`).Scan(&sku); err != nil {
		t.Skipf("the seed has no discounted variant to test with: %v", err)
	}

	var productID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT product_id FROM product_variants WHERE sku = $1`, sku).Scan(&productID); err != nil {
		t.Fatalf("read product: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE product_variants SET compare_at_price_cents = NULL
		WHERE product_id = $1 AND sku <> $2`, productID, sku); err != nil {
		t.Fatalf("clear siblings: %v", err)
	}

	var campaignID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO sale_campaigns (slug, title, ends_at)
		VALUES ($1, '測試活動', now() + interval '7 days') RETURNING id`,
		"admin-test-"+uuid.NewString()[:8]).Scan(&campaignID); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO sale_campaign_products (campaign_id, product_id) VALUES ($1, $2)`,
		campaignID, productID); err != nil {
		t.Fatalf("feature product: %v", err)
	}
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM sale_campaigns WHERE id = $1`, campaignID)
	})

	if err := s.SetVariantActive(ctx, sku, false); !errors.Is(err, admin.ErrRefused) {
		t.Errorf("retiring the last discounted variant of a featured product gave %v, "+
			"want ErrRefused — the campaign would point at nothing marked down", err)
	}
}

func pickingOrderHoldingStock(t *testing.T) (number string, orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	var variantID uuid.UUID
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
	defer func() { _ = tx.Rollback(ctx) }()

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
		VALUES ($1, $2, 'SHIP-TEST', '測試商品', 100000, 1)`, orderID, variantID); err != nil {
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
		`SELECT hold_inventory($1, $2, 1, now() + interval '30 minutes', $3)`,
		orderID, variantID, "ship-test:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, "cs_ship_"+number); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`, "cs_ship_"+number); err != nil {
		t.Fatalf("capture: %v", err)
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

func TestShipDoesAllFourWritesOrNone(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number, orderID := pickingOrderHoldingStock(t)

	if err := s.Ship(ctx, number, admin.Dispatch{Carrier: "黑貓宅急便", Tracking: "TW1234567890"}, uuid.NullUUID{}); err != nil {
		t.Fatalf("ship: %v", err)
	}

	var status, carrier, tracking string
	if err := pool.QueryRow(ctx, `
		SELECT o.fulfillment_status, sh.carrier, sh.tracking_number
		FROM orders o JOIN order_shipments sh ON sh.order_id = o.id
		WHERE o.id = $1`, orderID).Scan(&status, &carrier, &tracking); err != nil {
		t.Fatalf("read shipment: %v", err)
	}
	if status != "shipped" {
		t.Errorf("order is %q after shipping, want shipped", status)
	}
	if carrier != "黑貓宅急便" || tracking != "TW1234567890" {
		t.Errorf("shipment is %q/%q, want the carrier and tracking that were submitted", carrier, tracking)
	}

	var held int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM inventory_reservations WHERE order_id = $1 AND state = 'held'`,
		orderID).Scan(&held); err != nil {
		t.Fatalf("read reservations: %v", err)
	}
	if held != 0 {
		t.Errorf("%d reservations are still held after dispatch; a sweeper would "+
			"return stock that has already gone out", held)
	}

	var consumed int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM inventory_reservations WHERE order_id = $1 AND state = 'consumed'`,
		orderID).Scan(&consumed); err != nil {
		t.Fatalf("read reservations: %v", err)
	}
	if consumed != 1 {
		t.Errorf("%d reservations consumed, want 1", consumed)
	}

	var shippedQty int
	if scanErr := pool.QueryRow(ctx, `
		SELECT coalesce(sum(sl.quantity), 0) FROM order_shipment_lines sl
		WHERE sl.order_id = $1`, orderID).Scan(&shippedQty); scanErr != nil {
		t.Fatalf("read shipment lines: %v", scanErr)
	}
	if shippedQty != 1 {
		t.Errorf("%d units recorded as shipped, want 1 — a shipment with no "+
			"lines cannot be reconciled against a return", shippedQty)
	}

	var kinds []string
	rows, err := pool.Query(ctx,
		`SELECT kind FROM order_events WHERE order_id = $1 ORDER BY occurred_at, id`, orderID)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if scanErr := rows.Scan(&k); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		kinds = append(kinds, k)
	}
	if len(kinds) == 0 || kinds[len(kinds)-1] != "shipped" {
		t.Errorf("history ends with %v, want a 'shipped' entry", kinds)
	}
}

func TestShipRollsEverythingBackWhenTheStatusMoveIsRefused(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number := placeUnpaidOrder(t)

	var orderID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatalf("find order: %v", err)
	}

	err := s.Ship(ctx, number, admin.Dispatch{Carrier: "黑貓宅急便", Tracking: "TW999"}, uuid.NullUUID{})
	if !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("shipping a pending order gave %v, want ErrRefused", err)
	}

	var shipments, events int
	if err := pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM order_shipments WHERE order_id = $1),
		        (SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'shipped')`,
		orderID).Scan(&shipments, &events); err != nil {
		t.Fatalf("count: %v", err)
	}
	if shipments != 0 {
		t.Errorf("%d shipment rows survived a refused dispatch", shipments)
	}
	if events != 0 {
		t.Errorf("%d shipped events survived a refused dispatch", events)
	}
}

func TestShipNeedsACarrierAndATracking(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number, _ := pickingOrderHoldingStock(t)

	tests := []struct{ name, carrier, tracking string }{
		{"no carrier", "", "TW1"},
		{"no tracking", "黑貓", ""},
		{"both blank", "", ""},
		{"whitespace only", "   ", "\t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.Ship(ctx, number, admin.Dispatch{Carrier: tt.carrier, Tracking: tt.tracking}, uuid.NullUUID{}); !errors.Is(err, admin.ErrInvalid) {
				t.Errorf("Ship(%q, %q) gave %v, want ErrInvalid", tt.carrier, tt.tracking, err)
			}
		})
	}
}

func TestAdvanceCannotShip(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number, orderID := pickingOrderHoldingStock(t)

	if _, err := s.Advance(ctx, number, "shipped", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("Advance to shipped gave %v, want ErrRefused", err)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "picking" {
		t.Errorf("order is %q, want picking — Advance moved it", status)
	}
}

func TestAdvanceRecordsWhoAndWhen(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number, orderID := pickingOrderHoldingStock(t)

	var staff uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '出貨人員')
		RETURNING id`, "ship-"+number+"@goen.invalid").Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}

	if _, err := s.Advance(ctx, number, "cancelled", uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("advance: %v", err)
	}

	var kind string
	var actor uuid.NullUUID
	if err := pool.QueryRow(ctx, `
		SELECT kind, actor_user_id FROM order_events
		WHERE order_id = $1 ORDER BY occurred_at DESC, id DESC LIMIT 1`,
		orderID).Scan(&kind, &actor); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if kind != "cancelled" {
		t.Errorf("latest event is %q, want cancelled", kind)
	}
	if !actor.Valid || actor.UUID != staff {
		t.Errorf("event actor is %v, want the staff member who acted", actor)
	}
}

type fakeRefunder struct {
	failIntent bool
	refundErr  error
	state      admin.RefundState
	// sent counts calls to Refund. The database cannot answer "was this sent
	// twice": the request key makes a repeat hit the same row and settle_refund
	// returns early on 'succeeded', so a sum over refunds reads the same either
	// way. Only the provider knows, which is what this stands in for.
	sent *atomic.Int64
}

func (f fakeRefunder) PaymentIntentFor(_ context.Context, sessionID string) (string, error) {
	if f.failIntent {
		return "", errors.New("stripe is unreachable")
	}
	return "pi_for_" + sessionID, nil
}

func (f fakeRefunder) Refund(_ context.Context, intentID, requestKey string, _ int64) (string, admin.RefundState, error) {
	if f.sent != nil {
		f.sent.Add(1)
	}
	if f.refundErr != nil {
		return "", "", f.refundErr
	}
	state := f.state
	if state == "" {
		state = admin.RefundSucceeded
	}
	return "re_" + requestKey + "_" + intentID[:6], state, nil
}

func returnedOrder(t *testing.T, qty int32) (requestID uuid.UUID, orderNumber string) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID, lineID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'REF-SKU', '測試商品', 100000, 2) RETURNING id`,
		orderID).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'f@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 200000)`,
		orderID, "cs_ret_"+orderNumber); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 200000, NULL, NULL)`,
		"cs_ret_"+orderNumber); err != nil {
		t.Fatalf("capture: %v", err)
	}
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, '黑貓', 'T-'||$2) RETURNING id`, orderID, orderNumber).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 2)`, orderID, shipmentID, lineID); err != nil {
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
	return requestID, orderNumber
}

func couponedShippedOrder(t *testing.T, lines int) (requestID uuid.UUID, orderNumber string) {
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
		                    shipping_method_name, shipping_cents, discount_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 8000, 50000
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	lineIDs := make([]uuid.UUID, 2)
	for i := range lineIDs {
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, $2, '測試商品', 50000, 1, $3) RETURNING id`,
			orderID, fmt.Sprintf("CPN-SKU-%d", i), i).Scan(&lineIDs[i]); err != nil {
			t.Fatalf("create line %d: %v", i, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'c@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	ref := "cs_cpn_" + orderNumber
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 58000)`, orderID, ref); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 58000, NULL, NULL)`, ref); err != nil {
		t.Fatalf("capture: %v", err)
	}
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, '黑貓', 'TC-'||$2) RETURNING id`, orderID, orderNumber).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason) VALUES ($1, '不合用') RETURNING id`,
		orderID).Scan(&requestID); err != nil {
		t.Fatalf("create return request: %v", err)
	}
	for i := range lineIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
			VALUES ($1, $2, $3, 1)`, orderID, shipmentID, lineIDs[i]); err != nil {
			t.Fatalf("create shipment line %d: %v", i, err)
		}
		if i < lines {
			if _, err := tx.Exec(ctx, `
				INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
				VALUES ($1, $2, $3, 1)`, orderID, requestID, lineIDs[i]); err != nil {
				t.Fatalf("create return line %d: %v", i, err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return requestID, orderNumber
}

func TestARefundIsWhatTheCustomerPaid(t *testing.T) {
	tests := []struct {
		name  string
		lines int
		want  int64
		why   string
	}{
		{
			name: "one of two lines, so a proportional share of the coupon", lines: 1,
			want: 25000,
			why:  "the customer paid NT$250 for this item after the coupon, not NT$500",
		},
		{
			name: "both lines, so the whole contract and the delivery fee with it", lines: 2,
			want: 58000,
			why:  "a rescission returns everything paid under the contract, delivery included",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := staffContext(t)
			s := admin.NewStore(pool, fakeRefunder{}, nil)
			requestID, _ := couponedShippedOrder(t, tt.lines)

			if err := s.Decide(ctx, requestID.String(), "approved", "已收到退貨", uuid.NullUUID{}); err != nil {
				t.Fatalf("approve: %v — a return the shop cannot pay for is the defect", err)
			}
			var amount int64
			if err := pool.QueryRow(ctx, `
				SELECT amount_cents FROM refunds WHERE return_request_id = $1`,
				requestID).Scan(&amount); err != nil {
				t.Fatalf("read refund: %v", err)
			}
			if amount != tt.want {
				t.Errorf("refunded %d, want %d — %s", amount, tt.want, tt.why)
			}
		})
	}
}

func TestApprovingAReturnRefundsWhatTheORDERSays(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, _ := returnedOrder(t, 1)

	if err := s.Decide(ctx, requestID.String(), "approved", "已收到退貨", uuid.NullUUID{}); err != nil {
		t.Fatalf("approve: %v", err)
	}

	var status string
	var amount int64
	var providerRef *string
	if err := pool.QueryRow(ctx, `
		SELECT status, amount_cents, provider_ref FROM refunds
		WHERE return_request_id = $1`, requestID).Scan(&status, &amount, &providerRef); err != nil {
		t.Fatalf("read refund: %v", err)
	}
	if status != "succeeded" {
		t.Errorf("refund status is %q, want succeeded", status)
	}
	if amount != 100000 {
		t.Errorf("refunded %d, want 100000 — one unit at the order's own price", amount)
	}
	if providerRef == nil || *providerRef == "" {
		t.Error("no provider reference recorded; the refund cannot be reconciled")
	}

	var returnStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
		t.Fatalf("read return: %v", err)
	}
	if returnStatus != "approved" {
		t.Errorf("return is %q, want approved", returnStatus)
	}
}

func TestAFailedRefundLeavesARowToReconcile(t *testing.T) {
	tests := []struct {
		name       string
		refunder   fakeRefunder
		wantStatus string
		why        string
	}{
		{
			name:       "stripe unreachable before anything was asked",
			refunder:   fakeRefunder{failIntent: true},
			wantStatus: "pending",
			why:        "goen never asked Stripe to refund, so the claim is still outstanding",
		},
		{
			name:       "the request timed out, so nobody knows what Stripe did",
			refunder:   fakeRefunder{refundErr: errors.New("context deadline exceeded")},
			wantStatus: "pending",
			why: "goen did not hear an answer, and 'failed' would claim the money " +
				"is still at the shop AND free the capture to be claimed twice",
		},
		{
			name: "stripe refused the refund",
			refunder: fakeRefunder{refundErr: &stripe.Error{
				Type: stripe.ErrorTypeInvalidRequest,
				Code: stripe.ErrorCodeChargeAlreadyRefunded,
				Msg:  "charge has already been refunded",
			}},
			wantStatus: "failed",
			why:        "Stripe was asked and said no",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := staffContext(t)
			s := admin.NewStore(pool, tt.refunder, nil)
			requestID, _ := returnedOrder(t, 2)

			if err := s.Decide(ctx, requestID.String(), "approved", "", uuid.NullUUID{}); err == nil {
				t.Fatal("a refund that did not happen was reported as success")
			}

			var status string
			var amount int64
			if err := pool.QueryRow(ctx, `
				SELECT status, amount_cents FROM refunds WHERE return_request_id = $1`,
				requestID).Scan(&status, &amount); err != nil {
				t.Fatalf("no refund row survives a failed provider call — nothing "+
					"can reconcile the money: %v", err)
			}
			if status != tt.wantStatus {
				t.Errorf("refund status is %q, want %q — %s", status, tt.wantStatus, tt.why)
			}
			if amount != 200000 {
				t.Errorf("row records %d, want 200000", amount)
			}

			var returnStatus string
			if err := pool.QueryRow(ctx,
				`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
				t.Fatalf("read return: %v", err)
			}
			// APPROVED, and that is the fix rather than a regression. The
			// decision commits before a cent moves, which is what stops two
			// staff members deciding one return at once from both paying — so
			// a refund that fails afterwards can no longer leave the return
			// open. What must be true instead is that the attempt is on record
			// and can be finished, which the row above and
			// TestAStalledRefundCanBeRetriedToCompletion hold.
			if returnStatus != "approved" {
				t.Errorf("return is %q after a refund that did not happen, want "+
					"approved — the shop DID agree to the return, and the money "+
					"is what is outstanding", returnStatus)
			}
		})
	}
}

func TestAStalledRefundCanBeRetriedToCompletion(t *testing.T) {
	ctx, _ := staffContext(t)
	requestID, _ := returnedOrder(t, 2)

	stalled := admin.NewStore(pool, fakeRefunder{
		refundErr: errors.New("read tcp 1.2.3.4:443: i/o timeout"),
	}, nil)
	if err := stalled.Decide(ctx, requestID.String(), "approved", "已收到退貨", uuid.NullUUID{}); err == nil {
		t.Fatal("a refund that timed out was reported as success")
	}

	healthy := admin.NewStore(pool, fakeRefunder{}, nil)
	if err := healthy.Decide(ctx, requestID.String(), "approved", "已收到退貨", uuid.NullUUID{}); err != nil {
		t.Fatalf("the retry was refused, so a stalled refund can never be finished "+
			"and the customer is never paid: %v", err)
	}

	var rows int
	var status string
	var succeededAt *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT count(*), min(status), min(succeeded_at) FROM refunds
		WHERE return_request_id = $1`, requestID).Scan(&rows, &status, &succeededAt); err != nil {
		t.Fatalf("read refunds: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d refund rows after a retry, want 1 — request_key exists so the "+
			"retry finds its own row rather than opening a second claim", rows)
	}
	if status != "succeeded" {
		t.Errorf("refund status is %q after a successful retry, want succeeded", status)
	}
	if succeededAt == nil {
		t.Error("no succeeded_at on a succeeded refund")
	}

	var returnStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
		t.Fatalf("read return: %v", err)
	}
	if returnStatus != "approved" {
		t.Errorf("return is %q after the refund finally went through, want approved", returnStatus)
	}
}

func TestAPendingProviderRefundIsNotRecordedAsSucceeded(t *testing.T) {
	tests := []struct {
		name  string
		state admin.RefundState
	}{
		{name: "stripe accepted it and has not settled it", state: admin.RefundPending},
		{name: "stripe needs something else to happen first", state: admin.RefundRequiresAction},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := staffContext(t)
			s := admin.NewStore(pool, fakeRefunder{state: tt.state}, nil)
			requestID, _ := returnedOrder(t, 1)

			if err := s.Decide(ctx, requestID.String(), "approved", "已收到退貨", uuid.NullUUID{}); err != nil {
				t.Fatalf("a refund Stripe ACCEPTED was treated as a failure: %v", err)
			}

			var status string
			var succeededAt, failedAt *time.Time
			var providerRef *string
			if err := pool.QueryRow(ctx, `
				SELECT status, succeeded_at, failed_at, provider_ref FROM refunds
				WHERE return_request_id = $1`, requestID).
				Scan(&status, &succeededAt, &failedAt, &providerRef); err != nil {
				t.Fatalf("read refund: %v", err)
			}
			if status != string(tt.state) {
				t.Errorf("refund status is %q, want %q — goen recorded a state the "+
					"provider never claimed", status, tt.state)
			}
			if succeededAt != nil {
				t.Errorf("succeeded_at is %v on a refund that has not succeeded", *succeededAt)
			}
			if failedAt != nil {
				t.Errorf("failed_at is %v on a refund that has not failed", *failedAt)
			}
			if providerRef == nil || *providerRef == "" {
				t.Error("no provider reference on a refund Stripe accepted — it is " +
					"the only handle anybody has for chasing it")
			}

			var returnStatus string
			if err := pool.QueryRow(ctx,
				`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
				t.Fatalf("read return: %v", err)
			}
			if returnStatus != "approved" {
				t.Errorf("return is %q, want approved — the shop accepted the goods back", returnStatus)
			}
			var refundEvents int
			if err := pool.QueryRow(ctx, `
				SELECT count(*) FROM order_events e
				JOIN return_requests r ON r.order_id = e.order_id
				WHERE r.id = $1 AND e.kind = 'refunded'`, requestID).Scan(&refundEvents); err != nil {
				t.Fatalf("count events: %v", err)
			}
			if refundEvents != 0 {
				t.Errorf("%d 'refunded' events on an order whose refund has not landed — "+
					"the customer reads that timeline", refundEvents)
			}
		})
	}
}

// TestARefundStripeRefusedOutrightLeavesTheDecisionStanding replaces a test that
// asserted the defect's own shape.
//
// It used to require the return to stay `requested` after a refund Stripe
// refused, so it could be decided again — which is only safe if deciding is
// where the money is settled, and it was not: the payout ran BEFORE the CAS, so
// two staff members deciding at once could both pay. The claim moved ahead of
// the money, and a failed refund therefore leaves the decision standing.
//
// The property that has to survive is the one the old name was reaching for —
// a customer who sent goods back must not be stranded — and it does, in two
// pieces the old contract folded into one: the refund row is on record, and
// approving again resumes the payout rather than retaking the decision.
func TestARefundStripeRefusedOutrightLeavesTheDecisionStanding(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{state: admin.RefundFailed}, nil)
	requestID, _ := returnedOrder(t, 1)

	if err := s.Decide(ctx, requestID.String(), "approved", "已收到退貨", uuid.NullUUID{}); err == nil {
		t.Fatal("a refund Stripe refused was reported as success")
	}

	var returnStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
		t.Fatalf("read return: %v", err)
	}
	if returnStatus != "approved" {
		t.Errorf("return is %q, want approved — the decision is taken before any "+
			"money moves, which is what stops two staff members both paying",
			returnStatus)
	}

	// On record, so nothing is lost while it is outstanding.
	var refundStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM refunds WHERE return_request_id = $1`, requestID).Scan(&refundStatus); err != nil {
		t.Fatalf("no refund row survives a refusal, so nothing can reconcile it: %v", err)
	}
	if refundStatus != "failed" {
		t.Errorf("refund is %q, want failed — Stripe was asked and said no", refundStatus)
	}

	// A refund Stripe REFUSED is not retryable here, and that is the schema's
	// deliberate position rather than a gap this change opened:
	// refunds_settled_is_history forbids `failed` becoming `succeeded`, and
	// refundRequestKey means a second attempt finds the same row. The old
	// contract left the return `requested` as though it could be decided again,
	// but any retry met the same wall — so what it offered was the appearance
	// of recovery, not recovery.
	//
	// What is real is that the refusal is on record and surfaced:
	// TestTheHealthPageNamesARefundThatDidNotLand holds /admin/health, and the
	// staff notice says to check the Stripe dashboard. A STALLED refund — one
	// left `pending` because nobody knows what Stripe did — is the case that
	// genuinely resumes, and TestAStalledRefundCanBeRetriedToCompletion holds it.
	healthy := admin.NewStore(pool, fakeRefunder{}, nil)
	err := healthy.Decide(ctx, requestID.String(), "approved", "已收到退貨", uuid.NullUUID{})
	if !errors.Is(err, admin.ErrRefundIncomplete) {
		t.Errorf("re-approving after a hard refusal gave %v, want ErrRefundIncomplete — "+
			"a staff member must be told the money still has not gone", err)
	}
}
func TestTheHealthPageNamesARefundThatDidNotLand(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{
		refundErr: errors.New("read tcp 1.2.3.4:443: i/o timeout"),
	}, nil)
	requestID, orderNumber := returnedOrder(t, 1)

	if err := s.Decide(ctx, requestID.String(), "approved", "", uuid.NullUUID{}); err == nil {
		t.Fatal("a refund that timed out was reported as success")
	}

	view, err := s.WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if view.RefundsHealthy() {
		t.Error("a refund that never left reads as healthy")
	}

	// Found by identity, never by position: the suite is shuffled and other tests leave refunds behind.
	var found *pages.OpenRefund
	for i := range view.OpenRefunds {
		if view.OpenRefunds[i].Key == "return:"+requestID.String() {
			found = &view.OpenRefunds[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("the stalled refund for return %s is on no page — the row exists "+
			"only for reconciliation and nothing can read it", requestID)
	}
	want := pages.OpenRefund{
		OrderNumber: orderNumber,
		Key:         "return:" + requestID.String(),
		Status:      "pending",
		AmountCents: 100000,
		ProviderRef: "",
		Since:       found.Since,
	}
	if diff := cmp.Diff(want, *found); diff != "" {
		t.Errorf("WorkerHealth() open refund mismatch (-want +got):\n%s", diff)
	}
}

func TestRejectingAReturnMovesNoMoney(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, _ := returnedOrder(t, 2)

	if err := s.Decide(ctx, requestID.String(), "rejected", "超過鑑賞期", uuid.NullUUID{}); err != nil {
		t.Fatalf("reject: %v", err)
	}

	var refunds int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refunds WHERE return_request_id = $1`, requestID).Scan(&refunds); err != nil {
		t.Fatalf("count: %v", err)
	}
	if refunds != 0 {
		t.Errorf("%d refunds written for a REJECTED return", refunds)
	}

	var status, resolution string
	if err := pool.QueryRow(ctx,
		`SELECT status, coalesce(resolution, '') FROM return_requests WHERE id = $1`,
		requestID).Scan(&status, &resolution); err != nil {
		t.Fatalf("read return: %v", err)
	}
	if status != "rejected" || resolution != "超過鑑賞期" {
		t.Errorf("return is %q/%q, want rejected and the reason the staff gave", status, resolution)
	}
}

func TestAReturnIsDecidedOnce(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, _ := returnedOrder(t, 1)

	if err := s.Decide(ctx, requestID.String(), "approved", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := s.Decide(ctx, requestID.String(), "approved", "", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
		t.Errorf("second decision gave %v, want ErrRefused", err)
	}

	var refunds int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refunds WHERE return_request_id = $1`, requestID).Scan(&refunds); err != nil {
		t.Fatalf("count: %v", err)
	}
	if refunds != 1 {
		t.Errorf("%d refunds after two approvals, want 1", refunds)
	}
}

func TestTheLoserOfTwoSimultaneousDecisionsWritesNoAuditRow(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, _ := returnedOrder(t, 1)

	before := auditRowsFor(t, requestID)

	t1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin T1: %v", err)
	}
	defer func() { _ = t1.Rollback(ctx) }()
	if _, err := t1.Exec(ctx, `
		UPDATE return_requests SET status = 'rejected', decided_at = now()
		WHERE id = $1 AND status = 'requested'`, requestID); err != nil {
		t.Fatalf("T1 decide: %v", err)
	}

	decided := make(chan error, 1)
	go func() { decided <- s.Decide(ctx, requestID.String(), "approved", "", uuid.NullUUID{}) }()

	select {
	case err := <-decided:
		t.Fatalf("T2 finished before T1 committed (%v); it never met the lock, "+
			"so this run proves nothing", err)
	case <-time.After(250 * time.Millisecond):
	}

	if err := t1.Commit(ctx); err != nil {
		t.Fatalf("commit T1: %v", err)
	}

	if err := <-decided; !errors.Is(err, admin.ErrRefused) {
		t.Errorf("the second decision returned %v, want ErrRefused — it updated no "+
			"row and reported success", err)
	}

	if after := auditRowsFor(t, requestID); after != before {
		t.Errorf("%d audit rows for this return, was %d — the losing decision was "+
			"recorded as though it had been made", after, before)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "rejected" {
		t.Errorf("the return is %q, want rejected — the loser overwrote the winner", status)
	}

	// THE MONEY. Every assertion above was true while the loser refunded: it
	// returned ErrRefused, wrote no audit row and left the winner's status
	// standing, because the payout happened before the CAS it went on to lose.
	// A count proves the database held the line; only this says whether a cent
	// moved.
	var refunds int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refunds WHERE return_request_id = $1`, requestID).Scan(&refunds); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	if refunds != 0 {
		t.Errorf("%d refunds against a return that was REJECTED — the losing "+
			"decision paid before it found out it had lost", refunds)
	}
}

func auditRowsFor(t *testing.T, requestID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM audit_events
		WHERE entity_table = 'return_requests' AND entity_id = $1`, requestID).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

func TestARefundCannotExceedWhatWasCaptured(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, orderNumber := returnedOrder(t, 2)

	if err := s.Decide(ctx, requestID.String(), "approved", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("first approval: %v", err)
	}

	// Written straight in: the return ceiling would refuse a second request before the refund guard.
	var orderID, lineID, second uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT o.id, ol.id FROM orders o JOIN order_lines ol ON ol.order_id = o.id
		WHERE o.order_number = $1`, orderNumber).Scan(&orderID, &lineID); err != nil {
		t.Fatalf("find order: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason) VALUES ($1, '再退一次') RETURNING id`,
		orderID).Scan(&second); err != nil {
		t.Fatalf("create second return: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 2)`, orderID, second, lineID); err == nil {
		if decideErr := s.Decide(ctx, second.String(), "approved", "", uuid.NullUUID{}); decideErr == nil {
			t.Fatal("the same order was refunded twice")
		}
	}

	var total int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(r.amount_cents), 0) FROM refunds r
		JOIN payments p ON p.id = r.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND r.status IN ('pending', 'succeeded')`,
		orderNumber).Scan(&total); err != nil {
		t.Fatalf("sum refunds: %v", err)
	}
	if total > 200000 {
		t.Errorf("%d refunded against a capture of 200000", total)
	}
}

func TestGrantIsBoundedAndPositive(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	var email string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role) VALUES ('grant-'||gen_random_uuid()||'@example.com', 'customer')
		RETURNING email`).Scan(&email); err != nil {
		t.Fatalf("create user: %v", err)
	}

	tests := []struct {
		name    string
		email   string
		cents   int64
		reason  string
		wantErr error
	}{
		{"over the ceiling", email, admin.MaxCreditGrant + 1, "手滑", admin.ErrInvalid},
		{"exactly the ceiling", email, admin.MaxCreditGrant, "上限", nil},
		{"zero", email, 0, "沒事", admin.ErrInvalid},
		{"negative", email, -50000, "扣款", admin.ErrInvalid},
		{"no reason", email, 50000, "", admin.ErrInvalid},
		{"whitespace reason", email, 50000, "   ", admin.ErrInvalid},
		{"no email", "", 50000, "補償", admin.ErrInvalid},
		{"unknown customer", "nobody@example.invalid", 50000, "補償", admin.ErrRefused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.GrantCredit(ctx, tt.email, tt.cents, tt.reason, uuid.NullUUID{})
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("a legal grant was refused: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}

	var balance int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(e.amount_cents), 0) FROM store_credit_entries e
		JOIN store_credit_accounts a ON a.id = e.account_id
		JOIN users u ON u.id = a.user_id WHERE u.email = $1`, email).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if balance != admin.MaxCreditGrant {
		t.Errorf("balance is %d, want %d — a refused grant still posted",
			balance, admin.MaxCreditGrant)
	}
}

func TestGrantIsIdempotent(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	var email string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role) VALUES ('idem-'||gen_random_uuid()||'@example.com', 'customer')
		RETURNING email`).Scan(&email); err != nil {
		t.Fatalf("create user: %v", err)
	}

	for range 3 {
		if _, err := s.GrantCredit(ctx, email, 50000, "退貨補償", uuid.NullUUID{}); err != nil {
			t.Fatalf("grant: %v", err)
		}
	}

	var balance int64
	var entries int
	read := func() {
		t.Helper()
		if err := pool.QueryRow(ctx, `
			SELECT coalesce(sum(e.amount_cents), 0), count(*) FROM store_credit_entries e
			JOIN store_credit_accounts a ON a.id = e.account_id
			JOIN users u ON u.id = a.user_id WHERE u.email = $1`, email).Scan(&balance, &entries); err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	read()
	if entries != 1 || balance != 50000 {
		t.Errorf("%d entries totalling %d after submitting the same grant three "+
			"times, want 1 of 50000", entries, balance)
	}

	if _, err := s.GrantCredit(ctx, email, 30000, "另一次補償", uuid.NullUUID{}); err != nil {
		t.Fatalf("second, different grant: %v", err)
	}
	read()
	if entries != 2 || balance != 80000 {
		t.Errorf("%d entries totalling %d after a second, different grant, want 2 of 80000",
			entries, balance)
	}

	if _, err := s.GrantCredit(ctx, email, 30000, "第三次補償", uuid.NullUUID{}); err != nil {
		t.Fatalf("third grant: %v", err)
	}
	read()
	if entries != 3 || balance != 110000 {
		t.Errorf("%d entries totalling %d after a same-amount different-reason "+
			"grant, want 3 of 110000", entries, balance)
	}
}

func TestTheBackOfficeIsInvisibleToEveryoneButStaff(t *testing.T) {
	ctx := t.Context()
	h := admin.NewHandler(admin.NewStore(pool, fakeRefunder{}, nil),
		media.NewHandler(media.NewStore(pool), slog.New(slog.DiscardHandler)),
		outbox.NewStore(pool, slog.New(slog.DiscardHandler)),
		newsletter.NewStore(pool),
		slog.New(slog.DiscardHandler),
		nil,
		nil)

	var customerID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role) VALUES ('shopper-'||gen_random_uuid()||'@example.com', 'customer')
		RETURNING id`).Scan(&customerID); err != nil {
		t.Fatalf("create customer: %v", err)
	}

	guarded := h.RequireStaff(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("the back office"))
	})

	tests := []struct {
		name     string
		signedIn bool
		user     account.User
	}{
		{name: "signed out"},
		{name: "a signed-in customer", signedIn: true,
			user: account.User{ID: customerID.String(), Role: "customer"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin", nil)
			if tt.signedIn {
				req = req.WithContext(account.WithUser(req.Context(), tt.user))
			}
			w := httptest.NewRecorder()
			guarded(w, req)

			if w.Code != http.StatusNotFound {
				t.Errorf("status is %d, want 404 — anything else says /admin is a "+
					"real place", w.Code)
			}
			if loc := w.Header().Get("Location"); loc != "" {
				t.Errorf("redirected to %q; a redirect confirms the page exists and "+
					"puts its path in the visitor's history", loc)
			}
			if strings.Contains(w.Body.String(), "the back office") {
				t.Error("the handler ran")
			}
		})
	}

	// BOTH back-office roles, and 'staff' is the one that matters: this block
	// asserted role 'admin' under a variable named staffID and a comment saying
	// "create staff", so the role /admin/staff actually offers was never tested.
	// RequireStaff asked IsAdmin, and every colleague hired as staff met a 404 on
	// the whole back office.
	for _, role := range twofactor.Roles {
		t.Run(role+" reaches the back office", func(t *testing.T) {
			var id uuid.UUID
			if err := pool.QueryRow(ctx, `
				INSERT INTO users (email, role) VALUES ('bo-'||gen_random_uuid()||'@example.com', $1)
				RETURNING id`, role).Scan(&id); err != nil {
				t.Fatalf("create %s: %v", role, err)
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin", nil)
			req = req.WithContext(account.WithUser(req.Context(),
				account.User{ID: id.String(), Role: role}))
			w := httptest.NewRecorder()
			guarded(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("%s got %d, want 200 — /admin/staff offers this role, so a "+
					"colleague hired into it can do no work at all", role, w.Code)
			}
		})
	}
}

func TestOnlyAnAdminReachesTheStaffPage(t *testing.T) {
	ctx := t.Context()
	h := admin.NewHandler(admin.NewStore(pool, fakeRefunder{}, nil),
		media.NewHandler(media.NewStore(pool), slog.New(slog.DiscardHandler)),
		outbox.NewStore(pool, slog.New(slog.DiscardHandler)),
		newsletter.NewStore(pool),
		slog.New(slog.DiscardHandler),
		nil,
		nil)

	guarded := h.RequireAdmin(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("who works here"))
	})

	newUser := func(role string) account.User {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (email, role)
			VALUES ('`+role+`-'||gen_random_uuid()||'@example.com', $1)
			RETURNING id`, role).Scan(&id); err != nil {
			t.Fatalf("create %s: %v", role, err)
		}
		return account.User{ID: id.String(), Role: role}
	}

	for _, tt := range []struct {
		name     string
		signedIn bool
		user     account.User
		want     int
	}{
		{name: "signed out", want: http.StatusNotFound},
		{name: "a customer", signedIn: true, user: newUser("customer"), want: http.StatusNotFound},
		{name: "a staff member", signedIn: true, user: newUser("staff"), want: http.StatusNotFound},
		{name: "an admin", signedIn: true, user: newUser("admin"), want: http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/staff", nil)
			if tt.signedIn {
				req = req.WithContext(account.WithUser(req.Context(), tt.user))
			}
			w := httptest.NewRecorder()
			guarded(w, req)

			if w.Code != tt.want {
				t.Errorf("status is %d, want %d", w.Code, tt.want)
			}
			ran := strings.Contains(w.Body.String(), "who works here")
			if want := tt.want == http.StatusOK; ran != want {
				t.Errorf("the handler ran = %v, want %v", ran, want)
			}
		})
	}
}

func staffContext(t *testing.T) (context.Context, uuid.UUID) {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('audit-' || gen_random_uuid() || '@goen.invalid', 'admin', '稽核測試')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	ctx := account.WithUser(t.Context(), account.User{ID: id.String(), Role: "admin"})
	return web.WithRequestID(ctx, "req-"+id.String()[:8]), id
}

func auditRows(t *testing.T, action admin.Action) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM audit_events WHERE action = $1`, string(action)).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

func TestEveryBackOfficeWriteLeavesATrail(t *testing.T) {
	ctx, actor := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	sku := anyVariantSKU(t)
	slug := anyProductSlug(t)

	tests := []struct {
		name   string
		action admin.Action
		run    func() error
	}{
		{"adjust stock", admin.ActionAdjustStock, func() error {
			return s.AdjustStock(ctx, sku, 3, actor.String(), uuid.NewString())
		}},
		{"reprice", admin.ActionRepriceVariant, func() error {
			return s.SetVariantPrice(ctx, sku, 123400, 0)
		}},
		{"publish", admin.ActionPublishProduct, func() error {
			return s.SetProductStatus(ctx, slug, "draft")
		}},
		{"grant credit", admin.ActionGrantCredit, func() error {
			_, grantErr := s.GrantCredit(ctx, staffEmail(t, actor), 500,
				"測試", uuid.NullUUID{UUID: actor, Valid: true})
			return grantErr
		}},
		{"create campaign", admin.ActionCreateCampaign, func() error {
			_, err := s.CreateCampaign(ctx, &admin.CampaignForm{
				Slug: "trail-" + uuid.NewString()[:8], Title: "紀錄", Days: 7,
			})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := auditRows(t, tt.action)
			if err := tt.run(); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if after := auditRows(t, tt.action); after != before+1 {
				t.Errorf("%s left %d audit rows, want one more than %d — the action "+
					"happened and nobody can say who did it", tt.name, after, before)
			}
		})
	}
}

func TestAnAuditRowNamesItsActorAndRequest(t *testing.T) {
	ctx, actor := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	if err := s.SetProductStatus(ctx, anyProductSlug(t), "draft"); err != nil {
		t.Fatalf("publish: %v", err)
	}

	var gotActor uuid.UUID
	var requestID, action string
	if err := pool.QueryRow(ctx, `
		SELECT actor_user_id, coalesce(request_id, ''), action FROM audit_events
		WHERE actor_user_id = $1 ORDER BY occurred_at DESC LIMIT 1`, actor).
		Scan(&gotActor, &requestID, &action); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if gotActor != actor {
		t.Errorf("actor is %s, want %s", gotActor, actor)
	}
	if requestID == "" {
		t.Error("no request id; the row cannot be put beside the log lines from " +
			"the same request")
	}
	if action != string(admin.ActionPublishProduct) {
		t.Errorf("action is %q", action)
	}
}

func TestAnActionWithNoActorIsRefused(t *testing.T) {
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	slug := anyProductSlug(t)

	// Read before and compared after: anyProductSlug can hand back a product already in the target status.
	var before string
	if scanErr := pool.QueryRow(t.Context(),
		`SELECT status FROM products WHERE slug = $1`, slug).Scan(&before); scanErr != nil {
		t.Fatalf("read product: %v", scanErr)
	}
	target := "draft"
	if before == "draft" {
		target = "archived"
	}

	err := s.SetProductStatus(t.Context(), slug, target)
	if !errors.Is(err, admin.ErrNoActor) {
		t.Fatalf("a back-office write with no actor gave %v, want ErrNoActor", err)
	}

	var after string
	if scanErr := pool.QueryRow(t.Context(),
		`SELECT status FROM products WHERE slug = $1`, slug).Scan(&after); scanErr != nil {
		t.Fatalf("read product: %v", scanErr)
	}
	if after != before {
		t.Errorf("the write landed anyway (%s → %s); the audit failure must roll it back",
			before, after)
	}
}

func TestAFailedWriteLeavesNoAuditRow(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	before := auditRows(t, admin.ActionPublishProduct)
	if err := s.SetProductStatus(ctx, anyProductSlug(t), "nonsense"); err == nil {
		t.Fatal("an invalid status was accepted")
	}
	if err := s.SetProductStatus(ctx, "no-such-product-"+uuid.NewString(), "active"); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("publishing an absent product gave %v, want ErrNotFound", err)
	}
	if after := auditRows(t, admin.ActionPublishProduct); after != before {
		t.Errorf("%d audit rows after a refused write, want %d", after, before)
	}
}

func TestTheTrailCannotBeRewritten(t *testing.T) {
	ctx, _ := staffContext(t)
	if err := admin.NewStore(pool, fakeRefunder{}, nil).
		SetProductStatus(ctx, anyProductSlug(t), "draft"); err != nil {
		t.Fatalf("seed a row: %v", err)
	}

	tests := []struct {
		name string
		stmt string
	}{
		{"update", `UPDATE audit_events SET action = 'rewritten'`},
		{"delete", `DELETE FROM audit_events`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tt.stmt); err == nil {
				t.Fatalf("%s succeeded against an append-only table", tt.name)
			} else if _, name := constraintFrom(err); name != "audit_events_append_only" {
				t.Errorf("refused by %q, want audit_events_append_only: %v", name, err)
			}
		})
	}

	if err := asAdmin(ctx, t, `INSERT INTO audit_events (action, entity_table)
		VALUES ('forged', 'x')`); err == nil {
		t.Error("admin inserted an audit row directly, bypassing record_audit_event")
	}

	if err := asAdmin(ctx, t, `SELECT record_audit_event(
		(SELECT id FROM users LIMIT 1), 'test.control', 'products', NULL)`); err != nil {
		t.Errorf("admin cannot call record_audit_event: %v", err)
	}
}

func constraintFrom(err error) (code, name string) {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return "", ""
	}
	return pgErr.Code, pgErr.ConstraintName
}

func anyVariantSKU(t *testing.T) string {
	t.Helper()
	var sku string
	if err := pool.QueryRow(t.Context(),
		`SELECT sku FROM product_variants LIMIT 1`).Scan(&sku); err != nil {
		t.Fatalf("find variant: %v", err)
	}
	return sku
}

func anyProductSlug(t *testing.T) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(),
		`SELECT slug FROM products LIMIT 1`).Scan(&slug); err != nil {
		t.Fatalf("find product: %v", err)
	}
	return slug
}

func staffEmail(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var email string
	if err := pool.QueryRow(t.Context(),
		`SELECT email FROM users WHERE id = $1`, id).Scan(&email); err != nil {
		t.Fatalf("read staff email: %v", err)
	}
	return email
}

// asAdmin runs one statement with the back office's own database role: the suite
// otherwise connects as the owner, who is subject to no REVOKE at all.
func asAdmin(ctx context.Context, t *testing.T, stmt string) error {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatalf("set role admin: %v", err)
	}
	// RESET before release, or the pooled connection hands the admin role to whatever runs next.
	defer func() {
		if _, resetErr := conn.Exec(ctx, `RESET ROLE`); resetErr != nil {
			t.Errorf("reset role: %v", resetErr)
		}
	}()

	_, execErr := conn.Exec(ctx, stmt)
	return execErr
}

func TestANamedParentThatDoesNotExistIsRefused(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	errs, err := s.CreateCategory(ctx, &admin.TaxonomyForm{
		Slug: "orphan-" + uuid.NewString()[:8], Name: "孤兒", Parent: "no-such-parent",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, refused := errs["parent"]; !refused {
		t.Errorf("a category naming a parent that does not exist was accepted "+
			"(errors: %v)", errs)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM categories WHERE name = '孤兒'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Error("the category was created anyway, at the root")
	}
}

func TestACategoryIsCreatedUnderTheParentItNames(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	parent := "tax-parent-" + uuid.NewString()[:8]
	if errs, err := s.CreateCategory(ctx, &admin.TaxonomyForm{
		Slug: parent, Name: "上層",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("create parent: err=%v errs=%v", err, errs)
	}

	child := "tax-child-" + uuid.NewString()[:8]
	if errs, err := s.CreateCategory(ctx, &admin.TaxonomyForm{
		Slug: child, Name: "下層", Parent: parent,
	}); err != nil || len(errs) > 0 {
		t.Fatalf("create child: err=%v errs=%v", err, errs)
	}

	var got string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(p.slug, '') FROM categories c
		LEFT JOIN categories p ON p.id = c.parent_id
		WHERE c.slug = $1`, child).Scan(&got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != parent {
		t.Errorf("the child sits under %q, want %q", got, parent)
	}
}

func TestSomethingInUseCannotBeDeleted(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	empty := "tax-brand-" + uuid.NewString()[:8]
	if errs, err := s.CreateBrand(ctx, &admin.TaxonomyForm{Slug: empty, Name: "空品牌"}); err != nil || len(errs) > 0 {
		t.Fatalf("create: err=%v errs=%v", err, errs)
	}
	if err := s.Delete(ctx, "brand", empty); err != nil {
		t.Errorf("an unused brand was not deleted: %v", err)
	}

	var used string
	if err := pool.QueryRow(ctx, `
		SELECT b.slug FROM brands b
		WHERE EXISTS (SELECT 1 FROM products p WHERE p.brand_id = b.id)
		LIMIT 1`).Scan(&used); err != nil {
		t.Fatalf("find a used brand: %v", err)
	}
	if err := s.Delete(ctx, "brand", used); !errors.Is(err, admin.ErrInUse) {
		t.Errorf("deleting a brand with products gave %v, want ErrInUse", err)
	}

	parent := "tax-p-" + uuid.NewString()[:8]
	child := "tax-c-" + uuid.NewString()[:8]
	for _, c := range []admin.TaxonomyForm{
		{Slug: parent, Name: "有子分類"},
		{Slug: child, Name: "子", Parent: parent},
	} {
		if errs, err := s.CreateCategory(ctx, &c); err != nil || len(errs) > 0 {
			t.Fatalf("create %s: err=%v errs=%v", c.Slug, err, errs)
		}
	}
	if err := s.Delete(ctx, "category", parent); !errors.Is(err, admin.ErrInUse) {
		t.Errorf("deleting a category with children gave %v, want ErrInUse", err)
	}
	if err := s.Delete(ctx, "category", child); err != nil {
		t.Fatalf("delete child: %v", err)
	}
	if err := s.Delete(ctx, "category", parent); err != nil {
		t.Errorf("the parent was still refused once emptied: %v", err)
	}
}

func TestASlugIsNeverRenamed(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	slug := "tax-rename-" + uuid.NewString()[:8]
	if errs, err := s.CreateBrand(ctx, &admin.TaxonomyForm{Slug: slug, Name: "原名"}); err != nil || len(errs) > 0 {
		t.Fatalf("create: err=%v errs=%v", err, errs)
	}
	if err := s.Rename(ctx, "brand", slug, "新名字", "", ""); err != nil {
		t.Fatalf("rename: %v", err)
	}

	var name string
	if err := pool.QueryRow(ctx,
		`SELECT name FROM brands WHERE slug = $1`, slug).Scan(&name); err != nil {
		t.Fatalf("the slug changed, or the row is gone: %v", err)
	}
	if name != "新名字" {
		t.Errorf("name is %q, want 新名字", name)
	}
	if err := s.Rename(ctx, "brand", slug, "   ", "", ""); !errors.Is(err, admin.ErrInvalid) {
		t.Errorf("a blank name gave %v, want ErrInvalid", err)
	}
}

func TestRevenueCountsOnlyCommittedOrders(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

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

func TestTheWindowIsAnAllowlist(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

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
		if view.Days != int(admin.DefaultWindow) {
			t.Errorf("report(%d) reported %d days, want the default %d",
				days, view.Days, admin.DefaultWindow)
		}
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
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

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

func TestTheQueuePutsWhatTheShopOwesFirst(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	ps := product.NewStore(pool)
	asker := newAskingCustomer(t)
	slug := anyActiveProductSlug(t)

	old := ask(t, ps, slug, asker, "最舊的,店家已回", -3)
	if err := s.AnswerQuestion(ctx, old, asker, "店家的回答"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	middling := ask(t, ps, slug, asker, "中間的,只有顧客回", -2)
	if err := ps.Answer(ctx, middling, asker, "我覺得可以", false); err != nil {
		t.Fatalf("customer answer: %v", err)
	}
	newest := ask(t, ps, slug, asker, "最新的,沒人回", -1)

	view, err := s.Questions(ctx)
	if err != nil {
		t.Fatalf("questions: %v", err)
	}
	order := make([]string, 0, len(view.Rows))
	for _, q := range view.Rows {
		if q.ID == old || q.ID == middling || q.ID == newest {
			order = append(order, q.Body)
		}
	}
	if len(order) != 3 {
		t.Fatalf("%d of the three questions are in the queue", len(order))
	}
	if order[0] != "中間的,只有顧客回" {
		t.Errorf("first is %q, want the oldest one the shop still owes", order[0])
	}
	if order[2] != "最舊的,店家已回" {
		t.Errorf("last is %q, want the one the shop already answered", order[2])
	}
}

func TestHidingAQuestionIsRecordedAndCannotBeRepeated(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	ps := product.NewStore(pool)
	asker := newAskingCustomer(t)
	id := ask(t, ps, anyActiveProductSlug(t), asker, "會被隱藏的", 0)

	before := auditRows(t, admin.ActionHideQuestion)
	if err := s.HideQuestion(ctx, id); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if after := auditRows(t, admin.ActionHideQuestion); after != before+1 {
		t.Errorf("hiding left %d audit rows, want one more than %d", after, before)
	}
	if err := s.HideQuestion(ctx, id); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("hiding twice gave %v, want ErrNotFound", err)
	}
	if after := auditRows(t, admin.ActionHideQuestion); after != before+1 {
		t.Errorf("a refused hide wrote an audit row")
	}
}

func TestAnEmptyOfficialAnswerIsRefused(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	ps := product.NewStore(pool)
	asker := newAskingCustomer(t)
	id := ask(t, ps, anyActiveProductSlug(t), asker, "等一個回答", 0)

	for _, body := range []string{"", "   ", "\t\n"} {
		if err := s.AnswerQuestion(ctx, id, asker, body); !errors.Is(err, admin.ErrInvalid) {
			t.Errorf("%q gave %v, want ErrInvalid", body, err)
		}
	}
	if err := s.AnswerQuestion(ctx, id, asker, "真的回答"); err != nil {
		t.Errorf("a real answer was refused: %v", err)
	}
}

func ask(t *testing.T, s *product.Store, slug, userID, body string, days int) string {
	t.Helper()
	ctx := t.Context()
	if err := s.Ask(ctx, slug, userID, body); err != nil {
		t.Fatalf("ask %q: %v", body, err)
	}
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		UPDATE product_questions SET created_at = now() + make_interval(days => $2)
		WHERE body = $1 RETURNING id`, body, days).Scan(&id); err != nil {
		t.Fatalf("backdate %q: %v", body, err)
	}
	return id.String()
}

func newAskingCustomer(t *testing.T) string {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('aq-' || gen_random_uuid() || '@goen.invalid', 'customer', '提問者')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return id.String()
}

func anyActiveProductSlug(t *testing.T) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(),
		`SELECT slug FROM products WHERE status = 'active' LIMIT 1`).Scan(&slug); err != nil {
		t.Fatalf("find product: %v", err)
	}
	return slug
}

func TestHealthIsDerivedFromTheWorkNotFromAHeartbeat(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	if _, err := pool.Exec(ctx, `DELETE FROM outbox_messages`); err != nil {
		t.Fatalf("clear outbox: %v", err)
	}
	clean, err := s.WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if !clean.OutboxHealthy() {
		t.Errorf("an empty outbox reads as unhealthy: %s", clean.OutboxText(ctx))
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO outbox_messages (topic, dedupe_key, payload, attempts, available_at)
		VALUES ('test.health', 'health-stuck', '{}'::jsonb, 99, now())`); err != nil {
		t.Fatalf("insert stuck: %v", err)
	}
	stuck, stuckErr := s.WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if stuckErr != nil {
		t.Fatalf("health: %v", stuckErr)
	}
	if stuck.OutboxHealthy() {
		t.Error("a message that has run out of attempts reads as healthy")
	}
	if stuck.OutboxStuck != 1 {
		t.Errorf("%d stuck messages, want 1", stuck.OutboxStuck)
	}
}

// TestAnAcknowledgedPaymentLeavesTheAlarm is the other half of flagging one.
// The refund is at Stripe and nothing here can see it land, so the alarm has an
// off switch or /admin/health is unhealthy forever after the first arrival —
// and an alarm that is always on is one nobody reads.
func TestAnAcknowledgedPaymentLeavesTheAlarm(t *testing.T) {
	ctx, actor := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))

	eventID := "evt_ack_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events (provider, event_id, type, payload, unreconciled)
		VALUES ('stripe', $1, 'checkout.session.completed', '{}'::jsonb,
		        'money arrived for an order that was already cancelled')`,
		eventID); err != nil {
		t.Fatalf("flag the event: %v", err)
	}

	flagged, err := s.WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if flagged.PaymentsReconciled() {
		t.Fatal("money arrived for a cancelled order and /admin/health says there " +
			"is nothing to do, so this proves nothing about clearing it")
	}

	if err := s.ReconcilePayment(ctx, eventID, uuid.NullUUID{UUID: actor, Valid: true}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	settled, settledErr := s.WorkerHealth(ctx, worker)
	if settledErr != nil {
		t.Fatalf("health: %v", settledErr)
	}
	if !settled.PaymentsReconciled() {
		t.Errorf("%d payments still read as unreconciled after the refund was "+
			"acknowledged — the alarm is monotone and stops meaning anything",
			settled.UnreconciledPayments)
	}
	if auditRows(t, admin.ActionReconcilePayment) == 0 {
		t.Error("saying the money went back by hand is the shop's statement about " +
			"money and it left no audit row")
	}

	// A second press changes nothing: the row count is where the question is
	// asked, so there is no read-then-write for two staff members to both pass.
	if err := s.ReconcilePayment(ctx, eventID, uuid.NullUUID{UUID: actor, Valid: true}); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("acknowledging it twice = %v, want ErrNotFound", err)
	}
	// And an event nobody flagged is not acknowledgeable at all.
	unflagged := "evt_plain_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events (provider, event_id, type, payload)
		VALUES ('stripe', $1, 'payment_intent.processing', '{}'::jsonb)`, unflagged); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.ReconcilePayment(ctx, unflagged, uuid.NullUUID{UUID: actor, Valid: true}); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("acknowledging an event that was never flagged = %v, want ErrNotFound", err)
	}
}

func TestAMessageWaitingOnItsBackoffIsNotLate(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	if _, err := pool.Exec(ctx, `DELETE FROM outbox_messages`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO outbox_messages (topic, dedupe_key, payload, attempts, available_at)
		VALUES ('test.health', 'health-backoff', '{}'::jsonb, 1, now() + interval '1 hour')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	view, err := s.WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if view.OutboxPending != 1 {
		t.Errorf("%d pending, want 1", view.OutboxPending)
	}
	if view.OutboxOldest != 0 {
		t.Errorf("a message not yet due reads as %v overdue", view.OutboxOldest)
	}
	if !view.OutboxHealthy() {
		t.Errorf("a message waiting on its backoff reads as unhealthy: %s", view.OutboxText(ctx))
	}
}

func TestNeverRebuiltIsNotTheSameAsJustRebuilt(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	if _, err := pool.Exec(ctx, `DELETE FROM product_copurchases`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	never, err := s.WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if never.CopurchaseEverBuilt {
		t.Error("an empty projection reports itself as built")
	}
	if never.RecommendHealthy() {
		t.Error("a projection that has never been rebuilt reads as healthy")
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO product_copurchases (product_id, other_product_id, orders)
		SELECT p1.id, p2.id, 2 FROM products p1, products p2
		WHERE p1.id <> p2.id LIMIT 1`); err != nil {
		t.Fatalf("seed projection: %v", err)
	}
	fresh, freshErr := s.WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if freshErr != nil {
		t.Fatalf("health: %v", freshErr)
	}
	if !fresh.CopurchaseEverBuilt || !fresh.RecommendHealthy() {
		t.Errorf("a just-rebuilt projection reads as %s", fresh.RecommendText(ctx))
	}
}

func TestCancellingAnOrderInTheBackOfficeReturnsItsStock(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	var vid uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.stock_quantity > pv.safety_stock + 1
		ORDER BY pv.id LIMIT 1`).Scan(&vid); err != nil {
		t.Fatalf("find a sellable variant: %v", err)
	}
	number := placeHeldOrder(t, vid)

	var held int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&held); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	var staff uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '取消人員')
		RETURNING id`, "cancel-"+number+"@goen.invalid").Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	// record_audit_event reads the actor from the CONTEXT, not from the parameter.
	staffCtx := account.WithUser(ctx, account.User{ID: staff.String(), Role: "admin"})
	if _, err := s.Advance(staffCtx, number, "cancelled", uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	var after int32
	var state string
	if err := pool.QueryRow(ctx, `
		SELECT pv.stock_quantity,
		       (SELECT r.state FROM inventory_reservations r
		        JOIN orders o ON o.id = r.order_id
		        WHERE o.order_number = $2 LIMIT 1)
		FROM product_variants pv WHERE pv.id = $1`, vid, number).Scan(&after, &state); err != nil {
		t.Fatalf("read stock after: %v", err)
	}
	if after != held+1 {
		t.Errorf("stock is %d after cancelling, want %d — the hold was not released", after, held+1)
	}
	if state != "released" {
		t.Errorf("the hold is %s, want released", state)
	}
}

func placeHeldOrder(t *testing.T, vid uuid.UUID) string {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	var number string
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
		VALUES ($1, $2, 'ADMIN-HELD', '測試商品', 500000, 1)`, orderID, vid); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'held@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, now() + interval '30 minutes', $3)`,
		orderID, vid, "admin-cancel:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

func TestShippingEnqueuesTheDispatchNotice(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number := shippableOrder(t, "en")

	var staff uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '出貨')
		RETURNING id`, "dispatch-"+number+"@goen.invalid").Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	staffCtx := account.WithUser(ctx, account.User{ID: staff.String(), Role: "admin"})

	if err := s.Ship(staffCtx, number, admin.Dispatch{Carrier: "黑貓宅急便", Tracking: "903-2214-0001"},
		uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("ship: %v", err)
	}

	// Found by what the notice IS about, not by the dedupe key: the key is
	// deduplication's business — it carries the carrier as well now, because
	// order_shipments is unique on the pair — and a test bound to it asserts the
	// scheme rather than the notice.
	var payload []byte
	if err := pool.QueryRow(ctx,
		`SELECT payload FROM outbox_messages
		 WHERE topic = 'order.shipped' AND payload->>'tracking' = $1`,
		"903-2214-0001").Scan(&payload); err != nil {
		t.Fatalf("no dispatch notice was enqueued for %s: %v", number, err)
	}
	var got admin.OrderShipped
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode notice: %v", err)
	}
	want := admin.OrderShipped{
		OrderNumber: number, Email: "ship@example.com", Name: "收件人",
		Carrier: "黑貓宅急便", Tracking: "903-2214-0001",
		Locale: "en",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("dispatch notice (-want +got):\n%s", diff)
	}
}

func TestRestockingTellsEverybodyWhoAsked(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	var vid uuid.UUID
	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT pv.id, pv.sku FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active ORDER BY pv.id LIMIT 1`).Scan(&vid, &sku); err != nil {
		t.Fatalf("find a variant: %v", err)
	}
	emptyTheShelf(t, vid, "restock-test-empty")
	for _, address := range []string{"waiting1@example.com", "waiting2@example.com"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO stock_notifications (variant_id, email) VALUES ($1, $2)`,
			vid, address); err != nil {
			t.Fatalf("record interest from %s: %v", address, err)
		}
	}

	var actor uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '補貨')
		RETURNING id`, "restock-"+sku+"@goen.invalid").Scan(&actor); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	staffCtx := account.WithUser(ctx, account.User{ID: actor.String(), Role: "admin"})

	if err := s.AdjustStock(staffCtx, sku, 10, actor.String(), "restock-test-1"); err != nil {
		t.Fatalf("restock: %v", err)
	}

	var enqueued int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages m
		JOIN stock_notifications sn ON sn.id::text = m.dedupe_key
		WHERE m.topic = 'catalogue.restocked' AND sn.variant_id = $1`, vid).Scan(&enqueued); err != nil {
		t.Fatalf("count notices: %v", err)
	}
	if enqueued != 2 {
		t.Errorf("%d restock notices enqueued, want 2", enqueued)
	}

	var pending int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM stock_notifications WHERE variant_id = $1 AND notified_at IS NULL`,
		vid).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pending != 0 {
		t.Errorf("%d notices are still pending after the restock", pending)
	}

	if err := s.AdjustStock(staffCtx, sku, 5, actor.String(), "restock-test-2"); err != nil {
		t.Fatalf("second restock: %v", err)
	}
	var after int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages m
		JOIN stock_notifications sn ON sn.id::text = m.dedupe_key
		WHERE m.topic = 'catalogue.restocked' AND sn.variant_id = $1`, vid).Scan(&after); err != nil {
		t.Fatalf("count notices again: %v", err)
	}
	if after != enqueued {
		t.Errorf("a second restock enqueued %d more notices", after-enqueued)
	}
}

func TestAnAdjustmentBelowTheThresholdTellsNobody(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	var vid uuid.UUID
	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT pv.id, pv.sku FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active ORDER BY pv.id DESC LIMIT 1`).Scan(&vid, &sku); err != nil {
		t.Fatalf("find a variant: %v", err)
	}
	emptyTheShelf(t, vid, "threshold-empty")
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET safety_stock = 5 WHERE id = $1`, vid); err != nil {
		t.Fatalf("set safety stock: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO stock_notifications (variant_id, email) VALUES ($1, 'threshold@example.com')`,
		vid); err != nil {
		t.Fatalf("record interest: %v", err)
	}

	var actor uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '補貨')
		RETURNING id`, "threshold-"+sku+"@goen.invalid").Scan(&actor); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	staffCtx := account.WithUser(ctx, account.User{ID: actor.String(), Role: "admin"})

	if err := s.AdjustStock(staffCtx, sku, 3, actor.String(), "threshold-test-1"); err != nil {
		t.Fatalf("adjust: %v", err)
	}
	var pending int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM stock_notifications WHERE variant_id = $1 AND notified_at IS NULL`,
		vid).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pending != 1 {
		t.Errorf("a notice was spent on stock nobody can buy (%d pending, want 1)", pending)
	}

	if err := s.AdjustStock(staffCtx, sku, 5, actor.String(), "threshold-test-2"); err != nil {
		t.Fatalf("second adjust: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM stock_notifications WHERE variant_id = $1 AND notified_at IS NULL`,
		vid).Scan(&pending); err != nil {
		t.Fatalf("count pending again: %v", err)
	}
	if pending != 0 {
		t.Errorf("crossing the threshold told nobody (%d still pending)", pending)
	}
}

// emptyTheShelf takes a variant down to zero. It reads the quantity first because
// inventory_movements_delta_non_zero refuses a movement of nothing.
func emptyTheShelf(t *testing.T, vid uuid.UUID, key string) {
	t.Helper()
	var stock int32
	if err := pool.QueryRow(t.Context(),
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&stock); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if stock > 0 {
		if _, err := pool.Exec(t.Context(),
			`SELECT record_inventory_movement($1, $2, 'adjustment', $3, NULL, NULL, NULL)`,
			vid, -stock, key); err != nil {
			t.Fatalf("empty the shelf: %v", err)
		}
	}
	if err := pool.QueryRow(t.Context(),
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&stock); err != nil {
		t.Fatalf("re-read stock: %v", err)
	}
	if stock != 0 {
		t.Fatalf("the shelf still holds %d — the fixture did not reach its precondition", stock)
	}
}

func shippableOrder(t *testing.T, locale string) string {
	t.Helper()
	ctx := t.Context()

	var variantID uuid.UUID
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
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents, locale)
		SELECT next_order_number(), v.id, sm.code, v.name, 0, $1
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, locale).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, $2, 'SHIP-SKU', '測試商品', 100000, 1)`, orderID, variantID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, now() + interval '30 minutes', $3)`,
		orderID, variantID, "ship-fixture:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'ship@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`,
		orderID, "cs_ship_"+number); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`,
		"cs_ship_"+number); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move to picking: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

// TestAnOverClaimIsRefusedInWordsRatherThanByAConstraint goes through a goodwill refund:
// a second return meets return_within_shipment first and never reaches this guard.
func TestAnOverClaimIsRefusedInWordsRatherThanByAConstraint(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, orderNumber := returnedOrder(t, 2)

	var paymentID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT p.id FROM payments p JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND p.status = 'succeeded'`,
		orderNumber).Scan(&paymentID); err != nil {
		t.Fatalf("find payment: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT open_refund($1, $2, 150000, '善意退款', NULL)`,
		paymentID, "goodwill:"+orderNumber); err != nil {
		t.Fatalf("post the goodwill refund: %v", err)
	}

	err := s.Decide(ctx, requestID.String(), "approved", "", uuid.NullUUID{})
	if err == nil {
		t.Fatal("a return claiming more than remains was approved")
	}
	if !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("refused with %v, want ErrRefused", err)
	}
	// Figures only: matching the words would bind this to a message that is translated.
	for _, want := range []string{"200000", "150000", "50000"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "refunds_within_capture") {
		t.Errorf("the constraint reached the staff member: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read the return: %v", err)
	}
	if status != "requested" {
		t.Errorf("a refused approval left the return %s", status)
	}
}

func TestTheReturnQueueShowsWhatIsComingBack(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, _ := returnedOrder(t, 2)

	view, err := s.Returns(ctx)
	if err != nil {
		t.Fatalf("read the queue: %v", err)
	}

	var found *pages.AdminReturn
	for i := range view.Rows {
		if view.Rows[i].ID == requestID.String() {
			found = &view.Rows[i]
		}
	}
	if found == nil {
		t.Fatalf("the return %s is not in the queue", requestID)
	}
	if len(found.Lines) == 0 {
		t.Fatal("the queue row carries no lines — the decision is still blind")
	}
	line := found.Lines[0]
	if line.SKU == "" || line.Name == "" || line.Quantity != 2 {
		t.Errorf("the line is %+v, want a sku, a name and 2 units", line)
	}
}

func TestPublishingAVersionCarriesItsZoneSurcharges(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	var methodID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_methods WHERE code = 'home_delivery'`).Scan(&methodID); err != nil {
		t.Fatalf("find the method: %v", err)
	}
	// Its own surcharge and not the seed's: another test clears the current version's.
	var versionID, zoneID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT v.id FROM shipping_method_versions v
		WHERE v.method_id = $1 AND v.effective_at <= now()
		ORDER BY v.effective_at DESC, v.id DESC LIMIT 1`, methodID).Scan(&versionID); err != nil {
		t.Fatalf("find the current version: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_zones WHERE code = 'offshore'`).Scan(&zoneID); err != nil {
		t.Fatalf("find the zone: %v", err)
	}
	if err := s.SetZoneSurcharge(ctx, versionID.String(), zoneID.String(), 200); err != nil {
		t.Fatalf("set the surcharge to carry: %v", err)
	}

	before := surchargeOfCurrentVersion(t, "home_delivery")
	if before == 0 {
		t.Fatal("the surcharge this test just set is not there — it would prove nothing")
	}

	if err := s.PublishShippingVersion(ctx, admin.ShippingVersion{
		MethodID: methodID.String(), Name: "宅配到府", Carrier: "黑貓宅急便",
		NameEn: "Home delivery", CarrierEn: "T-Cat",
		FeeDollars: 100, FreeOverDollars: 2000,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if got := surchargeOfCurrentVersion(t, "home_delivery"); got != before {
		t.Errorf("the new version charges %d for 離島, want %d carried forward", got, before)
	}
	// The base fee too, or this passes on a publish that did nothing at all.
	var fee int64
	if err := pool.QueryRow(ctx, `
		SELECT v.fee_cents FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.code = 'home_delivery' AND v.effective_at <= now()
		ORDER BY v.effective_at DESC, v.id DESC LIMIT 1`).Scan(&fee); err != nil {
		t.Fatalf("read the new fee: %v", err)
	}
	if fee != 10000 {
		t.Errorf("the new version charges %d, want 10000", fee)
	}
}

func TestAZeroSurchargeClearsTheRowRatherThanStoringZero(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	var versionID, zoneID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT v.id FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.code = 'home_delivery' AND v.effective_at <= now()
		ORDER BY v.effective_at DESC, v.id DESC LIMIT 1`).Scan(&versionID); err != nil {
		t.Fatalf("find the version: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_zones WHERE code = 'offshore'`).Scan(&zoneID); err != nil {
		t.Fatalf("find the zone: %v", err)
	}

	if err := s.SetZoneSurcharge(ctx, versionID.String(), zoneID.String(), 250); err != nil {
		t.Fatalf("set: %v", err)
	}
	if n := surchargeRows(t, versionID); n != 1 {
		t.Fatalf("%d surcharge rows after setting one, want 1", n)
	}

	if err := s.SetZoneSurcharge(ctx, versionID.String(), zoneID.String(), 0); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n := surchargeRows(t, versionID); n != 0 {
		t.Errorf("%d surcharge rows after clearing, want 0 — zero was stored as a row", n)
	}
}

func surchargeOfCurrentVersion(t *testing.T, code string) int64 {
	t.Helper()
	var cents int64
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce((SELECT vz.surcharge_cents FROM shipping_version_zones vz
		                 WHERE vz.version_id = v.id LIMIT 1), 0)
		FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.code = $1 AND v.effective_at <= now()
		ORDER BY v.effective_at DESC, v.id DESC LIMIT 1`, code).Scan(&cents); err != nil {
		t.Fatalf("read surcharge: %v", err)
	}
	return cents
}

func surchargeRows(t *testing.T, versionID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM shipping_version_zones WHERE version_id = $1`,
		versionID).Scan(&n); err != nil {
		t.Fatalf("count surcharges: %v", err)
	}
	return n
}

func TestTheTierWindowMatchesTheProgramme(t *testing.T) {
	if got, want := admin.MembershipWindowDays, loyalty.Days(loyalty.MembershipWindow); got != want {
		t.Errorf("the back office reads a %d-day window and the programme says %d", got, want)
	}
}

func TestTwoTiersCannotShareAThreshold(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	if err := s.CreateTier(ctx, "band_a", "甲", "Band A", 90000, 120); err != nil {
		t.Fatalf("create the first band: %v", err)
	}
	err := s.CreateTier(ctx, "band_b", "乙", "", 90000, 130)
	if !errors.Is(err, admin.ErrRefused) {
		t.Errorf("a second band at the same threshold answered %v, want ErrRefused", err)
	}
}

func TestATierCannotEarnLessThanNoTier(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	if err := s.CreateTier(ctx, "worse", "倒扣", "", 80000, 90); !errors.Is(err, admin.ErrInvalid) {
		t.Errorf("a band below the base rate answered %v, want ErrInvalid", err)
	}
	if err := s.CreateTier(ctx, "worse", "基本", "Base", 80000, 100); err != nil {
		t.Errorf("a band at the base rate was refused: %v", err)
	}
}

func TestOneUploadCanBeAttachedToTwoProducts(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	const digest = "aa11bb22cc33dd44ee55ff6677889900aa11bb22cc33dd44ee55ff6677889900"
	if _, err := pool.Exec(ctx, `
		-- byte_size must equal length(bytes): media_objects_size_matches refuses
		-- a row that claims a size its own bytes do not have.
		INSERT INTO media_objects (digest, content_type, byte_size, width, height, bytes)
		VALUES ($1, 'image/png', 1, 800, 600, '\x00'::bytea)
		ON CONFLICT (digest) DO NOTHING`, digest); err != nil {
		t.Fatalf("store the image: %v", err)
	}

	first, second := twoProducts(t)
	if err := s.AttachImage(ctx, first, digest, "第一個商品", "First product", 800, 600); err != nil {
		t.Fatalf("attach to the first: %v", err)
	}
	if err := s.AttachImage(ctx, second, digest, "第二個商品", "", 800, 600); err != nil {
		t.Fatalf("attach the SAME image to the second: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM product_images WHERE storage_key = $1`, digest).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("the image is attached to %d products, want 2", n)
	}
	if err := s.AttachImage(ctx, first, digest, "再一次", "", 800, 600); err == nil {
		t.Error("the same image was attached to one product twice")
	}
}

func twoProducts(t *testing.T) (first, second string) {
	t.Helper()
	slugs := make([]string, 0, 2)
	for i := range 2 {
		slug := "picker-" + uuid.NewString()[:8] + "-" + strconv.Itoa(i)
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO products (brand_id, category_id, slug, name)
			SELECT b.id, c.id, $1, $1 FROM brands b, categories c
			ORDER BY b.id, c.id LIMIT 1`, slug); err != nil {
			t.Fatalf("create product: %v", err)
		}
		slugs = append(slugs, slug)
	}
	return slugs[0], slugs[1]
}

func TestADeliveryAddressCanBeCorrectedUntilItShips(t *testing.T) {
	ctx, staffID := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number := shippableOrder(t, "zh-Hant")

	correction := &admin.Delivery{
		Email: "fixed@example.com", Recipient: "收件人", Phone: "0922333444",
		PostalCode: "106", City: "台北市", District: "大安區", Street: "正確的地址 99 號",
	}
	if err := s.CorrectDelivery(ctx, number, correction); err != nil {
		t.Fatalf("correct a picking order: %v", err)
	}
	if got := streetOf(t, number); got != "正確的地址 99 號" {
		t.Errorf("the address is %q after the correction", got)
	}

	if err := s.Ship(ctx, number, admin.Dispatch{Carrier: "黑貓宅急便", Tracking: "903-2214-9999"},
		uuid.NullUUID{UUID: staffID, Valid: true}); err != nil {
		t.Fatalf("ship: %v", err)
	}
	correction.Street = "出貨後偷改的地址"
	if err := s.CorrectDelivery(ctx, number, correction); !errors.Is(err, admin.ErrTooLateToCorrect) {
		t.Fatalf("a shipped order was corrected: %v", err)
	}
	if got := streetOf(t, number); got != "正確的地址 99 號" {
		t.Errorf("the address changed after dispatch: %q", got)
	}
}

func TestCorrectingADeliveryDoesNotWriteTheAddressIntoTheAuditTrail(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number := shippableOrder(t, "zh-Hant")

	const street = "非常獨特的街道名稱 12345"
	if err := s.CorrectDelivery(ctx, number, &admin.Delivery{
		Email: "audit@example.com", Recipient: "收件人", Phone: "0922333444",
		PostalCode: "106", City: "台北市", District: "大安區", Street: street,
	}); err != nil {
		t.Fatalf("correct: %v", err)
	}

	var after string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(after::text, '') FROM audit_events
		WHERE action = 'order.delivery' ORDER BY occurred_at DESC LIMIT 1`).Scan(&after); err != nil {
		t.Fatalf("read the audit row: %v", err)
	}
	if after == "" {
		t.Fatal("no audit row was written for the correction")
	}
	if strings.Contains(after, street) {
		t.Errorf("the customer's address is in the audit trail: %s", after)
	}
	if !strings.Contains(after, number) {
		t.Errorf("the audit row does not say which order changed: %s", after)
	}
}

func TestCorrectingAPickupOrderCannotTurnItIntoAnAddressOne(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number := pickupOrderForCorrection(t)

	if err := s.CorrectDelivery(ctx, number, &admin.Delivery{
		Email: "pickup@example.com", Recipient: "收件人", Phone: "0922333444",
		PostalCode: "106", City: "台北市", District: "大安區", Street: "不該存下來的地址",
		PickupBrand: "hi_life", PickupStoreCode: "778899", PickupStoreName: "民生門市",
	}); err != nil {
		t.Fatalf("correct a pickup order: %v", err)
	}

	var street, brand, code *string
	if err := pool.QueryRow(ctx, `
		SELECT pd.street, pd.pickup_brand, pd.pickup_store_code
		FROM order_private_data pd JOIN orders o ON o.id = pd.order_id
		WHERE o.order_number = $1`, number).Scan(&street, &brand, &code); err != nil {
		t.Fatalf("read the delivery: %v", err)
	}
	if street != nil {
		t.Errorf("a pickup order kept a street address: %q", *street)
	}
	if brand == nil || *brand != "hi_life" || code == nil || *code != "778899" {
		t.Errorf("the pickup point was not updated: %v/%v", brand, code)
	}
}

func streetOf(t *testing.T, number string) string {
	t.Helper()
	var street string
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce(pd.street, '') FROM order_private_data pd
		JOIN orders o ON o.id = pd.order_id WHERE o.order_number = $1`,
		number).Scan(&street); err != nil {
		t.Fatalf("read street: %v", err)
	}
	return street
}

func pickupOrderForCorrection(t *testing.T) string {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.destination_kind = 'pickup_point'
		ORDER BY v.effective_at DESC LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create pickup order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'FIX-SKU', '測試商品', 100000, 1)`, orderID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                pickup_brand, pickup_store_code, pickup_store_name)
		VALUES ($1, 'pickup@example.com', '收件', '0912345678',
		        'family_mart', '012345', '台北車站門市')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

func TestHidingAReviewIsReversibleAndAudited(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	reviewID := someReview(t)

	if err := s.SetReviewHidden(ctx, reviewID, true); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if !reviewHidden(t, reviewID) {
		t.Fatal("the review is not hidden")
	}
	if err := s.SetReviewHidden(ctx, reviewID, true); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("hiding an already-hidden review answered %v", err)
	}

	if err := s.SetReviewHidden(ctx, reviewID, false); err != nil {
		t.Fatalf("show: %v", err)
	}
	if reviewHidden(t, reviewID) {
		t.Error("the review is still hidden")
	}

	var actions []string
	rows, err := pool.Query(ctx, `
		SELECT action FROM audit_events WHERE action LIKE 'review.%'
		  AND entity_id = $1 ORDER BY occurred_at`, reviewID)
	if err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		if scanErr := rows.Scan(&a); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		actions = append(actions, a)
	}
	if diff := cmp.Diff([]string{"review.hide", "review.show"}, actions); diff != "" {
		t.Errorf("the trail (-want +got):\n%s", diff)
	}
}

func TestTheReviewQueueShowsHiddenOnes(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	reviewID := someReview(t)

	if err := s.SetReviewHidden(ctx, reviewID, true); err != nil {
		t.Fatalf("hide: %v", err)
	}
	view, err := s.Reviews(ctx)
	if err != nil {
		t.Fatalf("read the queue: %v", err)
	}

	var found bool
	for _, r := range view.Rows {
		if r.ID == reviewID {
			found = true
			if !r.Hidden {
				t.Error("the queue does not mark it hidden")
			}
			if r.Action() != "/admin/reviews/show" {
				t.Errorf("the button posts to %s, want the show path", r.Action())
			}
		}
	}
	if !found {
		t.Error("a hidden review is missing from the queue — it cannot be put back")
	}
	if view.HiddenCount() == 0 {
		t.Error("the queue counts no hidden reviews")
	}
}

func someReview(t *testing.T) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO product_reviews (product_id, rating, body)
		SELECT id, 1, '測試評價 ' || gen_random_uuid() FROM products ORDER BY slug LIMIT 1
		RETURNING id::text`).Scan(&id); err != nil {
		t.Fatalf("create review: %v", err)
	}
	return id
}

func reviewHidden(t *testing.T, id string) bool {
	t.Helper()
	var hidden bool
	if err := pool.QueryRow(t.Context(),
		`SELECT hidden_at IS NOT NULL FROM product_reviews WHERE id = $1`, id).Scan(&hidden); err != nil {
		t.Fatalf("read hidden_at: %v", err)
	}
	return hidden
}

func TestTheInboxPutsTheLongestWaitFirst(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	old := messageAgedDays(t, "四天前", 4)
	fresh := messageAgedDays(t, "今天", 0)
	done := messageAgedDays(t, "已處理過", 6)
	if err := s.SetMessageHandled(ctx, done, true); err != nil {
		t.Fatalf("handle the old one: %v", err)
	}

	view, err := s.Messages(ctx)
	if err != nil {
		t.Fatalf("read the inbox: %v", err)
	}

	var order []string
	for i := range view.Rows {
		switch view.Rows[i].ID {
		case old, fresh, done:
			order = append(order, view.Rows[i].ID)
		}
	}
	if diff := cmp.Diff([]string{old, fresh, done}, order); diff != "" {
		t.Errorf("queue order (-want +got):\n%s", diff)
	}

	for i := range view.Rows {
		r := &view.Rows[i]
		switch r.ID {
		case old:
			if !r.Overdue() {
				t.Errorf("a four-day wait is not overdue: %+v", r)
			}
			if r.WaitingDays < 4 {
				t.Errorf("the four-day-old message reports %d days", r.WaitingDays)
			}
		case fresh:
			if r.Overdue() {
				t.Error("a message from today reads as overdue")
			}
		case done:
			if !r.Handled || r.Waiting(ctx) != "已處理" {
				t.Errorf("a handled message reads as %q", r.Waiting(ctx))
			}
		}
	}
}

func TestHandlingAMessageIsReversibleAndAudited(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	id := messageAgedDays(t, "來回一次", 1)

	if err := s.SetMessageHandled(ctx, id, true); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !messageHandled(t, id) {
		t.Fatal("the message is not handled")
	}
	if err := s.SetMessageHandled(ctx, id, true); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("handling an already-handled message answered %v", err)
	}

	if err := s.SetMessageHandled(ctx, id, false); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if messageHandled(t, id) {
		t.Error("the message is still handled")
	}

	var actions []string
	rows, err := pool.Query(ctx, `
		SELECT action FROM audit_events
		WHERE action LIKE 'message.%' AND entity_id = $1 ORDER BY occurred_at`, id)
	if err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		if scanErr := rows.Scan(&a); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		actions = append(actions, a)
	}
	if diff := cmp.Diff([]string{"message.handle", "message.reopen"}, actions); diff != "" {
		t.Errorf("the trail (-want +got):\n%s", diff)
	}
}

func TestTheInboxDoesNotCopyTheMessageIntoTheAuditTrail(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	const body = "非常獨特的訊息內容 987654"
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO contact_messages (name, email, subject, message)
		VALUES ('王小明', 'privacy@example.com', '訂單問題', $1) RETURNING id::text`,
		body).Scan(&id); err != nil {
		t.Fatalf("create message: %v", err)
	}
	if err := s.SetMessageHandled(ctx, id, true); err != nil {
		t.Fatalf("handle: %v", err)
	}

	var after string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(after::text, '') FROM audit_events
		WHERE action = 'message.handle' AND entity_id = $1`, id).Scan(&after); err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	if strings.Contains(after, body) {
		t.Errorf("the customer's message is in the audit trail: %s", after)
	}
	if !strings.Contains(after, id) {
		t.Errorf("the audit row does not say which message: %s", after)
	}
}

func messageAgedDays(t *testing.T, subject string, days int) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO contact_messages (name, email, subject, message, created_at)
		VALUES ('王小明', 'inbox@example.com', $1, '測試訊息',
		        now() - make_interval(days => $2, hours => 1))
		RETURNING id::text`, subject, days).Scan(&id); err != nil {
		t.Fatalf("create message: %v", err)
	}
	return id
}

func messageHandled(t *testing.T, id string) bool {
	t.Helper()
	var handled bool
	if err := pool.QueryRow(t.Context(),
		`SELECT handled_at IS NOT NULL FROM contact_messages WHERE id = $1`,
		id).Scan(&handled); err != nil {
		t.Fatalf("read handled_at: %v", err)
	}
	return handled
}

func TestAReturnPaysBackBothSources(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, orderNumber, accountID := creditFundedReturn(t, 2, 60000)

	if err := s.Decide(ctx, requestID.String(), "approved", "退貨完成", uuid.NullUUID{}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	var refunded int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(r.amount_cents), 0) FROM refunds r
		JOIN payments p ON p.id = r.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND r.status = 'succeeded'`,
		orderNumber).Scan(&refunded); err != nil {
		t.Fatalf("read refunds: %v", err)
	}
	if refunded != 140000 {
		t.Errorf("card refunded %d, want 140000 — the whole capture, card first", refunded)
	}

	var compensated int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(e.amount_cents), 0) FROM store_credit_entries e
		JOIN orders o ON o.id = e.order_id
		WHERE o.order_number = $1 AND e.amount_cents > 0 AND e.reverses_id IS NULL`,
		orderNumber).Scan(&compensated); err != nil {
		t.Fatalf("read compensation: %v", err)
	}
	if compensated != 60000 {
		t.Errorf("credit compensated %d, want 60000", compensated)
	}
	if got := creditBalanceOf(t, accountID); got != 60000 {
		t.Errorf("balance = %d, want 60000 — the credit they spent came back", got)
	}
}

func TestAPartialReturnPaysTheCardFirst(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, orderNumber, accountID := creditFundedReturn(t, 1, 60000)

	if err := s.Decide(ctx, requestID.String(), "approved", "退一件", uuid.NullUUID{}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	var refunded int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(r.amount_cents), 0) FROM refunds r
		JOIN payments p ON p.id = r.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND r.status = 'succeeded'`,
		orderNumber).Scan(&refunded); err != nil {
		t.Fatalf("read refunds: %v", err)
	}
	if refunded != 100000 {
		t.Errorf("card refunded %d, want the whole claim of 100000 — card first", refunded)
	}
	if got := creditBalanceOf(t, accountID); got != 0 {
		t.Errorf("balance = %d, want 0 — the card paid the whole claim, so none of "+
			"the credit was needed", got)
	}
}

func TestAWhollyCreditFundedReturnNeedsNoProvider(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, orderNumber, accountID := creditFundedReturn(t, 2, 200000)

	if err := s.Decide(ctx, requestID.String(), "approved", "全額購物金", uuid.NullUUID{}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	var refunds int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM refunds r
		JOIN payments p ON p.id = r.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1`, orderNumber).Scan(&refunds); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	if refunds != 0 {
		t.Errorf("%d refund rows for an order with no card payment, want 0", refunds)
	}
	if got := creditBalanceOf(t, accountID); got != 200000 {
		t.Errorf("balance = %d, want 200000 — every cent came back as credit", got)
	}
}

func TestCompensatingAReturnTwiceGivesCreditOnce(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, _, accountID := creditFundedReturn(t, 2, 200000)

	if err := s.Decide(ctx, requestID.String(), "approved", "第一次", uuid.NullUUID{}); err != nil {
		t.Fatalf("first Decide: %v", err)
	}
	var orderID, userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT o.id, o.user_id FROM orders o
		JOIN return_requests r ON r.order_id = o.id WHERE r.id = $1`,
		requestID).Scan(&orderID, &userID); err != nil {
		t.Fatalf("read the order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		SELECT post_store_credit($1, 200000, '退貨退回購物金', $2,
		       'return-credit:' || $3::text, NULL)`,
		userID, orderID, requestID); err != nil {
		t.Fatalf("second compensation: %v", err)
	}

	if got := creditBalanceOf(t, accountID); got != 200000 {
		t.Errorf("balance = %d after two compensations, want 200000 — the second must "+
			"have found the first by its key", got)
	}
}

func creditBalanceOf(t *testing.T, accountID uuid.UUID) int64 {
	t.Helper()
	var cents int64
	if err := pool.QueryRow(t.Context(),
		`SELECT coalesce(sum(amount_cents), 0) FROM store_credit_entries WHERE account_id = $1`,
		accountID).Scan(&cents); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return cents
}

func creditFundedReturn(t *testing.T, qty int32, creditCents int64) (
	requestID uuid.UUID, orderNumber string, accountID uuid.UUID,
) {
	t.Helper()
	ctx := t.Context()
	const price = 100000
	total := int64(2) * price
	card := total - creditCents

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('ret-credit-'||gen_random_uuid()||'@goen.invalid', 'customer', '退貨額度')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT post_store_credit($1, $2, '測試發放', NULL, $3, NULL)`,
		userID, creditCents, "grant:"+userID.String()); err != nil {
		t.Fatalf("grant credit: %v", err)
	}

	var orderID, lineID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'REF-SKU', '測試商品', $2, 2) RETURNING id`,
		orderID, price).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'f@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	// The spend, while the order is still an open unpaid checkout: the only moment store_credit_guard allows it.
	if _, err := tx.Exec(ctx, `SELECT post_store_credit($1, $2, '訂單折抵', $3, $4, NULL)`,
		userID, -creditCents, orderID, "spend:"+orderID.String()); err != nil {
		t.Fatalf("spend credit: %v", err)
	}
	if card > 0 {
		if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`,
			orderID, "cs_cred_"+orderNumber, card); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`,
			"cs_cred_"+orderNumber, card); err != nil {
			t.Fatalf("capture: %v", err)
		}
	}

	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, '黑貓', 'TC-'||$2) RETURNING id`,
		orderID, orderNumber).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 2)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
	for _, status := range []string{"picking", "shipped"} {
		if _, err := tx.Exec(ctx,
			`UPDATE orders SET fulfillment_status = $2 WHERE id = $1`,
			orderID, status); err != nil {
			t.Fatalf("move the order to %s: %v", status, err)
		}
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
	if err := tx.QueryRow(ctx,
		`SELECT id FROM store_credit_accounts WHERE user_id = $1`, userID).
		Scan(&accountID); err != nil {
		t.Fatalf("read credit account: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return requestID, orderNumber, accountID
}

func TestTheBackOfficeCanFindAnOrder(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number, recipient, addr := searchableOrder(t)

	for _, term := range []string{
		number,
		strings.ToLower(number),
		// Sliced by RUNE: recipient[:2] cuts a character in half and PostgreSQL refuses invalid UTF-8.
		string([]rune(recipient)[:2]),
		string([]rune(addr)[:8]),
		strings.ToUpper(string([]rune(addr)[:8])),
	} {
		view, err := s.Orders(ctx, "", term)
		if err != nil {
			t.Fatalf("Orders(%q): %v", term, err)
		}
		if !hasOrder(view, number) {
			t.Errorf("searching %q did not find %s", term, number)
		}
		if view.Term != term {
			t.Errorf("the box lost the term: %q", view.Term)
		}
	}
}

func TestASearchIgnoresTheStatusFilter(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number, _, _ := searchableOrder(t)

	view, err := s.Orders(ctx, "shipped", number)
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	if !hasOrder(view, number) {
		t.Error("a search was filtered away by the status tab")
	}
}

func TestATooShortSearchIsNotASearch(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	view, err := s.Orders(ctx, "", "王")
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	if view.Searching() {
		t.Error("a one-character term reads as a search")
	}
}

func TestAnErasedOrderIsNotFoundByItsOldAddress(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number, recipient, addr := searchableOrder(t)

	if _, err := pool.Exec(ctx, `
		UPDATE order_private_data pd SET
			email = NULL, recipient_name = NULL, phone = NULL, postal_code = NULL,
			city = NULL, district = NULL, street = NULL, erased_at = now()
		FROM orders o WHERE pd.order_id = o.id AND o.order_number = $1`,
		number); err != nil {
		t.Fatalf("erase: %v", err)
	}

	for _, term := range []string{string([]rune(recipient)[:2]), string([]rune(addr)[:8])} {
		view, err := s.Orders(ctx, "", term)
		if err != nil {
			t.Fatalf("Orders(%q): %v", term, err)
		}
		if hasOrder(view, number) {
			t.Errorf("an erased order was found by %q, which it no longer holds", term)
		}
	}
	view, err := s.Orders(ctx, "", number)
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	if !hasOrder(view, number) {
		t.Error("an erased order cannot be found by its own number; it is still an order")
	}
}

func hasOrder(v pages.AdminOrdersView, number string) bool {
	for i := range v.Orders {
		if v.Orders[i].Number == number {
			return true
		}
	}
	return false
}

func searchableOrder(t *testing.T) (number, recipient, addr string) {
	t.Helper()
	ctx := t.Context()
	recipient = "尋" + uuid.NewString()[:6]
	addr = "search-" + strings.ReplaceAll(uuid.NewString(), "-", "") + "@goen.invalid"

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
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'SEARCH-SKU', '測試商品', 100000, 1)`, orderID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, $2, $3, '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID, addr, recipient); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, recipient, addr
}

func TestTheBackOfficeCanSeeOneCustomerWhole(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	userID := creditedAccount(t, 50000)

	view, err := s.Customer(ctx, userID.String(), uuid.NullUUID{UUID: staff, Valid: true})
	if err != nil {
		t.Fatalf("Customer: %v", err)
	}
	if view.CreditCents != 50000 {
		t.Errorf("credit balance is %d, want 50000", view.CreditCents)
	}
	if view.Verified {
		t.Error("a customer nobody has verified reads as verified")
	}
	if view.Since == "" {
		t.Error("the page does not say when they registered")
	}
}

func TestLookingAtACustomerIsRecorded(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	userID := creditedAccount(t, 1000)

	if _, err := s.Customer(ctx, userID.String(), uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("Customer: %v", err)
	}

	var rows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE action = 'customer.view' AND entity_id = $1 AND actor_user_id = $2`,
		userID, staff).Scan(&rows); err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d audit rows for one lookup, want 1", rows)
	}

	var after string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce("after"::text, '') FROM audit_events
		WHERE action = 'customer.view' AND entity_id = $1`, userID).Scan(&after); err != nil {
		t.Fatalf("read the row: %v", err)
	}
	var addr string
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).
		Scan(&addr); err != nil {
		t.Fatalf("read the address: %v", err)
	}
	if strings.Contains(after, addr) {
		t.Errorf("the trail copied the customer's address into itself: %s", after)
	}
}

func TestACustomerSearchNeedsATerm(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	for _, term := range []string{"", " ", "王"} {
		view, err := s.Customers(ctx, term)
		if err != nil {
			t.Fatalf("Customers(%q): %v", term, err)
		}
		if view.Searching() {
			t.Errorf("%q reads as a search", term)
		}
		if len(view.Rows) != 0 {
			t.Errorf("%q listed %d customers without searching", term, len(view.Rows))
		}
	}
}

func TestACustomerIsFoundByTheStartOfTheirAddress(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	userID := creditedAccount(t, 0)

	var addr string
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).
		Scan(&addr); err != nil {
		t.Fatalf("read the address: %v", err)
	}

	for _, term := range []string{
		string([]rune(addr)[:10]),
		strings.ToUpper(string([]rune(addr)[:10])),
	} {
		view, err := s.Customers(ctx, term)
		if err != nil {
			t.Fatalf("Customers(%q): %v", term, err)
		}
		found := false
		for i := range view.Rows {
			if view.Rows[i].ID == userID.String() {
				found = true
			}
		}
		if !found {
			t.Errorf("searching %q did not find the customer", term)
		}
	}
}

func creditedAccount(t *testing.T, cents int64) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('cust-'||gen_random_uuid()||'@goen.invalid', 'customer', '顧客測試')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	// A zero grant would meet store_credit_entries_amount_non_zero, so an empty account is made directly.
	if cents > 0 {
		if _, err := pool.Exec(ctx,
			`SELECT post_store_credit($1, $2, '測試發放', NULL, $3, NULL)`,
			userID, cents, "grant:"+userID.String()); err != nil {
			t.Fatalf("grant credit: %v", err)
		}
	} else {
		if _, err := pool.Exec(ctx,
			`INSERT INTO store_credit_accounts (user_id) VALUES ($1)`, userID); err != nil {
			t.Fatalf("create credit account: %v", err)
		}
	}
	return userID
}

func TestACustomersSpendCountsOnlyCommittedOrders(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	userID := creditedAccount(t, 0)
	actor := uuid.NullUUID{UUID: staff, Valid: true}

	orderForCustomer(t, userID, 120000, true)
	cancelled := orderForCustomer(t, userID, 990000, false)
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE id = $1`,
		cancelled); err != nil {
		t.Fatalf("cancel the order: %v", err)
	}

	view, err := s.Customer(ctx, userID.String(), actor)
	if err != nil {
		t.Fatalf("Customer: %v", err)
	}
	if view.SpentCents != 120000 {
		t.Errorf("spend is %d, want 120000 — the cancelled order is being counted",
			view.SpentCents)
	}
	if view.Orders != 2 {
		t.Errorf("order count is %d, want 2", view.Orders)
	}
}

func TestAPromotedCustomerIsStillFindable(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	userID := creditedAccount(t, 0)
	orderForCustomer(t, userID, 50000, true)

	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1`,
		userID); err != nil {
		t.Fatalf("promote: %v", err)
	}

	var addr string
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).
		Scan(&addr); err != nil {
		t.Fatalf("read the address: %v", err)
	}
	view, err := s.Customers(ctx, string([]rune(addr)[:10]))
	if err != nil {
		t.Fatalf("Customers: %v", err)
	}
	found := false
	for i := range view.Rows {
		if view.Rows[i].ID == userID.String() {
			found = true
		}
	}
	if !found {
		t.Error("a promoted customer cannot be found by the search")
	}

	one, err := s.Customer(ctx, userID.String(), uuid.NullUUID{UUID: staff, Valid: true})
	if err != nil {
		t.Fatalf("Customer of a promoted customer: %v", err)
	}
	if one.SpentCents != 50000 {
		t.Errorf("spend is %d, want 50000", one.SpentCents)
	}
}

func orderForCustomer(t *testing.T, userID uuid.UUID, cents int64, paid bool) uuid.UUID {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id`, userID).Scan(&orderID); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'cust@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("delivery details: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'CUST-SKU', '顧客頁測試', $2, 1)`, orderID, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if paid {
		ref := "cust_" + orderID.String()
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

func TestTheBackOfficeSeesWhoCancelled(t *testing.T) {
	ctx, _ := staffContext(t)
	basket := cart.NewStore(pool)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number := placeUnpaidOrder(t)

	if _, err := basket.Cancel(ctx, number); err != nil {
		t.Fatalf("the customer cancels: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	var found bool
	for _, e := range view.Timeline {
		if e.Kind != "cancelled" {
			continue
		}
		found = true
		if e.By(ctx) != "顧客" {
			t.Errorf("the back office says %q cancelled it, want 顧客", e.By(ctx))
		}
		if e.Note != "" {
			t.Errorf("the cancellation still stores words: %q", e.Note)
		}
	}
	if !found {
		t.Fatal("no cancellation in the order's history")
	}
}

func TestADeliveredOrderMarksItsParcelsDelivered(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number := shippableOrder(t, "zh-Hant")

	if err := s.Ship(ctx, number, admin.Dispatch{Carrier: "黑貓宅急便", Tracking: "DELIVERED-" + number}, actor); err != nil {
		t.Fatalf("Ship: %v", err)
	}
	before, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order after shipping: %v", err)
	}
	if len(before.Shipments) != 1 {
		t.Fatalf("%d parcels after shipping, want 1", len(before.Shipments))
	}
	if before.Shipments[0].Delivered() {
		t.Error("a parcel that has just left reads as delivered")
	}

	if _, advErr := s.Advance(ctx, number, "delivered", actor); advErr != nil {
		t.Fatalf("Advance to delivered: %v", advErr)
	}
	after, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order after delivery: %v", err)
	}
	if !after.Shipments[0].Delivered() {
		t.Error("the order is delivered and its parcel still says otherwise")
	}

	first := deliveredAt(t, number)
	if first.IsZero() {
		t.Fatal("the parcel has no delivery timestamp at all")
	}
	if _, err := s.Advance(ctx, number, "completed", actor); err != nil {
		t.Fatalf("Advance to completed: %v", err)
	}
	if again := deliveredAt(t, number); !again.Equal(first) {
		t.Errorf("the delivery timestamp moved from %s to %s",
			first.Format(time.RFC3339Nano), again.Format(time.RFC3339Nano))
	}
}

func TestAnOrderCompletedWithoutADeliveryStepStillStampsItsParcels(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number := shippableOrder(t, "zh-Hant")

	if err := s.Ship(ctx, number, admin.Dispatch{Carrier: "7-ELEVEN 交貨便", Tracking: "COLLECTED-" + number}, actor); err != nil {
		t.Fatalf("Ship: %v", err)
	}
	if !deliveredAt(t, number).IsZero() {
		t.Fatal("a parcel that has just left is already stamped delivered; the fixture is wrong")
	}

	if _, err := s.Advance(ctx, number, "completed", actor); err != nil {
		t.Fatalf("Advance to completed: %v", err)
	}

	if deliveredAt(t, number).IsZero() {
		t.Error("an order completed without a delivery step left its parcel unstamped — " +
			"the customer's page says it never arrived and /admin/returns reads the " +
			"rescission window as never having started")
	}
}

func TestShippingIsRefusedWhenTheOrderHoldsNoStock(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number, orderID := pickingOrderHoldingStock(t)

	if _, err := pool.Exec(ctx, `
		UPDATE inventory_reservations SET state = 'released', settled_at = now()
		WHERE order_id = $1 AND state = 'held'`, orderID); err != nil {
		t.Fatalf("strand the order: %v", err)
	}

	err := s.Ship(ctx, number, admin.Dispatch{Carrier: "黑貓宅急便", Tracking: "NOHOLD-" + number}, uuid.NullUUID{})
	if !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("shipping an order holding no stock = %v, want ErrRefused", err)
	}

	var shipments int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_shipments WHERE order_id = $1`, orderID).Scan(&shipments); err != nil {
		t.Fatalf("count shipments: %v", err)
	}
	if shipments != 0 {
		t.Errorf("%d parcels recorded against a refused dispatch, want 0", shipments)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "picking" {
		t.Errorf("order is %q after a refused dispatch, want picking", status)
	}
}

func deliveredAt(t *testing.T, number string) time.Time {
	t.Helper()
	var at *time.Time
	if err := pool.QueryRow(t.Context(), `
		SELECT sh.delivered_at FROM order_shipments sh
		JOIN orders o ON o.id = sh.order_id
		WHERE o.order_number = $1`, number).Scan(&at); err != nil {
		t.Fatalf("read the parcel: %v", err)
	}
	if at == nil {
		return time.Time{}
	}
	return *at
}

func TestTheShopCanGiveAProductASpecTable(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	slug := draftProduct(t, ctx, s)

	if errs, err := s.AddSpec(ctx, slug, admin.SpecDraft{
		Label: "螢幕", Value: "6.3 吋 OLED", LabelEn: "Screen",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("AddSpec: %v %v", err, errs)
	}
	if errs, err := s.AddSpec(ctx, slug, admin.SpecDraft{
		Label: "重量", Value: "187 公克", LabelEn: "Weight", ValueEn: "187 g",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("AddSpec: %v %v", err, errs)
	}

	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if !view.HasSpecs() {
		t.Fatal("the product states no specs after two were added")
	}
	want := []string{"螢幕", "重量"}
	got := make([]string, 0, len(view.Specs))
	for _, sp := range view.Specs {
		got = append(got, sp.Label)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("spec labels (-want +got):\n%s", diff)
	}

	if rmErr := s.RemoveSpec(ctx, slug, view.Specs[0].ID); rmErr != nil {
		t.Fatalf("RemoveSpec: %v", rmErr)
	}
	after, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product after removal: %v", err)
	}
	if len(after.Specs) != 1 || after.Specs[0].Label != "重量" {
		t.Errorf("after removing 螢幕 the table is %+v", after.Specs)
	}

	other := draftProduct(t, ctx, s)
	if rmErr := s.RemoveSpec(ctx, other, after.Specs[0].ID); !errors.Is(rmErr, admin.ErrNotFound) {
		t.Errorf("removing another product's spec returned %v, want ErrNotFound", rmErr)
	}
}

func TestASpecIsRefusedRatherThanTruncated(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	slug := draftProduct(t, ctx, s)

	tests := []struct {
		name  string
		label string
		value string
		field string
	}{
		{name: "no label", label: "  ", value: "6.3 吋", field: "spec_label"},
		{name: "no value", label: "螢幕", value: "\t", field: "spec_value"},
		{
			name:  "a label that is a sentence",
			label: strings.Repeat("螢", admin.SpecLabelRunes+1),
			value: "6.3 吋",
			field: "spec_label",
		},
		{
			name:  "a value past the bound",
			label: "螢幕",
			value: strings.Repeat("吋", admin.SpecValueRunes+1),
			field: "spec_value",
		},
	}
	if errs, _ := s.AddSpec(ctx, slug, admin.SpecDraft{
		Label: "螢幕", Value: "6.3 吋",
		LabelEn: strings.Repeat("S", admin.SpecLabelRunes+1),
	}); errs["spec_label_en"] == "" {
		t.Errorf("an over-long English label was accepted: %v", errs)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs, err := s.AddSpec(ctx, slug, admin.SpecDraft{
				Label: tt.label, Value: tt.value,
			})
			if err != nil {
				t.Fatalf("AddSpec: %v", err)
			}
			if _, ok := errs[tt.field]; !ok {
				t.Errorf("AddSpec(%q, %q) refused %v, want a %s error",
					tt.label, tt.value, errs, tt.field)
			}
		})
	}

	if errs, err := s.AddSpec(ctx, slug, admin.SpecDraft{
		Label: strings.Repeat("螢", admin.SpecLabelRunes), Value: "剛好",
	}); err != nil || len(errs) > 0 {
		t.Errorf("a label exactly at the bound was refused: %v %v", err, errs)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO product_specs (product_id, label, value, position)
		SELECT id, repeat('螢', 41), '繞過表單', 99 FROM products WHERE slug = $1`, slug)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok ||
		pgErr.ConstraintName != "product_specs_label_bounded" {
		t.Errorf("a 41-character label written directly returned %v, want "+
			"product_specs_label_bounded", err)
	}
}

func draftProduct(t *testing.T, ctx context.Context, s *admin.Store) string {
	t.Helper()

	var brandID, catID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM brands LIMIT 1`).Scan(&brandID); err != nil {
		t.Fatalf("read a brand: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM categories LIMIT 1`).Scan(&catID); err != nil {
		t.Fatalf("read a category: %v", err)
	}
	slug := "spec-" + uuid.New().String()[:8]
	form := &admin.ProductForm{
		Slug: slug, Name: "規格表測試 " + slug, Summary: "測試用",
		Description: "測試用商品", BrandID: brandID, CategoryID: catID,
	}
	created, errs, err := s.CreateProduct(ctx, form)
	if err != nil || len(errs) > 0 {
		t.Fatalf("CreateProduct: %v %v", err, errs)
	}
	return created
}

func TestACategoryCarriesItsEnglishName(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	slug := "tax-en-" + uuid.NewString()[:8]

	if errs, err := s.CreateCategory(ctx, &admin.TaxonomyForm{
		Slug: slug, Name: "測試分類", NameEn: "Test category",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("create: err=%v errs=%v", err, errs)
	}

	tests := []struct {
		name   string
		locale string
		want   string
	}{
		{name: "a Chinese reader gets the Chinese name", locale: "zh-Hant", want: "測試分類"},
		{name: "an English reader gets the English one", locale: "en", want: "Test category"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			if err := pool.QueryRow(ctx, `
				SELECT localized_name(name, name_en, $2) FROM categories WHERE slug = $1`,
				slug, tt.locale).Scan(&got); err != nil {
				t.Fatalf("read the name: %v", err)
			}
			if got != tt.want {
				t.Errorf("localized_name in %s = %q, want %q", tt.locale, got, tt.want)
			}
		})
	}

	plain := "tax-plain-" + uuid.NewString()[:8]
	if errs, err := s.CreateCategory(ctx, &admin.TaxonomyForm{
		Slug: plain, Name: "沒翻的分類",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("create: err=%v errs=%v", err, errs)
	}
	var fallback string
	if err := pool.QueryRow(ctx, `
		SELECT localized_name(name, name_en, 'en') FROM categories WHERE slug = $1`,
		plain).Scan(&fallback); err != nil {
		t.Fatalf("read the name: %v", err)
	}
	if fallback != "沒翻的分類" {
		t.Errorf("an untranslated category renders %q to an English reader, want the "+
			"Chinese name", fallback)
	}

	if err := s.Rename(ctx, "category", slug, "測試分類", "", ""); err != nil {
		t.Fatalf("rename: %v", err)
	}
	var cleared *string
	if err := pool.QueryRow(ctx,
		`SELECT name_en FROM categories WHERE slug = $1`, slug).Scan(&cleared); err != nil {
		t.Fatalf("read name_en: %v", err)
	}
	if cleared != nil {
		t.Errorf("clearing the English name left %q", *cleared)
	}
}

func TestTheShopCanGiveAProductAVariantPicker(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	slug := draftProduct(t, ctx, s)

	if errs, err := s.AddOption(ctx, slug, admin.OptionDraft{
		Name: "顏色", NameEn: "Colour",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", err, errs)
	}
	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if !view.HasOptions() || view.Options[0].Name != "顏色" {
		t.Fatalf("the product's options are %+v", view.Options)
	}
	if view.Options[0].HasValues() {
		t.Error("a new option already has values")
	}

	optionID := view.Options[0].ID
	for _, v := range []struct{ value, label string }{
		{"星霧藍", "Mist Blue"},
		{"曜石黑", ""},
	} {
		if errs, addErr := s.AddOptionValue(ctx, slug, admin.OptionDraft{
			OptionID: optionID, Name: v.value, NameEn: v.label,
		}); addErr != nil || len(errs) > 0 {
			t.Fatalf("AddOptionValue(%s): %v %v", v.value, addErr, errs)
		}
	}

	view, err = s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if len(view.Options[0].Values) != 2 {
		t.Fatalf("the axis has %d values, want 2", len(view.Options[0].Values))
	}
	if view.Options[0].Values[0].Value != "星霧藍" ||
		view.Options[0].Values[0].Label != "Mist Blue" {
		t.Errorf("the first value is %+v", view.Options[0].Values[0])
	}
	if view.Options[0].Values[1].Label != "" {
		t.Errorf("an untranslated value reports label %q",
			view.Options[0].Values[1].Label)
	}

	if errs, addErr := s.AddVariant(ctx, slug, &admin.VariantForm{
		SKU: "PICKER-NONE", PriceCents: 100000,
	}); addErr != nil {
		t.Fatalf("AddVariant: %v", addErr)
	} else if errs["options"] == "" {
		t.Errorf("a variant naming no option value was accepted: %v", errs)
	}

	if errs, addErr := s.AddVariant(ctx, slug, &admin.VariantForm{
		SKU: "PICKER-BLUE", PriceCents: 100000,
		OptionValues: []string{view.Options[0].Values[0].ID},
	}); addErr != nil || len(errs) > 0 {
		t.Fatalf("AddVariant: %v %v", addErr, errs)
	}

	after, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	var found bool
	for _, sv := range after.Variants {
		if sv.SKU != "PICKER-BLUE" {
			continue
		}
		found = true
		if sv.OptionText() != "星霧藍" {
			t.Errorf("the variant list shows %q for its options", sv.OptionText())
		}
	}
	if !found {
		t.Error("the new variant is not in the product's variant list")
	}
}

func TestAVariantCannotBorrowAnotherProductsOptionValue(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	first := draftProduct(t, ctx, s)
	if errs, err := s.AddOption(ctx, first, admin.OptionDraft{Name: "顏色"}); err != nil ||
		len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", err, errs)
	}
	firstView, err := s.Product(ctx, first)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if errs, addErr := s.AddOptionValue(ctx, first, admin.OptionDraft{
		OptionID: firstView.Options[0].ID, Name: "星霧藍",
	}); addErr != nil || len(errs) > 0 {
		t.Fatalf("AddOptionValue: %v %v", addErr, errs)
	}
	firstView, err = s.Product(ctx, first)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	borrowed := firstView.Options[0].Values[0].ID

	second := draftProduct(t, ctx, s)
	if errs, addErr := s.AddOption(ctx, second, admin.OptionDraft{Name: "顏色"}); addErr != nil ||
		len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", addErr, errs)
	}

	errs, addErr := s.AddVariant(ctx, second, &admin.VariantForm{
		SKU: "BORROW-1", PriceCents: 100000, OptionValues: []string{borrowed},
	})
	if addErr != nil {
		t.Fatalf("AddVariant: %v", addErr)
	}
	if errs["options"] == "" {
		t.Errorf("a variant borrowing another product's value was accepted: %v", errs)
	}
	var variants int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM product_variants WHERE sku = 'BORROW-1'`).Scan(&variants); err != nil {
		t.Fatalf("count variants: %v", err)
	}
	if variants != 0 {
		t.Errorf("%d variants named BORROW-1 survived the refusal", variants)
	}
}

func TestTheOptionValueIsAddedToTheRightProduct(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	first := draftProduct(t, ctx, s)
	if errs, err := s.AddOption(ctx, first, admin.OptionDraft{Name: "顏色"}); err != nil ||
		len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", err, errs)
	}
	view, err := s.Product(ctx, first)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}

	second := draftProduct(t, ctx, s)
	errs, err := s.AddOptionValue(ctx, second, admin.OptionDraft{
		OptionID: view.Options[0].ID, Name: "星霧藍",
	})
	if err != nil {
		t.Fatalf("AddOptionValue: %v", err)
	}
	if errs["value"] == "" {
		t.Errorf("a value was added to another product's axis: %v", errs)
	}
}

func TestARestockNoticeNamesTheProductInTheReadersLanguage(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	var vid uuid.UUID
	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT pv.id, pv.sku FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active AND p.name_en IS NOT NULL
		  AND p.name_en <> p.name
		ORDER BY pv.id LIMIT 1`).Scan(&vid, &sku); err != nil {
		t.Fatalf("find a translated variant: %v", err)
	}
	emptyTheShelf(t, vid, "restock-locale-empty")

	waiting := map[string]string{
		"zh-Hant": "zh-waiting@example.com",
		"en":      "en-waiting@example.com",
	}
	for locale, address := range waiting {
		if _, err := pool.Exec(ctx, `
			INSERT INTO stock_notifications (variant_id, email, locale)
			VALUES ($1, $2, $3)`, vid, address, locale); err != nil {
			t.Fatalf("record interest from %s: %v", address, err)
		}
	}

	var actor uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '補貨')
		RETURNING id`, "restock-locale-"+sku+"@goen.invalid").Scan(&actor); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	staffCtx := account.WithUser(ctx, account.User{ID: actor.String(), Role: "admin"})

	if err := s.AdjustStock(staffCtx, sku, 10, actor.String(), "restock-locale-1"); err != nil {
		t.Fatalf("restock: %v", err)
	}

	var zhName, enName string
	if err := pool.QueryRow(ctx, `
		SELECT p.name, p.name_en FROM products p
		JOIN product_variants pv ON pv.product_id = p.id WHERE pv.id = $1`,
		vid).Scan(&zhName, &enName); err != nil {
		t.Fatalf("read both names: %v", err)
	}

	for locale, address := range waiting {
		var payload string
		if err := pool.QueryRow(ctx, `
			SELECT m.payload::text FROM outbox_messages m
			JOIN stock_notifications sn ON sn.id::text = m.dedupe_key
			WHERE m.topic = 'catalogue.restocked' AND sn.email = $1`,
			address).Scan(&payload); err != nil {
			t.Fatalf("read the %s payload: %v", locale, err)
		}
		want, other := zhName, enName
		if locale == "en" {
			want, other = enName, zhName
		}
		if !strings.Contains(payload, want) {
			t.Errorf("the %s letter does not name the product as %q: %s",
				locale, want, payload)
		}
		if strings.Contains(payload, other) {
			t.Errorf("the %s letter names the product as %q, the other language: %s",
				locale, other, payload)
		}
	}
}

func TestAltTextFollowsThePagesLanguage(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	slug := draftProduct(t, ctx, s)
	digest := storeMedia(t)

	if err := s.AttachImage(ctx, slug, digest, "銀色筆電,螢幕開啟",
		"Silver laptop, screen open", 800, 600); err != nil {
		t.Fatalf("AttachImage: %v", err)
	}

	for _, tt := range []struct {
		name   string
		locale string
		want   string
	}{
		{name: "a Chinese page", locale: "zh-Hant", want: "銀色筆電,螢幕開啟"},
		{name: "an English page", locale: "en", want: "Silver laptop, screen open"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			if err := pool.QueryRow(ctx, `
				SELECT localized_name(i.alt_text, i.alt_text_en, $2)
				FROM product_images i JOIN products p ON p.id = i.product_id
				WHERE p.slug = $1`, slug, tt.locale).Scan(&got); err != nil {
				t.Fatalf("read the alt text: %v", err)
			}
			if got != tt.want {
				t.Errorf("the %s announces %q, want %q", tt.name, got, tt.want)
			}
		})
	}

	second := draftProduct(t, ctx, s)
	if err := s.AttachImage(ctx, second, digest, "沒有英文說明", "", 800, 600); err != nil {
		t.Fatalf("AttachImage without English: %v", err)
	}
	var fallback string
	if err := pool.QueryRow(ctx, `
		SELECT localized_name(i.alt_text, i.alt_text_en, 'en')
		FROM product_images i JOIN products p ON p.id = i.product_id
		WHERE p.slug = $1`, second).Scan(&fallback); err != nil {
		t.Fatalf("read the alt text: %v", err)
	}
	if fallback != "沒有英文說明" {
		t.Errorf("an untranslated image announces %q, want the Chinese text", fallback)
	}
}

func storeMedia(t *testing.T) string {
	t.Helper()
	digest := fmt.Sprintf("%064x", uuid.New().ID())
	if _, err := pool.Exec(t.Context(), `
		-- byte_size must equal length(bytes): media_objects_size_matches refuses a
		-- row that claims a size its own bytes do not have.
		INSERT INTO media_objects (digest, content_type, byte_size, width, height, bytes)
		VALUES ($1, 'image/png', 1, 800, 600, '\x00'::bytea)`, digest); err != nil {
		t.Fatalf("store the image: %v", err)
	}
	return digest
}

func TestTheShopCanRunAPromotion(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	if errs, err := s.CreateBanner(ctx, &admin.BannerForm{
		Message: "全站滿 NT$3,000 免運", Short: "滿 3,000 免運",
		MessageEn: "Free delivery over NT$3,000", ShortEn: "Free over 3,000",
		CTALabel: "看看", CTAHref: "/deals", CTALabelEn: "Shop", Days: 7,
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateBanner: %v %v", err, errs)
	}

	banners, err := s.Banners(ctx)
	if err != nil {
		t.Fatalf("Banners: %v", err)
	}
	var made *pages.AdminBanner
	for i := range banners {
		if banners[i].Message == "全站滿 NT$3,000 免運" {
			made = &banners[i]
		}
	}
	if made == nil {
		t.Fatal("the promotion is not in the back office's list")
	}
	if !made.Active || !made.Translated() || !made.HasCTA() || !made.Scheduled() {
		t.Errorf("the promotion reads as %+v", *made)
	}

	shop := home.NewStore(pool)
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
	}{
		{name: "Chinese", locale: i18n.ZhHant, want: "全站滿 NT$3,000 免運"},
		{name: "English", locale: i18n.En, want: "Free delivery over NT$3,000"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			banner, bannerErr := shop.Banner(i18n.WithLocale(ctx, tt.locale), "")
			if bannerErr != nil {
				t.Fatalf("Banner: %v", bannerErr)
			}
			if banner.Message != tt.want {
				t.Errorf("the strip reads %q, want %q", banner.Message, tt.want)
			}
		})
	}

	if err := s.SetBannerActive(ctx, made.ID, false); err != nil {
		t.Fatalf("SetBannerActive: %v", err)
	}
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM promo_banners WHERE id = $1 AND NOT is_active`,
		made.ID).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Error("switching a promotion off deleted it")
	}
}

func TestAPromotionsButtonMustStayOnThisSite(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	for _, href := range []string{
		"https://evil.example/deals",
		"//evil.example/deals",
		"javascript:alert(1)",
	} {
		errs, err := s.CreateBanner(ctx, &admin.BannerForm{
			Message: "測試", CTALabel: "看看", CTAHref: href,
		})
		if err != nil {
			t.Fatalf("CreateBanner(%q): %v", href, err)
		}
		if errs["cta"] == "" {
			t.Errorf("CreateBanner accepted the href %q: %v", href, errs)
		}
	}

	errs, err := s.CreateBanner(ctx, &admin.BannerForm{Message: "測試", CTALabel: "看看"})
	if err != nil {
		t.Fatalf("CreateBanner: %v", err)
	}
	if errs["cta"] == "" {
		t.Errorf("a label with no href was accepted: %v", errs)
	}
}

func TestSupportCanAnswerAQuestionWithoutADeploy(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	category := "測試分類-" + uuid.NewString()[:8]

	if errs, err := s.CreateFAQEntry(ctx, &admin.FAQForm{
		Category: category, Question: "可以貨到付款嗎?", Answer: "目前只支援信用卡。",
		CategoryEn: "Testing", QuestionEn: "Can I pay on delivery?",
		AnswerEn: "Card only for now.",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateFAQEntry: %v %v", err, errs)
	}

	view, err := s.FAQ(ctx)
	if err != nil {
		t.Fatalf("FAQ: %v", err)
	}
	var made *pages.AdminFAQEntry
	for i := range view.Rows {
		if view.Rows[i].Category == category {
			made = &view.Rows[i]
		}
	}
	if made == nil {
		t.Fatal("the entry is not in the back office's list")
	}
	if !made.Translated() {
		t.Error("an entry with an English answer reads as untranslated")
	}

	content := site.NewStore(pool)
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
	}{
		{name: "Chinese", locale: i18n.ZhHant, want: "目前只支援信用卡。"},
		{name: "English", locale: i18n.En, want: "Card only for now."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rows, faqErr := content.FAQEntries(i18n.WithLocale(ctx, tt.locale))
			if faqErr != nil {
				t.Fatalf("FAQEntries: %v", faqErr)
			}
			var found bool
			for i := range rows {
				if rows[i].Answer == tt.want {
					found = true
				}
			}
			if !found {
				t.Errorf("/faq does not answer %q in %s", tt.want, tt.locale)
			}
		})
	}

	if errs, updErr := s.UpdateFAQEntry(ctx, &admin.FAQForm{
		ID: made.ID, Category: category,
		Question: made.Question, Answer: "現在也支援超商取貨付款。",
		QuestionEn: made.QuestionEn, AnswerEn: "Store pickup payment works now.",
	}); updErr != nil || len(errs) > 0 {
		t.Fatalf("UpdateFAQEntry: %v %v", updErr, errs)
	}
	rows, err := content.FAQEntries(i18n.WithLocale(ctx, i18n.En))
	if err != nil {
		t.Fatalf("FAQEntries: %v", err)
	}
	var rewritten bool
	for i := range rows {
		if rows[i].Answer == "Store pickup payment works now." {
			rewritten = true
		}
	}
	if !rewritten {
		t.Error("the rewritten answer is not on the page")
	}

	if err := s.DeleteFAQEntry(ctx, made.ID); err != nil {
		t.Fatalf("DeleteFAQEntry: %v", err)
	}
	var rows2 int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM faq_entries WHERE category = $1`, category).Scan(&rows2); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows2 != 0 {
		t.Errorf("%d entries survived the delete", rows2)
	}
}

func TestTwoFAQEntriesInOneCategoryDoNotCollide(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	category := "順序分類-" + uuid.NewString()[:8]

	for _, q := range []string{"第一個問題", "第二個問題", "第三個問題"} {
		if errs, err := s.CreateFAQEntry(ctx, &admin.FAQForm{
			Category: category, Question: q, Answer: "答案",
		}); err != nil || len(errs) > 0 {
			t.Fatalf("CreateFAQEntry(%s): %v %v", q, err, errs)
		}
	}

	var positions []int32
	rows, err := pool.Query(ctx,
		`SELECT position FROM faq_entries WHERE category = $1 ORDER BY position`, category)
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p int32
		if scanErr := rows.Scan(&p); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		positions = append(positions, p)
	}
	if diff := cmp.Diff([]int32{1, 2, 3}, positions); diff != "" {
		t.Errorf("positions (-want +got):\n%s", diff)
	}
}

func TestAShopCanOfferAThirdDeliveryMethod(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	emptyCart, cartErr := cart.NewStore(pool).Create(ctx, uuid.NewString(), uuid.NullUUID{})
	if cartErr != nil {
		t.Fatalf("create cart: %v", cartErr)
	}
	code := "express" + uuid.NewString()[:6]

	if errs, err := s.CreateMethod(ctx, &admin.NewMethod{
		Code: code, Destination: "address",
		Name: "隔日到貨", NameEn: "Next-day delivery",
		Carrier: "順豐", CarrierEn: "SF Express",
		FeeDollars: 150, FreeOverDollars: 5000,
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateMethod: %v %v", err, errs)
	}

	var versions int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.code = $1`, code).Scan(&versions); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if versions != 1 {
		t.Errorf("%d versions for a new method, want exactly 1", versions)
	}

	basket := cart.NewStore(pool)
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
	}{
		{name: "Chinese", locale: i18n.ZhHant, want: "隔日到貨"},
		{name: "English", locale: i18n.En, want: "Next-day delivery"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			choices, err := basket.ShippingChoices(i18n.WithLocale(ctx, tt.locale), emptyCart, 100000)
			if err != nil {
				t.Fatalf("ShippingChoices: %v", err)
			}
			var found bool
			for i := range choices {
				if choices[i].Name == tt.want {
					found = true
				}
			}
			if !found {
				t.Errorf("the checkout does not offer %q", tt.want)
			}
		})
	}

	var methodID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM shipping_methods WHERE code = $1`, code).Scan(&methodID); err != nil {
		t.Fatalf("read the method: %v", err)
	}
	if err := s.SetMethodActive(ctx, methodID, false); err != nil {
		t.Fatalf("SetMethodActive: %v", err)
	}
	choices, err := basket.ShippingChoices(ctx, emptyCart, 100000)
	if err != nil {
		t.Fatalf("ShippingChoices: %v", err)
	}
	for i := range choices {
		if choices[i].Name == "隔日到貨" {
			t.Error("a retired method is still offered at checkout")
		}
	}
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM shipping_methods WHERE code = $1`, code).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Error("retiring a method deleted it — every past order names its version")
	}
}

func TestAShopCanSayWhichPostalCodesCostMore(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	code := "remote" + uuid.NewString()[:6]

	if errs, err := s.CreateZone(ctx, &admin.NewZone{
		Code: code, Name: "山區", NameEn: "Mountain", Prefixes: "546 552, 553",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateZone: %v %v", err, errs)
	}

	var zoneID string
	var prefixes []string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM shipping_zones WHERE code = $1`, code).Scan(&zoneID); err != nil {
		t.Fatalf("read the zone: %v", err)
	}
	rows, err := pool.Query(ctx,
		`SELECT prefix FROM shipping_zone_prefixes WHERE zone_id = $1::uuid ORDER BY prefix`, zoneID)
	if err != nil {
		t.Fatalf("read prefixes: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if scanErr := rows.Scan(&p); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		prefixes = append(prefixes, p)
	}
	if diff := cmp.Diff([]string{"546", "552", "553"}, prefixes); diff != "" {
		t.Errorf("prefixes (-want +got):\n%s", diff)
	}

	other := "remote2" + uuid.NewString()[:6]
	if errs, zoneErr := s.CreateZone(ctx, &admin.NewZone{
		Code: other, Name: "另一區", Prefixes: "546",
	}); zoneErr != nil || len(errs) > 0 {
		t.Fatalf("CreateZone: %v %v", zoneErr, errs)
	}
	var owner string
	if err := pool.QueryRow(ctx, `
		SELECT z.code FROM shipping_zone_prefixes p
		JOIN shipping_zones z ON z.id = p.zone_id WHERE p.prefix = '546'`).Scan(&owner); err != nil {
		t.Fatalf("read the owner: %v", err)
	}
	if owner != other {
		t.Errorf("546 belongs to %q, want it moved to %q", owner, other)
	}
}

func TestAZonePrefixMustBeThreeDigits(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	for _, list := range []string{"88", "8801", "abc", "880 xx"} {
		errs, err := s.CreateZone(ctx, &admin.NewZone{
			Code: "bad" + uuid.NewString()[:6], Name: "壞的", Prefixes: list,
		})
		if err != nil {
			t.Fatalf("CreateZone(%q): %v", list, err)
		}
		if errs["prefixes"] == "" {
			t.Errorf("CreateZone accepted the prefix list %q: %v", list, errs)
		}
	}

	code := "empty" + uuid.NewString()[:6]
	if errs, err := s.CreateZone(ctx, &admin.NewZone{
		Code: code, Name: "空的", Prefixes: "999",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateZone: %v %v", err, errs)
	}
	var zoneID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM shipping_zones WHERE code = $1`, code).Scan(&zoneID); err != nil {
		t.Fatalf("read the zone: %v", err)
	}
	if err := s.DeleteZone(ctx, zoneID); !errors.Is(err, admin.ErrInUse) {
		t.Errorf("deleting a zone with prefixes gave %v, want ErrInUse", err)
	}
	if err := s.RemoveZonePrefix(ctx, zoneID, "999"); err != nil {
		t.Fatalf("RemoveZonePrefix: %v", err)
	}
	if err := s.DeleteZone(ctx, zoneID); err != nil {
		t.Errorf("deleting an empty zone gave %v", err)
	}
}

func TestTheStockLedgerCanBeRead(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	// A variant that ALREADY holds stock: at a prior balance of zero the running total
	// and the movement's own delta are the same number and the assertion proves nothing.
	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT sku FROM product_variants WHERE stock_quantity > 0
		ORDER BY stock_quantity DESC LIMIT 1`).Scan(&sku); err != nil {
		t.Fatalf("find a stocked variant: %v", err)
	}

	before, err := s.Movements(ctx, sku)
	if err != nil {
		t.Fatalf("Movements: %v", err)
	}
	if before.Stock <= 0 {
		t.Fatalf("the chosen variant holds %d — the running total would equal the "+
			"delta and prove nothing", before.Stock)
	}

	// A key unique to this run, or the second run of this suite is a no-op.
	key := "ledger-test-" + uuid.NewString()
	if adjErr := s.AdjustStock(ctx, sku, 7, staff.String(), key); adjErr != nil {
		t.Fatalf("AdjustStock: %v", adjErr)
	}

	after, err := s.Movements(ctx, sku)
	if err != nil {
		t.Fatalf("Movements: %v", err)
	}
	if len(after.Rows) != len(before.Rows)+1 {
		t.Fatalf("%d rows after one adjustment, had %d", len(after.Rows), len(before.Rows))
	}

	newest := after.Rows[0]
	if newest.Delta != 7 {
		t.Errorf("the newest movement is %d, want +7", newest.Delta)
	}
	if newest.Reason != "adjustment" || newest.ReasonText(ctx) != "人工調整" {
		t.Errorf("the movement reads as %q / %q", newest.Reason, newest.ReasonText(ctx))
	}
	if newest.By(ctx) == "系統" {
		t.Error("a hand adjustment is attributed to the system")
	}
	if newest.DeltaText() != "+7" {
		t.Errorf("the delta reads %q, want +7 — a bare 7 is half the story",
			newest.DeltaText())
	}
	if newest.Running != before.Stock+7 {
		t.Errorf("the newest running total is %d, want %d — the stock before plus "+
			"this movement", newest.Running, before.Stock+7)
	}
	if newest.Running == newest.Delta {
		t.Error("the running total equals this movement's own delta, so it is not " +
			"running over the ledger at all")
	}
	if newest.Running != after.Stock {
		t.Errorf("the newest running total is %d and the variant holds %d",
			newest.Running, after.Stock)
	}
}

// TestAReleaseInTheLedgerNamesItsOrder drives a RELEASE, the only movement that reaches
// the order through the reservation: a HOLD stays green with that join deleted.
func TestAReleaseInTheLedgerNamesItsOrder(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	basket := cart.NewStore(pool)

	var vid uuid.UUID
	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT pv.id, pv.sku FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.stock_quantity > pv.safety_stock + 2
		ORDER BY pv.stock_quantity DESC LIMIT 1`).Scan(&vid, &sku); err != nil {
		t.Fatalf("find a stocked variant: %v", err)
	}
	number := placeHeldOrder(t, vid)

	if _, err := basket.Cancel(ctx, number); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	view, err := s.Movements(ctx, sku)
	if err != nil {
		t.Fatalf("Movements: %v", err)
	}
	var release, hold bool
	for i := range view.Rows {
		m := &view.Rows[i]
		if m.OrderNumber != number {
			continue
		}
		switch m.Reason {
		case "release":
			release = true
		case "hold":
			hold = true
		}
	}
	if !release {
		seen := make([]string, 0, len(view.Rows))
		for i := range view.Rows {
			seen = append(seen, view.Rows[i].Reason+"/"+view.Rows[i].OrderNumber)
		}
		t.Errorf("no release naming %s in the ledger: %v", number, seen)
	}
	if !hold {
		t.Errorf("no hold naming %s in the ledger", number)
	}
}

func TestTheOrderPageShowsTheInvoiceChoice(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number := placeUnpaidOrder(t)

	var orderID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatalf("read the order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_preferences (order_id, invoice_type, tax_id)
		VALUES ($1, 'company', '12345678')`, orderID); err != nil {
		t.Fatalf("record the preference: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if !view.HasInvoice() {
		t.Fatal("the order page does not show a 發票 preference that exists")
	}
	if view.InvoiceText(ctx) != "公司統編 12345678" {
		t.Errorf("the page says %q, want 公司統編 12345678", view.InvoiceText(ctx))
	}

	plain, err := s.Order(ctx, placeUnpaidOrder(t))
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if plain.HasInvoice() {
		t.Error("an order with no preference reports one")
	}
}

func TestTheShopSetsEachProductsWarrantyTerm(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	slug := draftProduct(t, ctx, s)

	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if view.WarrantyMonths != 0 || view.WarrantyMonthsText() != "" {
		t.Errorf("a new product reports %d months (%q)",
			view.WarrantyMonths, view.WarrantyMonthsText())
	}

	form := &admin.ProductForm{
		Slug: slug, Name: view.Name, Summary: view.Summary,
		Description: view.Description, BrandID: view.BrandID,
		CategoryID: view.CategoryID, WarrantyMonths: 24,
	}
	if errs, updErr := s.UpdateProduct(ctx, form); updErr != nil || len(errs) > 0 {
		t.Fatalf("UpdateProduct: %v %v", updErr, errs)
	}

	after, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if after.WarrantyMonths != 24 {
		t.Errorf("the product states %d months, want 24", after.WarrantyMonths)
	}

	form.WarrantyMonths = 0
	if errs, updErr := s.UpdateProduct(ctx, form); updErr != nil || len(errs) > 0 {
		t.Fatalf("UpdateProduct: %v %v", updErr, errs)
	}
	var months *int32
	if scanErr := pool.QueryRow(ctx,
		`SELECT warranty_months FROM products WHERE slug = $1`, slug).Scan(&months); scanErr != nil {
		t.Fatalf("read the term: %v", scanErr)
	}
	if months != nil {
		t.Errorf("clearing the term left %d", *months)
	}

	form.WarrantyMonths = admin.MaxWarrantyMonths + 1
	errs, err := s.UpdateProduct(ctx, form)
	if err != nil {
		t.Fatalf("UpdateProduct: %v", err)
	}
	if errs["warranty_months"] == "" {
		t.Errorf("a %d-month term was accepted: %v", form.WarrantyMonths, errs)
	}
}

// returnedOrderWithStock is returnedOrder with a REAL variant behind its line, and its
// own product per call: returning produces stock, so a shared row fails shuffled.
func returnedOrderWithStock(t *testing.T, name string, qty int32) (requestID, variantID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	slug := name + "-" + uuid.NewString()[:8]
	var productID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, $1, '退貨測試商品', 'active', now()
		FROM brands b, categories c WHERE b.slug = 'pixelight' AND c.slug = 'phones'
		RETURNING id`, slug).Scan(&productID); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, safety_stock, position)
		VALUES ($1, upper($2), 100000, 0, 1) RETURNING id`,
		productID, slug).Scan(&variantID); err != nil {
		t.Fatalf("create variant: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT record_inventory_movement($1, 5, 'receipt', $2, NULL, NULL, NULL)`,
		variantID, "seed:"+slug); err != nil {
		t.Fatalf("stock the variant: %v", err)
	}

	var orderID, lineID uuid.UUID
	var orderNumber string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, $2, upper($3), '退貨測試商品', 100000, 2) RETURNING id`,
		orderID, variantID, slug).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'r@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 200000)`,
		orderID, "cs_rets_"+orderNumber); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 200000, NULL, NULL)`,
		"cs_rets_"+orderNumber); err != nil {
		t.Fatalf("capture: %v", err)
	}
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, '黑貓', 'TS-'||$2) RETURNING id`,
		orderID, orderNumber).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 2)`, orderID, shipmentID, lineID); err != nil {
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
	return requestID, variantID
}

func stockOf(t *testing.T, variantID uuid.UUID) int32 {
	t.Helper()
	var n int32
	if err := pool.QueryRow(t.Context(),
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, variantID).Scan(&n); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return n
}

func returnLineID(t *testing.T, requestID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(),
		`SELECT order_line_id FROM return_request_lines WHERE return_request_id = $1`,
		requestID).Scan(&id); err != nil {
		t.Fatalf("read return line: %v", err)
	}
	return id
}

func TestAnInspectedReturnPutsTheSellableUnitsBack(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, variantID := returnedOrderWithStock(t, "restock", 2)
	lineID := returnLineID(t, requestID)

	before := stockOf(t, variantID)
	if err := s.Decide(ctx, requestID.String(), "approved", "", actor); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got := stockOf(t, variantID); got != before {
		t.Fatalf("approving a return moved stock %d -> %d; nothing has come back yet",
			before, got)
	}

	if err := s.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: lineID, Received: 2, Restocked: 1, Note: "一件外盒破損",
	}}, actor); err != nil {
		t.Fatalf("inspect: %v", err)
	}

	if got, want := stockOf(t, variantID), before+1; got != want {
		t.Errorf("stock is %d after restocking one of two returned units, want %d", got, want)
	}

	var reason, sourceType string
	var delta int32
	if err := pool.QueryRow(ctx, `
		SELECT reason, coalesce(source_type, ''), delta FROM inventory_movements
		WHERE variant_id = $1 ORDER BY created_at DESC LIMIT 1`,
		variantID).Scan(&reason, &sourceType, &delta); err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	if reason != "return" || delta != 1 {
		t.Errorf("the ledger says %q %+d, want return +1", reason, delta)
	}
	if sourceType != "return_request" {
		t.Errorf("the movement points at %q, want return_request — a shop asking "+
			"why the number moved gets no answer otherwise", sourceType)
	}
}

func TestAReturnCannotCloseWithAnUninspectedLine(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, variantID := returnedOrderWithStock(t, "uninspected", 2)
	lineID := returnLineID(t, requestID)

	if err := s.Decide(ctx, requestID.String(), "approved", "", actor); err != nil {
		t.Fatalf("approve: %v", err)
	}

	if err := s.CompleteReturn(ctx, requestID.String(), "", actor); !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("closing an uninspected return = %v, want ErrRefused", err)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "approved" {
		t.Errorf("the return is %q after a refused completion, want approved", status)
	}

	if err := s.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: lineID, Received: 2, Restocked: 2,
	}}, actor); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if err := s.CompleteReturn(ctx, requestID.String(), "已退款並入庫", actor); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "completed" {
		t.Errorf("the return is %q after closing, want completed", status)
	}
	if got, want := stockOf(t, variantID), int32(5-0+2); got != want {
		t.Errorf("stock is %d after closing, want %d — completing a return must not "+
			"restock a second time", got, want)
	}
}

func TestInspectingIsRefusedBeforeApproval(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, variantID := returnedOrderWithStock(t, "unapproved", 2)
	lineID := returnLineID(t, requestID)

	before := stockOf(t, variantID)
	err := s.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: lineID, Received: 2, Restocked: 2,
	}}, actor)
	if !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("inspecting an undecided return = %v, want ErrRefused", err)
	}
	if got := stockOf(t, variantID); got != before {
		t.Errorf("stock moved %d -> %d on a refused inspection", before, got)
	}
}

func TestRestockingMoreThanArrivedIsRefused(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, variantID := returnedOrderWithStock(t, "overrestock", 1)
	lineID := returnLineID(t, requestID)

	if err := s.Decide(ctx, requestID.String(), "approved", "", actor); err != nil {
		t.Fatalf("approve: %v", err)
	}
	before := stockOf(t, variantID)

	err := s.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: lineID, Received: 1, Restocked: 2,
	}}, actor)
	if !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("restocking 2 of 1 received = %v, want ErrInvalid", err)
	}
	if got := stockOf(t, variantID); got != before {
		t.Errorf("stock moved %d -> %d on a refused inspection", before, got)
	}
}

func TestALineIsInspectedOnceAndACorrectionIsAnAdjustment(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, variantID := returnedOrderWithStock(t, "twice", 2)
	lineID := returnLineID(t, requestID)

	if err := s.Decide(ctx, requestID.String(), "approved", "", actor); err != nil {
		t.Fatalf("approve: %v", err)
	}
	before := stockOf(t, variantID)

	if err := s.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: lineID, Received: 2, Restocked: 2,
	}}, actor); err != nil {
		t.Fatalf("first inspection: %v", err)
	}

	err := s.InspectReturn(ctx, requestID.String(), []admin.ReturnLineInspection{{
		OrderLineID: lineID, Received: 2, Restocked: 1,
	}}, actor)
	if !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("re-inspecting = %v, want ErrRefused", err)
	}

	if got, want := stockOf(t, variantID), before+2; got != want {
		t.Errorf("stock is %d after a refused re-inspection, want %d", got, want)
	}
	var movements int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_movements
		WHERE variant_id = $1 AND reason = 'return'`, variantID).Scan(&movements); err != nil {
		t.Fatalf("count movements: %v", err)
	}
	if movements != 1 {
		t.Errorf("%d return movements after two inspections, want 1", movements)
	}

	var restocked int32
	if err := pool.QueryRow(ctx, `
		SELECT restocked_quantity FROM return_request_lines
		WHERE return_request_id = $1`, requestID).Scan(&restocked); err != nil {
		t.Fatalf("read the line: %v", err)
	}
	if restocked != 2 {
		t.Errorf("the line records %d restocked, want 2 — the refused submission "+
			"changed the record without changing the stock", restocked)
	}
}

func twoLineOrderWithStock(t *testing.T, name string) (
	number string, orderID uuid.UUID, lineIDs, variantIDs []uuid.UUID,
) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}

	for i := range 2 {
		slug := fmt.Sprintf("%s-%d-%s", name, i, uuid.NewString()[:8])
		var productID, variantID, lineID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
			SELECT b.id, c.id, $1, '分批出貨測試', 'active', now()
			FROM brands b, categories c WHERE b.slug = 'pixelight' AND c.slug = 'phones'
			RETURNING id`, slug).Scan(&productID); err != nil {
			t.Fatalf("create product: %v", err)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO product_variants (product_id, sku, price_cents, safety_stock, position)
			VALUES ($1, upper($2), 100000, 0, 1) RETURNING id`,
			productID, slug).Scan(&variantID); err != nil {
			t.Fatalf("create variant: %v", err)
		}
		if _, err := tx.Exec(ctx,
			`SELECT record_inventory_movement($1, 10, 'receipt', $2, NULL, NULL, NULL)`,
			variantID, "seed:"+slug); err != nil {
			t.Fatalf("stock the variant: %v", err)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, $2, upper($3), '分批出貨測試', 100000, 3, $4) RETURNING id`,
			orderID, variantID, slug, i).Scan(&lineID); err != nil {
			t.Fatalf("create line: %v", err)
		}
		if _, err := tx.Exec(ctx,
			`SELECT hold_inventory($1, $2, 3, now() + interval '30 minutes', $3)`,
			orderID, variantID, "hold:"+slug); err != nil {
			t.Fatalf("hold: %v", err)
		}
		lineIDs = append(lineIDs, lineID)
		variantIDs = append(variantIDs, variantID)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'p@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 600000)`,
		orderID, "cs_part_"+number); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 600000, NULL, NULL)`,
		"cs_part_"+number); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move to picking: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, orderID, lineIDs, variantIDs
}

func heldFor(t *testing.T, orderID, variantID uuid.UUID) int32 {
	t.Helper()
	var n int32
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce(sum(quantity), 0)::integer FROM inventory_reservations
		WHERE order_id = $1 AND variant_id = $2 AND state = 'held'`,
		orderID, variantID).Scan(&n); err != nil {
		t.Fatalf("read held: %v", err)
	}
	return n
}

func shippedFor(t *testing.T, lineID uuid.UUID) int32 {
	t.Helper()
	var n int32
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce(sum(quantity), 0)::integer FROM order_shipment_lines
		WHERE order_line_id = $1`, lineID).Scan(&n); err != nil {
		t.Fatalf("read shipped: %v", err)
	}
	return n
}

func TestAnOrderCanShipInTwoParcels(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, variants := twoLineOrderWithStock(t, "partial")

	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "黑貓宅急便", Tracking: "P1-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 2},
	}, actor); err != nil {
		t.Fatalf("first parcel: %v", err)
	}

	if got := shippedFor(t, lines[0]); got != 2 {
		t.Errorf("line 1 has shipped %d, want 2", got)
	}
	if got := shippedFor(t, lines[1]); got != 0 {
		t.Errorf("line 2 has shipped %d, want 0 — it was not in this parcel", got)
	}
	if got := heldFor(t, orderID, variants[0]); got != 1 {
		t.Errorf("line 1 still holds %d, want 1", got)
	}
	if got := heldFor(t, orderID, variants[1]); got != 3 {
		t.Errorf("line 2 still holds %d, want 3 — nothing of it has gone out", got)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "shipped" {
		t.Errorf("the order is %q after its first parcel, want shipped", status)
	}

	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "黑貓宅急便", Tracking: "P2-" + number,
	}, actor); err != nil {
		t.Fatalf("second parcel: %v", err)
	}

	if got := shippedFor(t, lines[0]); got != 3 {
		t.Errorf("line 1 has shipped %d after both parcels, want 3", got)
	}
	if got := shippedFor(t, lines[1]); got != 3 {
		t.Errorf("line 2 has shipped %d after both parcels, want 3", got)
	}
	if got := heldFor(t, orderID, variants[0]); got != 0 {
		t.Errorf("line 1 still holds %d after shipping everything, want 0 — a "+
			"sweeper would return goods that have gone out", got)
	}
	if got := heldFor(t, orderID, variants[1]); got != 0 {
		t.Errorf("line 2 still holds %d after shipping everything, want 0", got)
	}

	var parcels int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_shipments WHERE order_id = $1`, orderID).Scan(&parcels); err != nil {
		t.Fatalf("count parcels: %v", err)
	}
	if parcels != 2 {
		t.Errorf("%d parcels, want 2", parcels)
	}

	err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "黑貓宅急便", Tracking: "P3-" + number,
	}, actor)
	if !errors.Is(err, admin.ErrRefused) {
		t.Errorf("a third parcel on a fully shipped order = %v, want ErrRefused", err)
	}
}

func TestAParcelCannotCarryMoreThanRemains(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, variants := twoLineOrderWithStock(t, "overship")

	err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "黑貓宅急便", Tracking: "OVER-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 4},
	}, actor)
	if !errors.Is(err, admin.ErrQuantity) {
		t.Fatalf("shipping 4 of 3 = %v, want ErrQuantity", err)
	}

	var parcels int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_shipments WHERE order_id = $1`, orderID).Scan(&parcels); err != nil {
		t.Fatalf("count parcels: %v", err)
	}
	if parcels != 0 {
		t.Errorf("%d parcels after a refused dispatch, want 0", parcels)
	}
	if got := heldFor(t, orderID, variants[0]); got != 3 {
		t.Errorf("the hold moved to %d on a refused dispatch, want 3", got)
	}
}

func TestAnEmptyParcelIsRefused(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, _ := twoLineOrderWithStock(t, "emptyparcel")

	err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "黑貓宅急便", Tracking: "EMPTY-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 0, lines[1]: 0},
	}, actor)
	if !errors.Is(err, admin.ErrQuantity) {
		t.Fatalf("a parcel carrying nothing = %v, want ErrQuantity", err)
	}
	var parcels int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_shipments WHERE order_id = $1`, orderID).Scan(&parcels); err != nil {
		t.Fatalf("count parcels: %v", err)
	}
	if parcels != 0 {
		t.Errorf("%d parcels after an empty dispatch, want 0", parcels)
	}
}

func TestEachParcelTellsTheCustomer(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, _ := twoLineOrderWithStock(t, "notice")

	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "黑貓宅急便", Tracking: "N1-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 3},
	}, actor); err != nil {
		t.Fatalf("first parcel: %v", err)
	}
	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "黑貓宅急便", Tracking: "N2-" + number,
		Lines: map[uuid.UUID]int32{lines[1]: 3},
	}, actor); err != nil {
		t.Fatalf("second parcel: %v", err)
	}

	var notices int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = 'order.shipped' AND payload->>'order_number' = $1`,
		number).Scan(&notices); err != nil {
		t.Fatalf("count notices: %v", err)
	}
	if notices != 2 {
		t.Errorf("%d dispatch notices for two parcels, want 2 — a customer told "+
			"about one box is left wondering about the other", notices)
	}

	var events int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'shipped'`,
		orderID).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 2 {
		t.Errorf("%d shipped events for two parcels, want 2", events)
	}
}

func TestAReceiptIsFiledAsAReceiptAndNotAnAdjustment(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := staffID(t)

	var sku string
	var before int32
	if err := pool.QueryRow(ctx, `
		SELECT sku, stock_quantity FROM product_variants
		WHERE is_active ORDER BY position LIMIT 1`).Scan(&sku, &before); err != nil {
		t.Fatalf("read variant: %v", err)
	}

	key := "receipt-" + uuid.NewString()
	if err := s.ReceiveStock(ctx, sku, 12, actor, key); err != nil {
		t.Fatalf("receive: %v", err)
	}

	var delta int32
	var reason, source string
	var hasActor bool
	if err := pool.QueryRow(ctx, `
		SELECT m.delta, m.reason, coalesce(m.source_type, ''), m.actor_user_id IS NOT NULL
		FROM inventory_movements m WHERE m.idempotency_key = $1`, key).
		Scan(&delta, &reason, &source, &hasActor); err != nil {
		t.Fatalf("the receipt left no movement row: %v", err)
	}
	if delta != 12 || reason != "receipt" || source != "admin" || !hasActor {
		t.Errorf("movement = %d/%q/%q/actor:%v, want 12/receipt/admin/actor:true",
			delta, reason, source, hasActor)
	}

	var after int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE sku = $1`, sku).Scan(&after); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if after != before+12 {
		t.Errorf("stock went %d -> %d, want %d", before, after, before+12)
	}
}

func TestAReceiptIsIdempotent(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := staffID(t)

	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT sku FROM product_variants WHERE is_active ORDER BY position DESC LIMIT 1`).
		Scan(&sku); err != nil {
		t.Fatalf("read variant: %v", err)
	}

	key := "receipt-dup-" + uuid.NewString()
	if err := s.ReceiveStock(ctx, sku, 4, actor, key); err != nil {
		t.Fatalf("first receipt: %v", err)
	}
	var afterFirst int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE sku = $1`, sku).Scan(&afterFirst); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	if err := s.ReceiveStock(ctx, sku, 4, actor, key); err == nil {
		t.Error("the same idempotency key booked one delivery in twice")
	}
	var afterSecond int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE sku = $1`, sku).Scan(&afterSecond); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if afterSecond != afterFirst {
		t.Errorf("stock moved again on the repeat: %d -> %d", afterFirst, afterSecond)
	}
}

func TestAReceiptCannotTakeStockAway(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := staffID(t)

	var sku string
	var before int32
	if err := pool.QueryRow(ctx, `
		SELECT sku, stock_quantity FROM product_variants
		WHERE is_active AND stock_quantity > 5 ORDER BY position LIMIT 1`).Scan(&sku, &before); err != nil {
		t.Fatalf("read variant: %v", err)
	}

	key := "receipt-neg-" + uuid.NewString()
	if err := s.ReceiveStock(ctx, sku, -3, actor, key); !errors.Is(err, admin.ErrRefused) {
		t.Errorf("a negative receipt gave %v, want ErrRefused — a correction filed "+
			"as a delivery is the distinction this door exists to draw", err)
	}
	var after int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE sku = $1`, sku).Scan(&after); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if after != before {
		t.Errorf("the refused receipt moved stock %d -> %d", before, after)
	}
}

func TestTheShopCanFindAWarrantyTheCustomerRegistered(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	serial, number := registeredWarranty(t, "SN-"+strings.ToUpper(uuid.NewString()[:8]))

	for _, term := range []struct {
		name string
		q    string
	}{
		{name: "by the serial off the label", q: serial},
		{name: "by the order number off the confirmation mail", q: number},
	} {
		t.Run(term.name, func(t *testing.T) {
			view, err := s.Warranties(ctx, term.q)
			if err != nil {
				t.Fatalf("search warranties: %v", err)
			}
			if !view.Searching() {
				t.Fatal("the page did not run a search for a term long enough to be one")
			}
			if len(view.Rows) != 1 {
				t.Fatalf("searching %q found %d registrations, want 1", term.q, len(view.Rows))
			}
			got := view.Rows[0]
			if got.Serial != serial || got.Order != number {
				t.Errorf("found serial %q on order %q, want %q on %q",
					got.Serial, got.Order, serial, number)
			}
			if !got.InForce {
				t.Error("a warranty registered today reads as expired")
			}
		})
	}

	view, err := s.Warranties(ctx, "SN-NOSUCHTHING")
	if err != nil {
		t.Fatalf("search warranties: %v", err)
	}
	if len(view.Rows) != 0 {
		t.Errorf("an unregistered serial found %d rows, want 0", len(view.Rows))
	}
}

func TestTheWarrantyLookupRefusesToListEverything(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	registeredWarranty(t, "SN-"+strings.ToUpper(uuid.NewString()[:8]))

	for _, term := range []string{"", " ", "A"} {
		view, err := s.Warranties(ctx, term)
		if err != nil {
			t.Fatalf("search warranties %q: %v", term, err)
		}
		if view.Searching() {
			t.Errorf("%q ran a search", term)
		}
		if len(view.Rows) != 0 {
			t.Errorf("%q listed %d registrations without being asked", term, len(view.Rows))
		}
	}
}

func registeredWarranty(t *testing.T, serial string) (registered, orderNumber string) {
	t.Helper()
	ctx := t.Context()

	var variantID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.is_active AND p.warranty_months IS NOT NULL LIMIT 1`).Scan(&variantID); err != nil {
		t.Fatalf("find a variant of a product with a stated term: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('wr-' || gen_random_uuid() || '@goen.invalid', 'customer', '保固客戶')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create customer: %v", err)
	}

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}

	var lineID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, $2, 'WR-SKU', '保固測試商品', 100000, 1) RETURNING id`,
		orderID, variantID).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'wr@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number, shipped_at, delivered_at)
		VALUES ($1, '黑貓', 'WR-' || $2, now() - interval '5 days', now() - interval '3 days')
		RETURNING id`, orderID, number).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := warranty.NewStore(pool).Register(
		ctx, lineID.String(), userID.String(), serial, 1); err != nil {
		t.Fatalf("register warranty: %v", err)
	}
	return serial, number
}

// TestACategoryCreatedInTheBackOfficeCanCarryAnIcon covers categories.icon_key. The
// icon set is closed and icons.Category has no default arm, so an unknown key is refused.
func TestACategoryCreatedInTheBackOfficeCanCarryAnIcon(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	slug := "iconcat-" + uuid.NewString()[:8]

	errs, err := s.CreateCategory(ctx, &admin.TaxonomyForm{
		Slug: slug, Name: "圖示分類", IconKey: "laptop",
	})
	if err != nil || len(errs) > 0 {
		t.Fatalf("create category: err=%v fields=%v", err, errs)
	}

	var icon string
	if readErr := pool.QueryRow(ctx,
		`SELECT coalesce(icon_key, '') FROM categories WHERE slug = $1`, slug).Scan(&icon); readErr != nil {
		t.Fatalf("read icon: %v", readErr)
	}
	if icon != "laptop" {
		t.Errorf("icon_key = %q, want %q — the home page tiles this category with "+
			"whatever is here, and an empty one draws nothing", icon, "laptop")
	}

	if renameErr := s.Rename(ctx, "category", slug, "改名分類", "", "laptop"); renameErr != nil {
		t.Fatalf("rename: %v", renameErr)
	}
	if readErr := pool.QueryRow(ctx,
		`SELECT coalesce(icon_key, '') FROM categories WHERE slug = $1`, slug).Scan(&icon); readErr != nil {
		t.Fatalf("read icon after rename: %v", readErr)
	}
	if icon != "laptop" {
		t.Errorf("after a rename icon_key = %q, want laptop", icon)
	}

	bad, err := s.CreateCategory(ctx, &admin.TaxonomyForm{
		Slug: "iconbad-" + uuid.NewString()[:8], Name: "壞圖示", IconKey: "rocket",
	})
	if err != nil {
		t.Fatalf("create with an unknown icon: %v", err)
	}
	if _, refused := bad["icon_key"]; !refused {
		t.Error("an icon outside the set icons.Category can draw was accepted, " +
			"which stores a value the home page renders as nothing")
	}
}

// TestTheLoserOfTwoSimultaneousDecisionsPostsNoCredit is the same rule with no
// provider anywhere in it.
//
// The card path can look like a Stripe problem. This one cannot: a wholly
// credit-funded order has no payment row at all, so the losing decision's payout
// is a single INSERT into the ledger — committed, on its own, before the CAS it
// was about to lose. The customer's balance went up on a return the shop had
// just refused, and nothing recorded who did it.
func TestTheLoserOfTwoSimultaneousDecisionsPostsNoCredit(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	// WHOLLY credit-funded: no payment row exists, so the payout on this path is
	// one INSERT into the ledger and there is no provider to blame.
	requestID, _, accountID := creditFundedReturn(t, 2, 200000)
	before := creditBalance(t, accountID)

	t1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin T1: %v", err)
	}
	defer func() { _ = t1.Rollback(ctx) }()
	if _, err := t1.Exec(ctx, `
		UPDATE return_requests SET status = 'rejected', decided_at = now()
		WHERE id = $1 AND status = 'requested'`, requestID); err != nil {
		t.Fatalf("T1 decide: %v", err)
	}

	decided := make(chan error, 1)
	go func() { decided <- s.Decide(ctx, requestID.String(), "approved", "", uuid.NullUUID{}) }()

	select {
	case err := <-decided:
		t.Fatalf("T2 finished before T1 committed (%v); it never met the lock", err)
	case <-time.After(250 * time.Millisecond):
	}
	if err := t1.Commit(ctx); err != nil {
		t.Fatalf("commit T1: %v", err)
	}
	if err := <-decided; !errors.Is(err, admin.ErrRefused) {
		t.Errorf("the second decision returned %v, want ErrRefused", err)
	}

	if after := creditBalance(t, accountID); after != before {
		t.Errorf("store credit went %d -> %d on a return that was REJECTED", before, after)
	}
}

func creditBalance(t *testing.T, accountID uuid.UUID) int64 {
	t.Helper()
	var cents int64
	if err := pool.QueryRow(t.Context(),
		`SELECT coalesce(sum(amount_cents), 0)::bigint FROM store_credit_entries
		 WHERE account_id = $1`, accountID).Scan(&cents); err != nil {
		t.Fatalf("read credit balance: %v", err)
	}
	return cents
}

// TestAnOrderCannotFinishWhileItStillOwesAParcel holds stock that used to be
// stranded permanently and invisibly.
//
// An order ships in as many parcels as it takes, and only the first moves the
// status. But the transition guard asked nothing about what was outstanding,
// and the status dropdown offers 已送達 and 已完成 as peers — so shipping one
// parcel of several and then finishing the order left the remaining
// reservations `held` with no door out: release_reservation refuses them by
// name because a completed order is committed, ExpiredReservations excludes
// committed orders by predicate, and /admin/health counts expired holds with
// that same predicate. The units were off the shelf forever, invisible on the
// one page built to make stock backlogs visible, while the customer read 已完成
// for goods that never left.
//
// It refuses rather than releasing: what has not gone out is either still going
// out — CanShip already allows the second parcel — or it is an abandonment,
// which is a decision a person makes rather than a side effect of a dropdown.
func TestAnOrderCannotFinishWhileItStillOwesAParcel(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, variants := twoLineOrderWithStock(t, "unfinished")

	// One parcel, carrying part of the first line only.
	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "黑貓宅急便", Tracking: "U1-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 1},
	}, actor); err != nil {
		t.Fatalf("first parcel: %v", err)
	}

	// DELIVERED is allowed and must be: it is a fact about the parcel that went
	// out, and it is the ONLY thing that stamps order_shipments.delivered_at.
	// Refusing it left a partially shipped order unable to record that anything
	// had arrived, so /admin/returns read 尚未送達 for goods the customer held —
	// on the screen built to inform a 消保法 §19 decision.
	if _, err := s.Advance(ctx, number, "delivered", actor); err != nil {
		t.Fatalf("a partially shipped order could not record its first parcel as "+
			"delivered: %v", err)
	}
	var stamped int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_shipments sh JOIN orders o ON o.id = sh.order_id
		WHERE o.order_number = $1 AND sh.delivered_at IS NOT NULL`,
		number).Scan(&stamped); err != nil {
		t.Fatalf("count stamped parcels: %v", err)
	}
	if stamped == 0 {
		t.Error("no parcel was stamped delivered, so the rescission window never starts")
	}

	// COMPLETED is refused: the order is not finished while it still owes a parcel.
	_, err := s.Advance(ctx, number, "completed", actor)
	if err == nil {
		t.Fatal("an order still owing a parcel was completed; whatever is still " +
			"held is now stranded with no door out")
	}
	// The `ok` is asserted, not used as a condition. Advance used to wrap with
	// "%w: %s", which puts the message in the string and the PgError nowhere in
	// the chain — so `ok` was always false, the whole clause was skipped, and the
	// test asked only that SOMETHING was refused. Every other rule on that
	// statement passed it, including the pre-database refusals that never reach
	// PostgreSQL at all.
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		t.Fatalf("the refusal does not carry the rule that made it: %v\n"+
			"Asserting only that an error happened cannot tell one rule from another", err)
	}
	if pgErr.ConstraintName != "orders_finished_when_shipped" {
		t.Errorf("refused by %q, want orders_finished_when_shipped — a statement "+
			"meant to prove one rule often trips another first", pgErr.ConstraintName)
	}

	// And once everything has gone out, finishing works and nothing is held.
	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "黑貓宅急便", Tracking: "U2-" + number,
	}, actor); err != nil {
		t.Fatalf("second parcel: %v", err)
	}
	if _, err := s.Advance(ctx, number, "completed", actor); err != nil {
		t.Fatalf("a fully shipped order could not be completed: %v", err)
	}
	for i, v := range variants {
		if got := heldFor(t, orderID, v); got != 0 {
			t.Errorf("line %d still holds %d on a completed order", i+1, got)
		}
	}
}

// TestTwoCarriersSharingATrackingNumberBothNotify holds a dedupe key that was
// narrower than the fact it was deduplicating.
//
// order_shipments is unique on (carrier, tracking_number) — the schema's own
// statement that a tracking number identifies a parcel only alongside who is
// carrying it. The dispatch notice keyed on the tracking number ALONE, so a
// second shipment with a colliding number from a different carrier met
// ON CONFLICT DO NOTHING in the outbox: Ship still succeeded, the parcel went
// out, and the customer was never told.
//
// The comment above it says an order shipped in two parcels is two notices, and
// that stays true — two parcels of one order carry two tracking numbers. What
// it did not cover is two parcels of DIFFERENT orders that happen to share one.
func TestTwoCarriersSharingATrackingNumberBothNotify(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}

	const shared = "SHARED-1234567890"
	first, _, firstLines, _ := twoLineOrderWithStock(t, "carrier-a")
	second, _, secondLines, _ := twoLineOrderWithStock(t, "carrier-b")

	if err := s.Ship(ctx, first, admin.Dispatch{
		Carrier: "黑貓宅急便", Tracking: shared,
		Lines: map[uuid.UUID]int32{firstLines[0]: 1},
	}, actor); err != nil {
		t.Fatalf("first carrier: %v", err)
	}
	if err := s.Ship(ctx, second, admin.Dispatch{
		Carrier: "新竹物流", Tracking: shared,
		Lines: map[uuid.UUID]int32{secondLines[0]: 1},
	}, actor); err != nil {
		t.Fatalf("second carrier: %v — the database allows the pair, so the "+
			"application must too", err)
	}

	var notices int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = 'order.shipped' AND payload->>'tracking' = $1`, shared).Scan(&notices); err != nil {
		t.Fatalf("count dispatch notices: %v", err)
	}
	if notices != 2 {
		t.Errorf("%d dispatch notices for two parcels sharing a tracking number, "+
			"want 2 — one customer's parcel left and nothing told them", notices)
	}
}

// TestASplitReturnResumesTheHalfThatFailed holds a resume gate that asked one
// of two questions.
//
// A return can be paid from BOTH sources — card first, credit last — and the
// two commit separately: the card through the provider, the credit as a ledger
// entry afterwards. The gate deciding whether a retry has anything left to do
// asked only whether the CARD half had settled. So a return whose card refund
// landed and whose credit compensation did NOT was reported as finished: the
// retry was refused by name, no other door posts that credit, and the customer
// was short by the credit portion with nothing on /admin/health saying so.
//
// It also has to resume only what is MISSING. Re-sending a settled card refund
// meets refunds_settled_is_history and re-posting the credit meets its
// idempotency key, so a retry that sent both could never finish the failed half.
//
// The half-paid state is CONSTRUCTED rather than raced into. An earlier version
// injected the failure by holding the credit account's row and cancelling on a
// timer, and it was flaky three runs in four — sometimes the credit landed
// anyway, and the test then failed for a reason that had nothing to do with the
// gate. What is under test is the resume, not how the state arose.
func TestASplitReturnResumesTheHalfThatFailed(t *testing.T) {
	ctx, _ := staffContext(t)
	requestID, orderNumber, accountID := creditFundedReturn(t, 2, 60000)

	// Approved, with the CARD half settled under the return's own request key —
	// which is what refundRequestKey produces and what makes a retry find the
	// same row — and no credit entry at all.
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests SET status = 'approved', decided_at = now(),
		       resolution = '退貨完成'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve the return: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, reason,
		                     return_request_id, status, provider_ref, succeeded_at)
		SELECT p.id, 'return:' || $1::text, 140000, '退貨完成', $1::uuid, 'succeeded',
		       're_constructed', now()
		FROM payments p JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $2 AND p.status = 'succeeded'`,
		requestID, orderNumber); err != nil {
		t.Fatalf("settle the card half: %v", err)
	}
	if got := cardRefunded(t, orderNumber); got != 140000 {
		t.Fatalf("the card half reads %d, want 140000 — the fixture did not build the "+
			"state under test", got)
	}
	before := creditBalance(t, accountID)

	// The retry must RESUME the credit half rather than refuse the whole return.
	sent := &atomic.Int64{}
	s := admin.NewStore(pool, fakeRefunder{sent: sent}, nil)
	if err := s.Decide(ctx, requestID.String(), "approved", "退貨完成", uuid.NullUUID{}); err != nil {
		t.Fatalf("the retry was refused (%v), so the credit half can never be paid "+
			"and the customer stays short", err)
	}
	if after := creditBalance(t, accountID); after <= before {
		t.Errorf("store credit went %d -> %d; the failed half was not resumed", before, after)
	}

	// And the card half is not sent TO STRIPE twice. The row count cannot say so
	// — the request key makes a repeat hit the same row, and settle_refund
	// returns early on 'succeeded', so the sum is 140000 whether or not the call
	// went out. Only the provider knows, which is what the counter stands for.
	if n := sent.Load(); n != 0 {
		t.Errorf("the retry sent %d refund(s) to the provider; the card half had "+
			"already landed and resuming means paying only what is MISSING", n)
	}
	if got := cardRefunded(t, orderNumber); got != 140000 {
		t.Errorf("the card half is now %d, want 140000 — the retry re-sent a refund "+
			"that had already landed", got)
	}

	// The order now holds a refund from BOTH sources, which is the only shape
	// that can tell the two definitions of "what has gone back" apart. The 折讓
	// form offers this figure and internal/invoice bounds an allowance by it,
	// and they were computed separately: card-only here, card + credit there.
	// So a split-refunded order defaulted the form to the card half and the
	// 統一發票 went on recording a sale that was reversed.
	// An invoicer, because the 折讓 figure is only filled when one is configured
	// — no provider, no form, no number to get wrong.
	withInvoices := admin.NewStore(pool, fakeRefunder{}, noDocuments{})
	view, viewErr := withInvoices.Order(ctx, orderNumber)
	if viewErr != nil {
		t.Fatalf("read the order: %v", viewErr)
	}
	credited := creditBalance(t, accountID) - before
	if credited <= 0 {
		t.Fatal("no credit was returned, so this proves nothing about the sum")
	}
	if want := int64(140000) + credited; view.RefundedCents != want {
		t.Errorf("the 折讓 form offers %d and %d has gone back (card %d + credit %d).\n"+
			"The form's figure and the bound an allowance is held to are one fact, "+
			"and a 折讓 short of what was refunded over-reports the sale to the 財政部",
			view.RefundedCents, want, 140000, credited)
	}
}

func cardRefunded(t *testing.T, orderNumber string) int64 {
	t.Helper()
	var cents int64
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce(sum(r.amount_cents), 0) FROM refunds r
		JOIN payments p ON p.id = r.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND r.status = 'succeeded'`,
		orderNumber).Scan(&cents); err != nil {
		t.Fatalf("read refunds: %v", err)
	}
	return cents
}

// TestADeliveredOrderCanStillShipWhatItOwes is the exit from a trap that had
// none. orders_legal_transition permits shipped -> delivered with a line still
// outstanding, deliberately — delivered says the parcels that WENT OUT have
// arrived, which is true whether or not more is to come — and
// orders_finished_when_shipped then refuses 'completed'. With no dispatch form
// at 'delivered' the order is wedged for ever, and the outstanding line's hold
// is stranded: release_reservation refuses a committed order, ExpiredReservations
// excludes it, and /admin/health counts neither.
//
// The dropdown offers 已送達 and 已完成 as peers with no hint that one is a
// one-way door, which is why this belongs in the code and not the operator's head.
func TestADeliveredOrderCanStillShipWhatItOwes(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, _, lines, _ := twoLineOrderWithStock(t, "wedged")

	// One parcel carrying part of the first line, then straight to delivered:
	// what went out has arrived, and the rest is still to come.
	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "黑貓宅急便", Tracking: "D1-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 1},
	}, actor); err != nil {
		t.Fatalf("first parcel: %v", err)
	}
	if _, err := s.Advance(ctx, number, "delivered", actor); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("read the order: %v", err)
	}
	if !view.CanShip {
		t.Fatal("a delivered order that still owes a parcel offers no dispatch form, " +
			"and completing it is refused — the order is wedged and its remaining " +
			"hold is stranded off the shelf where nothing counts it")
	}

	if err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "黑貓宅急便", Tracking: "D2-" + number,
	}, actor); err != nil {
		t.Fatalf("the second parcel of a delivered order was refused: %v", err)
	}
	if _, err := s.Advance(ctx, number, "completed", actor); err != nil {
		t.Errorf("finishing an order that owes nothing was refused: %v", err)
	}

	var held int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_reservations ir
		JOIN orders o ON o.id = ir.order_id
		WHERE o.order_number = $1 AND ir.state = 'held'`, number).Scan(&held); err != nil {
		t.Fatalf("read the holds: %v", err)
	}
	if held != 0 {
		t.Errorf("%d hold(s) still on the shelf after everything shipped", held)
	}
}

// noDocuments is an Invoicer that has filed nothing. The figure under test is
// what has been REFUNDED, which is a question about money and not about
// documents, so the documents are the part that can be empty.
type noDocuments struct{}

func (noDocuments) Documents(context.Context, string) ([]invoice.Document, error) {
	return nil, nil
}

func (noDocuments) Issue(context.Context, string) (invoice.Document, error) {
	return invoice.Document{}, invoice.ErrDisabled
}

func (noDocuments) Void(context.Context, string, string) error { return invoice.ErrDisabled }

func (noDocuments) Allowance(context.Context, string, int64) (invoice.Document, error) {
	return invoice.Document{}, invoice.ErrDisabled
}
