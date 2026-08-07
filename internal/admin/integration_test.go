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
	"github.com/koopa0/goen/internal/loyalty"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/site"
	"github.com/koopa0/goen/internal/ui/pages"
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

// TestStockMovesOnlyThroughTheLedger is the back office's central rule. An
// adjustment must leave a movement row with a reason and an actor, because the
// ledger is what an audit reads — a direct UPDATE would move the shelf and
// leave the ledger disagreeing with it.
//
// The database is what enforces this: admin holds no UPDATE on stock_quantity,
// so a direct write is refused rather than merely discouraged. That half is
// asserted in internal/db's privilege suite; this asserts the movement is
// actually written.
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

// TestAdjustmentIsIdempotent is what stops a double-click doubling a
// correction. inventory_movements has a unique index on the key, so the second
// write is refused — and the page's key is derived from the stock it rendered,
// so the same button pressed twice carries the same key.
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

// TestAdvanceRefusesAnUnfundedOrder is the guard that matters most in a back
// office: orders_funded_to_leave_pending stops an order shipping before it is
// paid for. The store does not re-derive that rule — it lets the database
// refuse and reports the refusal.
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
	// Cancelling from pending IS legal, which is the control: without it a
	// store that refused every transition would pass the check above.
	if _, err := s.Advance(ctx, number, "cancelled", uuid.NullUUID{}); err != nil {
		t.Errorf("cancelling a pending order was refused: %v", err)
	}
}

// TestAdvanceRefusesAnIllegalTransition covers the state machine itself.
func TestAdvanceRefusesAnIllegalTransition(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number := placeUnpaidOrder(t)

	// 'shipped' is refused by the store itself now, whatever the current state:
	// Ship is the only door, because dispatch also records a carrier and
	// settles stock. Kept as a case because it USED to be the schema refusing
	// it, and the reason changing is worth being explicit about.
	if _, err := s.Advance(ctx, number, "shipped", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
		t.Errorf("pending -> shipped gave %v, want ErrRefused", err)
	}
	// And a status the schema does not know at all.
	if _, err := s.Advance(ctx, number, "teleported", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
		t.Errorf("an unknown status gave %v, want ErrRefused", err)
	}
}

// placeUnpaidOrder writes an order in pending with no payment against it.
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

// TestRetiringTheLastDiscountedVariantIsRefused is the campaign guard reached
// through the path it was written for. sale_campaign_variant_still_valid only
// ever fires on an admin write, because store cannot touch product_variants at
// all — so this is the first time it is exercised end to end.
func TestRetiringTheLastDiscountedVariantIsRefused(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	// A product with exactly one discounted, active variant, featured in a
	// campaign.
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
	// Leave exactly one discounted variant on this product.
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

// pickingOrderHoldingStock writes a paid order in 'picking' that is holding one
// unit of a real variant — the state a dispatch actually starts from.
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
	// Funded, so orders_funded_to_leave_pending lets it into picking.
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

// TestShipDoesAllFourWritesOrNone proves dispatch is atomic.
//
// A shipment row without the status move is an order showing as picking with a
// tracking number; the status move without consuming the reservation leaves
// stock held against goods that have physically left, so a sweeper would later
// return it to the shelf and oversell; either without the event leaves no
// record of who dispatched it.
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

	// What was in the parcel. Without these lines the shipment records that
	// something went out but not what, and return_within_shipment's ceiling is
	// zero — so no return of this order would ever be possible.
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

// TestShipRollsEverythingBackWhenTheStatusMoveIsRefused proves a refused
// dispatch leaves no shipment row and no history entry behind.
//
// An order that is not picking cannot be dispatched, and the refusal must take
// the shipment row with it. A shipment against an order nobody picked is a
// tracking number for a parcel that does not exist.
func TestShipRollsEverythingBackWhenTheStatusMoveIsRefused(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number := placeUnpaidOrder(t) // still pending: picking -> shipped is the only legal path

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

// TestShipNeedsACarrierAndATracking. A blank tracking number is a dispatch
// nobody can follow, and the CHECK would refuse it — but refusing in Go turns
// a 500 into a message the staff member can act on.
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

// TestAdvanceCannotShip proves the status endpoint is not a second door to
// dispatch. Without this, a hand-written POST produces a shipped order with no
// carrier, no tracking and its stock still held.
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

// TestAdvanceRecordsWhoAndWhen. A status move with no history entry is a state
// change nobody can account for, and order_events is append-only so it can
// never be filled in afterwards.
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

// fakeRefunder stands in for Stripe.
//
// A hand-written fake, which rules/testing.md allows for exactly this case: a
// paid third-party API that cannot be run in a container. It implements the
// consumer-defined admin.Refunder, lives beside the tests that use it, and is
// asserted on OUTPUTS — what ends up in the refunds table — never on how many
// times it was called.
type fakeRefunder struct {
	// failIntent stops goen BEFORE it asks Stripe for anything, so the refund
	// is still outstanding whatever Stripe would have said.
	failIntent bool
	// refundErr is what the provider call returns, and its TYPE is the whole
	// point rather than its presence. A plain error is an ambiguous transport
	// failure — Stripe may well HAVE refunded and goen did not hear the answer
	// — and only a *stripe.Error carrying a decline type is Stripe saying no.
	// Writing both as a terminal 'failed' is what this fake used to be unable
	// to tell apart, because it only carried a bool.
	refundErr error
	// state is what Stripe says the refund IS when it accepts one. The ZERO
	// VALUE means succeeded, because almost every test in this file wants a
	// working provider and says so by writing fakeRefunder{}.
	state admin.RefundState
}

func (f fakeRefunder) PaymentIntentFor(_ context.Context, sessionID string) (string, error) {
	if f.failIntent {
		return "", errors.New("stripe is unreachable")
	}
	return "pi_for_" + sessionID, nil
}

func (f fakeRefunder) Refund(_ context.Context, intentID, requestKey string, _ int64) (string, admin.RefundState, error) {
	if f.refundErr != nil {
		return "", "", f.refundErr
	}
	state := f.state
	if state == "" {
		state = admin.RefundSucceeded
	}
	return "re_" + requestKey + "_" + intentID[:6], state, nil
}

// returnedOrder writes a shipped, paid order with an open return request for
// `qty` units of its only line, and returns the request id.
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
	// Paid in full: 2 x 100000, no shipping.
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

// couponedShippedOrder is a DISCOUNTED order with a delivery fee, shipped, with
// a return request open against `lines` of its two lines.
//
// The figures are the ones that make the two old defects visible and are chosen
// so every expected value below can be computed by hand: two lines at NT$500,
// a NT$500 coupon, NT$80 of delivery. order_amount_owed is therefore
// 100000 - 50000 + 8000 = 58000, and payments_capture_matches_order forces the
// capture to be exactly that.
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

// TestARefundIsWhatTheCustomerPaid holds the two halves of the refund figure
// that the raw line-price sum got wrong, in opposite directions.
//
// The old expression was sum(quantity * unit_price_cents) — the UNDISCOUNTED
// goods, and nothing else:
//
//   - It over-claimed on a partial return. payments_capture_matches_order forces
//     the capture to equal order_amount_owed, which is NET of the discount, so
//     returning one of two NT$500 lines on an order with a NT$500 coupon
//     refunded NT$500 for an item the customer paid NT$250 of. refunds_within_
//     capture was satisfied because the total still fitted underneath.
//   - It made a FULL return impossible. The same order claimed NT$1,000 against
//     NT$580 of capture, so Decide refused it outright: goods back at the shop
//     and no door in the product that could pay for them.
//   - And it never returned the delivery fee, which 消保法 §19 I requires on a
//     rescission — the customer bears 任何費用, meaning none.
//
// The full-return figure is the one worth reading: it comes out at exactly what
// was captured. That is not a coincidence to be computed from the code, it is
// the property — rescinding the whole contract returns everything paid under it.
func TestARefundIsWhatTheCustomerPaid(t *testing.T) {
	tests := []struct {
		name  string
		lines int
		want  int64
		why   string
	}{
		{
			name: "one of two lines, so a proportional share of the coupon", lines: 1,
			// 50000 goods less its half of the 50000 coupon.
			want: 25000,
			why:  "the customer paid NT$250 for this item after the coupon, not NT$500",
		},
		{
			name: "both lines, so the whole contract and the delivery fee with it", lines: 2,
			// 100000 goods - 50000 coupon + 8000 delivery, which is the capture.
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

// TestApprovingAReturnRefundsWhatTheORDERSays proves the refund figure comes
// from the order's own line prices.
//
// The request carries a quantity and nothing else. A refund amount that arrived
// on a form — or was derived from anything the customer controls — is the
// oldest hole there is, and this one pays out real money.
func TestApprovingAReturnRefundsWhatTheORDERSays(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, _ := returnedOrder(t, 1) // 1 of 2 units at 100000

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

// TestAFailedRefundLeavesARowToReconcile proves a provider failure still leaves
// a row that reconciliation can act on, in the state the failure justifies.
//
// The refund row is committed BEFORE Stripe is called. A row written only on
// success is a refund that may have succeeded at the provider and exists
// nowhere in goen — the one state nothing can reconcile.
//
// WHICH state is the half this test used to get wrong, and it was asserting the
// defect. Every error was written as a terminal 'failed', including a network
// timeout in which Stripe may well have refunded — so goen recorded "the money
// is still here" about money it could not see. 'failed' also drops the row out
// of refunds_guard's sum, which frees the same capture to be claimed again.
// UNKNOWN IS NOT FAILURE: only a decision from Stripe is terminal, and the
// decision is read off the SDK's typed error rather than its wording.
//
// This test says what each failure LEAVES. That a pending one can then be
// resumed is TestAStalledRefundCanBeRetriedToCompletion — the half that was
// missing, and the reason this test's own fixture could pass while the refund
// it left behind was unrecoverable.
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
			name: "the request timed out, so nobody knows what Stripe did",
			// A plain error, which is what a timeout, a reset connection and a
			// 502 from a proxy all are. Stripe may have refunded.
			refunder:   fakeRefunder{refundErr: errors.New("context deadline exceeded")},
			wantStatus: "pending",
			why: "goen did not hear an answer, and 'failed' would claim the money " +
				"is still at the shop AND free the capture to be claimed twice",
		},
		{
			name: "stripe refused the refund",
			// The typed error the SDK returns for a decision. This is the only
			// shape that may be written as terminal.
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
			ctx := t.Context()
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

			// The return did NOT close. A settled return with an unpaid
			// customer has no door back, because a return is decided once.
			var returnStatus string
			if err := pool.QueryRow(ctx,
				`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
				t.Fatalf("read return: %v", err)
			}
			if returnStatus != "requested" {
				t.Errorf("return is %q after a refund that did not happen, want requested",
					returnStatus)
			}
		})
	}
}

// TestAStalledRefundCanBeRetriedToCompletion is the half nobody had written,
// and without it the refund state machine was one-way.
//
// The whole design of two transactions around the provider is that a refund
// which stalls can be RESUMED: the row is committed first, request_key is
// goen's own idempotency key, and open_refund is idempotent on it. None of that
// was reachable. RefundedSoFar counted 'pending' refunds against the capture
// WITHOUT excluding this return's own row, so the second attempt read its own
// outstanding claim as somebody else's, computed zero headroom, and was refused
// by splitRefund with ErrRefused before Stripe was called at all.
//
// The order here is the whole capture on purpose — 200000 refundable against
// 200000 captured — because that is the fixture the old test already used and
// the case where the arithmetic has no slack to hide the defect in.
func TestAStalledRefundCanBeRetriedToCompletion(t *testing.T) {
	ctx, _ := staffContext(t)
	requestID, _ := returnedOrder(t, 2)

	stalled := admin.NewStore(pool, fakeRefunder{
		refundErr: errors.New("read tcp 1.2.3.4:443: i/o timeout"),
	}, nil)
	if err := stalled.Decide(ctx, requestID.String(), "approved", "已收到退貨", uuid.NullUUID{}); err == nil {
		t.Fatal("a refund that timed out was reported as success")
	}

	// Same return, same request key, a provider that answers this time.
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

// TestAPendingProviderRefundIsNotRecordedAsSucceeded proves goen records what
// the provider SAID.
//
// StripeRefunder.Refund used to discard the refund's status and return only its
// id, and the caller then wrote 'succeeded' with a succeeded_at stamp. Stripe
// refunds are genuinely 'pending' and 'requires_action' in real life, so goen
// asserted money had moved that had not — the inverse of mistake #16, which
// says unknown is NULL. 'requires_action' appeared in no Go file in this
// repository at all, while refunds_status_known had always allowed it.
//
// succeeded_at is asserted as well as the status because they are ONE fact:
// refunds_succeeded_has_time is a biconditional, so a stamp with no success is
// as impossible as a success with no stamp — and the stamp is what any later
// reconciliation would read as "the money left at this time".
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

			// The shop's decision stands: it took the goods back. What must not
			// happen is the ORDER's history telling the customer their money is
			// back, because order_events is rendered on their own order page.
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

// TestARefundStripeRefusedOutrightLeavesTheReturnOpen covers the other arm: a
// refund Stripe accepts the REQUEST for and answers 'failed'.
//
// It is not an error from the API — the call succeeded and the answer was no —
// so it would otherwise sail through as an accepted refund and close the
// return. A settled return with an unpaid customer has no door back, because a
// return is decided once.
func TestARefundStripeRefusedOutrightLeavesTheReturnOpen(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{state: admin.RefundFailed}, nil)
	requestID, _ := returnedOrder(t, 1)

	if err := s.Decide(ctx, requestID.String(), "approved", "已收到退貨", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
		t.Fatalf("approving with a failed refund gave %v, want ErrRefused", err)
	}

	var returnStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
		t.Fatalf("read return: %v", err)
	}
	if returnStatus != "requested" {
		t.Errorf("return is %q after a refund Stripe refused, want requested — "+
			"closing it strands a customer who sent goods back and was not paid",
			returnStatus)
	}
}

// TestTheHealthPageNamesARefundThatDidNotLand is the door this table never had.
//
// CLAUDE.md claimed the refund row is committed before the provider is called
// "so a crash between the two leaves something reconciliation can find". Half
// of that was false: NOTHING READ the row. The only query over `refunds` was
// the arithmetic in RefundedSoFar, so an outstanding claim on real money was in
// the database and on no page — the shape product_specs and promo_banners were
// each found in, and TestEveryTableIsRead passed it because a SELECT counts.
//
// It asserts the row is NAMED rather than counted, for the reason the stuck
// outbox list exists: "1 筆退款還沒退成功" tells an operator something is wrong
// and nothing about which customer.
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

	// Found by IDENTITY, never by position or count: this suite is shuffled and
	// other tests leave refunds of their own behind.
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

// TestRejectingAReturnMovesNoMoney proves a refusal writes no refund at all.
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

// TestAReturnIsDecidedOnce. A second decision on a settled return would refund
// again — the same money, twice.
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

// TestTheLoserOfTwoSimultaneousDecisionsWritesNoAuditRow closes the window
// between the check and the write.
//
// Decide reads the request on the POOL, before closeReturn opens its
// transaction, so two staff members clicking on one request both pass that
// check. DecideReturn carries `status = 'requested'` in its own WHERE clause —
// the only place the question is asked under a lock — but it was `:exec`, so
// the loser's UPDATE matched zero rows, SQL called that success, and the
// transaction committed an audit row asserting a decision nobody made. On the
// approval path that row lands after a refund has already been paid.
//
// Two goroutines and a start channel would not prove this: they finish
// microseconds apart and never overlap (CLAUDE.md #9). T1's transaction is held
// OPEN across T2's whole pre-check instead, which makes the interleaving the
// defect needs the one that actually happens.
func TestTheLoserOfTwoSimultaneousDecisionsWritesNoAuditRow(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, _ := returnedOrder(t, 1)

	before := auditRowsFor(t, requestID)

	// T1: a staff member's decision, in flight and holding the row.
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

	// T2: the second staff member. Its pre-check reads the COMMITTED row and
	// still sees 'requested' — that is the defect, and the reason the statement
	// has to be the authority. It then blocks on T1's lock.
	// t.Error, never t.Fatal, from a goroutine.
	decided := make(chan error, 1)
	go func() { decided <- s.Decide(ctx, requestID.String(), "approved", "", uuid.NullUUID{}) }()

	// Give T2 time to get past its pre-check and onto the lock. If it has not,
	// the test still passes for the right reason once T1 commits — this only
	// makes the interleaving the intended one.
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

	// The consequence, not just the return value: the trail must not record a
	// decision that did not happen.
	if after := auditRowsFor(t, requestID); after != before {
		t.Errorf("%d audit rows for this return, was %d — the losing decision was "+
			"recorded as though it had been made", after, before)
	}

	// And the decision that DID happen is T1's, unchanged.
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "rejected" {
		t.Errorf("the return is %q, want rejected — the loser overwrote the winner", status)
	}
}

// auditRowsFor counts the audit trail's entries for one return request.
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

// TestARefundCannotExceedWhatWasCaptured is the database's guard, reached
// through the back office. Two returns each claiming the full order would pay
// out twice what came in.
func TestARefundCannotExceedWhatWasCaptured(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, orderNumber := returnedOrder(t, 2) // the whole order: 200000

	if err := s.Decide(ctx, requestID.String(), "approved", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("first approval: %v", err)
	}

	// A second request against the same order, claiming the same units. The
	// return ceiling would refuse it first, so this one is written straight in
	// to reach the REFUND guard.
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
		// If the return ceiling let it through, the refund guard is next.
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

// TestGrantIsBoundedAndPositive proves the grant form refuses a fat-finger.
//
// The form gives money away. A staff member who means NT$500 and types 500000
// should meet a refusal, not a customer with a windfall — and a negative
// "grant" is a deduction wearing the wrong form's clothes.
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

	// Only the one legal grant landed.
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

// TestGrantIsIdempotent. A double-submitted form is one posting.
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

	// A DIFFERENT grant to the same customer is a second posting, not a repeat.
	// Without the amount and reason in the key this would be swallowed as a
	// duplicate, and a customer owed two separate apologies would get one.
	if _, err := s.GrantCredit(ctx, email, 30000, "另一次補償", uuid.NullUUID{}); err != nil {
		t.Fatalf("second, different grant: %v", err)
	}
	read()
	if entries != 2 || balance != 80000 {
		t.Errorf("%d entries totalling %d after a second, different grant, want 2 of 80000",
			entries, balance)
	}

	// Same amount, different reason: still two distinct grants.
	if _, err := s.GrantCredit(ctx, email, 30000, "第三次補償", uuid.NullUUID{}); err != nil {
		t.Fatalf("third grant: %v", err)
	}
	read()
	if entries != 3 || balance != 110000 {
		t.Errorf("%d entries totalling %d after a same-amount different-reason "+
			"grant, want 3 of 110000", entries, balance)
	}
}

// TestTheBackOfficeIsInvisibleToEveryoneButStaff proves both refusals are the
// same 404 an absent page gives.
//
// A signed-out visitor used to be redirected to /signin?next=/admin, which told
// them the back office was real — the exact disclosure the 404 for signed-in
// customers was chosen to avoid — and wrote the path into their history and
// referrer on the way there. Both refusals are the same now, and this holds
// them to it.
func TestTheBackOfficeIsInvisibleToEveryoneButStaff(t *testing.T) {
	ctx := t.Context()
	h := admin.NewHandler(admin.NewStore(pool, fakeRefunder{}, nil),
		media.NewHandler(media.NewStore(pool), slog.New(slog.DiscardHandler)),
		outbox.NewStore(pool, slog.New(slog.DiscardHandler)),
		newsletter.NewStore(pool),
		slog.New(slog.DiscardHandler),
		// nil: this case is about who can SEE the back office, and a second
		// factor gate in front of it would answer a different question.
		nil,
		// nil again, for the session closer: no Stripe key here, so no order can
		// have a checkout open.
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

	// The control: staff DO get through, or a guard that refused everyone would
	// pass every assertion above.
	var staffID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role) VALUES ('staff-'||gen_random_uuid()||'@example.com', 'admin')
		RETURNING id`).Scan(&staffID); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin", nil)
	req = req.WithContext(account.WithUser(req.Context(),
		account.User{ID: staffID.String(), Role: "admin"}))
	w := httptest.NewRecorder()
	guarded(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("staff got %d, want 200 — the guard refuses everyone", w.Code)
	}
}

// TestOnlyAnAdminReachesTheStaffPage is the half of the staff-promotion Critical
// that lives in the ROUTE, and it was the half with no test.
//
// The escalation had two doors and each needed closing. The one in the store —
// AddStaff refusing the actor's own address — was fixed and locked. This one is
// the gate: /admin/staff was behind RequireStaff, which accepts `staff` as well
// as `admin`, so any staff member could open the page that adds a colleague,
// revoke another admin, or strip somebody's second factor.
//
// The refusal is the same 404 a customer gets at /admin, for the same reason:
// telling somebody their colleagues' page exists but is not for them is worth
// more to a prober than the accuracy is to them.
//
// The CONTROL matters as much as the refusal here — RequireAdmin wraps
// RequireStaff, so a mistake in the wrapping could refuse everybody and every
// assertion above would still pass.
func TestOnlyAnAdminReachesTheStaffPage(t *testing.T) {
	ctx := t.Context()
	h := admin.NewHandler(admin.NewStore(pool, fakeRefunder{}, nil),
		media.NewHandler(media.NewStore(pool), slog.New(slog.DiscardHandler)),
		outbox.NewStore(pool, slog.New(slog.DiscardHandler)),
		newsletter.NewStore(pool),
		slog.New(slog.DiscardHandler),
		// nil, because this asks who may reach the page and not whether the
		// second factor was proved. RequireStaff's own gate covers that.
		nil,
		// nil for the session closer: no Stripe key here.
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
		// The one the route used to let through.
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

// staffContext is a request context carrying a signed-in staff member, which is
// what audited() reads the actor from.
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

// auditRows is what the trail holds for one action.
func auditRows(t *testing.T, action admin.Action) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM audit_events WHERE action = $1`, string(action)).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

// TestEveryBackOfficeWriteLeavesATrail proves no back-office write goes
// unattributed.
//
// The trail's whole value is that it is complete: a back-office action nobody
// recorded is one nobody can attribute afterwards, and an incomplete trail is
// worse than none because it reads as if it covered everything.
//
// Each case runs the real store method and asserts a row appeared. Driving the
// methods rather than checking that they CALL something is what makes this a
// test of the behaviour instead of the wiring.
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

// TestAnAuditRowNamesItsActorAndRequest proves a row carries the two facts
// nothing else records.
//
// The actor is the only thing audit_events adds over order_events,
// inventory_movements and the rest — every one of those already says WHAT
// happened to an entity. Without the actor there is no reason for this table.
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

// TestAnActionWithNoActorIsRefused proves an unattributable write does not
// happen.
//
// Not recorded anonymously, and not allowed through unaudited: RequireStaff
// means an absent actor is a wiring mistake, and the write it belongs to must
// stop rather than land with nobody's name on it.
func TestAnActionWithNoActorIsRefused(t *testing.T) {
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	slug := anyProductSlug(t)

	// Read BEFORE, and compared after. Asserting the status is not 'draft'
	// afterwards was the first version, and it broke the day another test's fixture
	// started creating draft products: anyProductSlug picked one, so the assertion
	// held before the write ever ran. A test that passes because of what it found
	// rather than what it did is trap #22 from CLAUDE.md.
	var before string
	if scanErr := pool.QueryRow(t.Context(),
		`SELECT status FROM products WHERE slug = $1`, slug).Scan(&before); scanErr != nil {
		t.Fatalf("read product: %v", scanErr)
	}
	target := "draft"
	if before == "draft" {
		target = "archived"
	}

	// No account.WithUser: a context that never went through RequireStaff.
	//
	// Bound to ErrNoActor, not to "an error happened". The foreign key on
	// actor_user_id also refuses a zero uuid, so a test that only asked whether
	// something failed stayed green with this whole check deleted — which the
	// mutation run found.
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

// TestAFailedWriteLeavesNoAuditRow proves the trail describes only work that
// committed.
//
// An audit row for work that rolled back is a lie, and the trail is only worth
// reading if every row describes something that actually happened. One
// transaction is what makes that true rather than aspirational.
func TestAFailedWriteLeavesNoAuditRow(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	before := auditRows(t, admin.ActionPublishProduct)
	// A status the store refuses outright, and one the database refuses: both
	// must leave the trail untouched.
	if err := s.SetProductStatus(ctx, anyProductSlug(t), "nonsense"); err == nil {
		t.Fatal("an invalid status was accepted")
	}
	// This is what the first version of the trail got wrong. An UPDATE matching
	// no rows is not an error in SQL, so publishing a slug that does not exist
	// reported success and left an audit row saying a product had been
	// published — a false statement about a product that never existed.
	if err := s.SetProductStatus(ctx, "no-such-product-"+uuid.NewString(), "active"); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("publishing an absent product gave %v, want ErrNotFound", err)
	}
	if after := auditRows(t, admin.ActionPublishProduct); after != before {
		t.Errorf("%d audit rows after a refused write, want %d", after, before)
	}
}

// TestTheTrailCannotBeRewritten proves the person being audited cannot edit
// the record.
//
// Append-only is enforced by forbid_change and by admin holding no INSERT — two
// locks, because the value of an audit trail is precisely that the person being
// audited cannot edit it.
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
			// As the OWNER, which is the strongest caller there is. If the owner
			// cannot do it, nobody can.
			if _, err := pool.Exec(ctx, tt.stmt); err == nil {
				t.Fatalf("%s succeeded against an append-only table", tt.name)
			} else if _, name := constraintFrom(err); name != "audit_events_append_only" {
				t.Errorf("refused by %q, want audit_events_append_only: %v", name, err)
			}
		})
	}

	// And the admin role cannot write one directly, only through the function.
	// Asserted as `admin` rather than as the owner: the owner can, and a check
	// that ran as the owner would prove the opposite of what it claims.
	if err := asAdmin(ctx, t, `INSERT INTO audit_events (action, entity_table)
		VALUES ('forged', 'x')`); err == nil {
		t.Error("admin inserted an audit row directly, bypassing record_audit_event")
	}

	// The control: the same role CAN write one through the door it is meant to
	// use. Without this the case above would also pass if the grant were simply
	// missing, and a trail nothing can write is not append-only — it is broken.
	if err := asAdmin(ctx, t, `SELECT record_audit_event(
		(SELECT id FROM users LIMIT 1), 'test.control', 'products', NULL)`); err != nil {
		t.Errorf("admin cannot call record_audit_event: %v", err)
	}
}

// constraintFrom pulls the constraint out of a pg error.
func constraintFrom(err error) (code, name string) {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return "", ""
	}
	return pgErr.Code, pgErr.ConstraintName
}

// anyVariantSKU and anyProductSlug are fixtures the cases above share.
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

// asAdmin runs one statement with the back office's own database role.
//
// The suite otherwise connects as the owner, who is subject to no REVOKE at
// all — so a privilege assertion made on the ordinary pool proves nothing. This
// takes a connection, assumes the role, and puts it back.
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
	// RESET before release, or the pooled connection hands the admin role to
	// whatever runs next.
	defer func() {
		if _, resetErr := conn.Exec(ctx, `RESET ROLE`); resetErr != nil {
			t.Errorf("reset role: %v", resetErr)
		}
	}()

	_, execErr := conn.Exec(ctx, stmt)
	return execErr
}

// TestANamedParentThatDoesNotExistIsRefused proves a category is never quietly
// created somewhere else.
//
// The first version resolved the parent with a scalar subquery, which yields
// NULL for a slug that is not there — so naming a missing parent created a ROOT
// category and reported success. A staff member asked for one thing and
// silently got another, which is worse than a refusal because nothing tells
// them to look.
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

// TestACategoryIsCreatedUnderTheParentItNames proves the tree is built where
// asked.
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

// TestSomethingInUseCannotBeDeleted proves nothing is orphaned by a deletion.
//
// The emptiness check is the DELETE's own WHERE clause, not a count read first:
// a product created between the two would be orphaned. The foreign keys are ON
// DELETE RESTRICT and would refuse it anyway — this makes the refusal a row
// count, so the page can say "move the products first" instead of showing a
// constraint name.
func TestSomethingInUseCannotBeDeleted(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	// A brand nothing uses goes.
	empty := "tax-brand-" + uuid.NewString()[:8]
	if errs, err := s.CreateBrand(ctx, &admin.TaxonomyForm{Slug: empty, Name: "空品牌"}); err != nil || len(errs) > 0 {
		t.Fatalf("create: err=%v errs=%v", err, errs)
	}
	if err := s.Delete(ctx, "brand", empty); err != nil {
		t.Errorf("an unused brand was not deleted: %v", err)
	}

	// One with a product does not.
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

	// And a category with children does not, even with no products of its own.
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
	// The child goes, and then the parent can.
	if err := s.Delete(ctx, "category", child); err != nil {
		t.Fatalf("delete child: %v", err)
	}
	if err := s.Delete(ctx, "category", parent); err != nil {
		t.Errorf("the parent was still refused once emptied: %v", err)
	}
}

// TestASlugIsNeverRenamed proves every indexed URL survives a rename.
//
// A slug is in every URL a search engine has indexed and every link anybody has
// sent. goen has no redirect table, so changing one breaks all of them
// silently — Rename touches the display name and nothing else.
func TestASlugIsNeverRenamed(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	slug := "tax-rename-" + uuid.NewString()[:8]
	if errs, err := s.CreateBrand(ctx, &admin.TaxonomyForm{Slug: slug, Name: "原名"}); err != nil || len(errs) > 0 {
		t.Fatalf("create: err=%v errs=%v", err, errs)
	}
	if err := s.Rename(ctx, "brand", slug, "新名字", ""); err != nil {
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
	// A blank name is refused rather than stored: brands_name_present would
	// refuse it too, and this is what turns that into a sentence.
	if err := s.Rename(ctx, "brand", slug, "   ", ""); !errors.Is(err, admin.ErrInvalid) {
		t.Errorf("a blank name gave %v, want ErrInvalid", err)
	}
}

// TestRevenueCountsOnlyCommittedOrders proves an abandoned checkout is not a
// sale.
//
// An order that was placed and never paid is not revenue. Counting it would
// make every abandoned checkout look like a sale, which is the one thing a
// revenue figure must not do.
func TestRevenueCountsOnlyCommittedOrders(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	before, err := s.Report(ctx, 30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	// One order placed and left unpaid.
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

	// The same order paid for.
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

// TestTheWindowIsAnAllowlist proves a URL cannot ask for arbitrary work.
//
// The window reaches a query that scans order history, so an arbitrary number
// from a URL is an arbitrary amount of work anybody can ask for.
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

// reportOrder places one order for `cents` and optionally pays for it.
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

// TestCommittedCoversAnOrderWithNoPaymentRow proves the single definition of
// committed has both halves.
//
// The failure CLAUDE.md records three times: a fully store-credited or
// fully-discounted order legally leaves pending with NO payment row at all, and
// three guards that read "EXISTS a succeeded payment" as "committed" did
// nothing for those orders.
//
// committed_orders is now the single definition of the test, so it is the one
// place that has to get both halves right — and the half that is easy to lose
// is the second, because every order anybody writes by hand in a test has a
// payment.
func TestCommittedCoversAnOrderWithNoPaymentRow(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	before, err := s.Report(ctx, 30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	// A ZERO-OWED order: one line at zero, which is what a fully discounted or
	// fully store-credited order comes to. orders_funded_to_leave_pending
	// refuses to advance an order that still owes money — correctly — so this
	// is the only shape that reaches the state the case is about.
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
	// Revenue does not move: the order is committed and worth nothing, which is
	// exactly right and is why "committed" and "revenue" are different
	// questions.
	if after.RevenueCents != before.RevenueCents {
		t.Errorf("a zero-owed order moved revenue from %d to %d",
			before.RevenueCents, after.RevenueCents)
	}
}

// TestTheQueuePutsWhatTheShopOwesFirst proves the list is ordered by what is
// owed and how long.
//
// Oldest first, unlike every other back-office list — a question waiting three
// days is more urgent than one asked this morning, and newest-first would bury
// it exactly as it becomes worth answering.
//
// And "answered" means the SHOP answered. Three customer replies and no
// official one is still an unanswered question: the person deciding came for
// the shop's answer.
func TestTheQueuePutsWhatTheShopOwesFirst(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	ps := product.NewStore(pool)
	asker := newAskingCustomer(t)
	slug := anyActiveProductSlug(t)

	// Oldest, answered by the shop. Then one only a customer replied to. Then
	// the newest, untouched.
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
	// The two the shop owes come first, oldest of those first; the answered one
	// sinks to the bottom.
	if order[0] != "中間的,只有顧客回" {
		t.Errorf("first is %q, want the oldest one the shop still owes", order[0])
	}
	if order[2] != "最舊的,店家已回" {
		t.Errorf("last is %q, want the one the shop already answered", order[2])
	}
}

// TestHidingAQuestionIsRecordedAndCannotBeRepeated proves the audit row
// describes work that happened.
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
	// Hiding an already-hidden question changes nothing and says so, rather
	// than writing a second audit row for work that did not happen.
	if err := s.HideQuestion(ctx, id); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("hiding twice gave %v, want ErrNotFound", err)
	}
	if after := auditRows(t, admin.ActionHideQuestion); after != before+1 {
		t.Errorf("a refused hide wrote an audit row")
	}
}

// TestAnEmptyOfficialAnswerIsRefused proves the shop cannot publish nothing.
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

// ask posts a question and back-dates it by `days`, returning its id.
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

// TestHealthIsDerivedFromTheWorkNotFromAHeartbeat proves the signals cannot say
// "fine" while the work is not being done.
//
// Every figure comes from the work itself — undelivered messages, unreleased
// holds, the projection's age — rather than from something the workers write to
// say they are alive. A heartbeat says "I am running"; these say "the work is
// being done", and a worker looping without progress passes the first and fails
// the second.
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
		t.Errorf("an empty outbox reads as unhealthy: %s", clean.OutboxText())
	}

	// A message that has exhausted its attempts never resolves on its own —
	// the worker has given up — so one is worth a person's time.
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

// TestAMessageWaitingOnItsBackoffIsNotLate proves a legitimate retry is not a
// backlog.
//
// Overdue is measured from available_at — when a message became DUE — not from
// when it was written. The claim pushes available_at forward by a lease and the
// backoff pushes it further, so a message legitimately waiting is in the future
// and must not read as a backlog.
func TestAMessageWaitingOnItsBackoffIsNotLate(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	if _, err := pool.Exec(ctx, `DELETE FROM outbox_messages`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	// Undelivered, one attempt, and not due for another hour.
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
		t.Errorf("a message waiting on its backoff reads as unhealthy: %s", view.OutboxText())
	}
}

// TestNeverRebuiltIsNotTheSameAsJustRebuilt proves the one state meaning "never
// ran" does not read as the healthiest.
//
// max() over an empty table is NULL. Collapsed into a zero age it would read as
// "rebuilt this instant" — the healthiest possible answer for the one state
// that means the worker has never run. It would also have been a scan error on
// exactly one deployment: a fresh one.
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

	// One row, rebuilt now.
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
		t.Errorf("a just-rebuilt projection reads as %s", fresh.RecommendText())
	}
}

// TestTheStuckThresholdMatchesTheWorkers used to sit here, keeping
// admin.OutboxMaxAttempts equal to outbox.MaxAttempts. The constant is gone:
// the health page reads the outbox's own list now, so it imports the package
// and uses the real number. Two constants that must agree are two constants
// that can disagree, and the fix was to have one.

// TestCancellingAnOrderInTheBackOfficeReturnsItsStock holds the staff-side
// cancellation to the same promise as the customer's.
//
// Advance used to set the status and stop. The units stayed off the shelf until
// cart.SweepForever noticed, which is up to HoldTTL later — and the sweeper is
// what a customer's abandoned checkout gets, not what a deliberate cancellation
// deserves.
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
	// The audit trail reads the actor from the CONTEXT, not from the parameter
	// — record_audit_event writes in the caller's transaction and refuses a row
	// attributed to nobody.
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

// placeHeldOrder writes an unpaid order holding one unit of vid.
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

// TestShippingEnqueuesTheDispatchNotice holds that a dispatch reaches the
// person waiting for it.
//
// order.shipped was a topic constant with no producer: the tracking number went
// into order_shipments and sat there for anyone who thought to look. Shipping is
// the moment the customer is waiting on.
func TestShippingEnqueuesTheDispatchNotice(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	// The CUSTOMER placed this order in English. The staff member pressing Ship
	// reads Chinese, and the notice must not follow them: an English customer
	// told about their parcel in the shopkeeper's language is the mistake
	// orders.locale exists to make impossible. Set at PLACEMENT, because
	// orders_freeze_money freezes the locale once the order settles.
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

	var payload []byte
	if err := pool.QueryRow(ctx,
		`SELECT payload FROM outbox_messages WHERE topic = 'order.shipped' AND dedupe_key = $1`,
		"903-2214-0001").Scan(&payload); err != nil {
		t.Fatalf("no dispatch notice was enqueued for %s: %v", number, err)
	}
	var got admin.OrderShipped
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode notice: %v", err)
	}
	// The tracking number is IN the message. It is what the customer takes to
	// the carrier's own site, and a mail that only says "it shipped" makes them
	// come back and find it.
	want := admin.OrderShipped{
		OrderNumber: number, Email: "ship@example.com", Name: "收件人",
		Carrier: "黑貓宅急便", Tracking: "903-2214-0001",
		// Off the ORDER, never off the staff member who pressed the button.
		Locale: "en",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("dispatch notice (-want +got):\n%s", diff)
	}
}

// TestRestockingTellsEverybodyWhoAsked holds the restock notice end to end.
//
// stock_notifications collected addresses from the day the PDP offered the
// form, and notified_at was never set by any production code — every one of
// those customers was waiting for an email nothing would ever send.
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

	// A second adjustment tells nobody again: the claim spent them.
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

// TestAnAdjustmentBelowTheThresholdTellsNobody. "In stock" is
// stock_quantity > safety_stock, and telling somebody about a unit the shop
// will not sell them is worse than staying quiet.
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
	// A safety stock the adjustment will not clear.
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

	// Three units against a safety stock of five: still not sellable.
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

	// The control: clearing the threshold does tell them.
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

// emptyTheShelf takes a variant down to zero, which is the state somebody asks
// for a restock notice from.
//
// It reads the quantity first because inventory_movements_delta_non_zero
// refuses a movement of nothing — and with -shuffle=on another test may have
// emptied the same variant already. A fixture that assumes it runs first is a
// fixture that fails on a reordering rather than on a defect.
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

// shippableOrder writes a funded order sitting in picking, holding its stock.
//
// It used to write a line with no variant and take no hold at all, which made it
// a fixture for an order the application cannot produce: every line takes a hold
// in PlaceOrder's own transaction. Ship now refuses an order holding nothing —
// shipping one posts no inventory movement, so the parcel leaves and the shelf
// count does not — and this fixture was the first thing that refusal caught.
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

// TestAnOverClaimIsRefusedInWordsRatherThanByAConstraint holds what a staff
// member is told when a refund will not fit.
//
// refunds_within_capture refuses an over-claim either way, and what reached the
// staff member was its name. RefundedSoFar was written so the back office could
// see what was LEFT instead — and it had no caller, so nobody ever did.
//
// Reached through a refund that did not come from a return: a goodwill payment
// back, after which a full return no longer fits. Going through a second
// return instead does not reach this guard at all — return_within_shipment
// refuses the claim first, which is the ceiling working and a different rule.
// The first version of this test took that route and SKIPPED.
func TestAnOverClaimIsRefusedInWordsRatherThanByAConstraint(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, orderNumber := returnedOrder(t, 2) // 200000 captured, 200000 claimed

	var paymentID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT p.id FROM payments p JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND p.status = 'succeeded'`,
		orderNumber).Scan(&paymentID); err != nil {
		t.Fatalf("find payment: %v", err)
	}
	// 150,000 already paid back for some other reason. 50,000 remains.
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
	// The numbers, not a constraint name. "只剩 50000 可退" is what a person
	// can act on; "refunds_within_capture" is what they then have to ask about.
	for _, want := range []string{"已退", "只剩", "150000", "50000"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "refunds_within_capture") {
		t.Errorf("the constraint reached the staff member: %v", err)
	}

	// Nothing was written. The guard runs before OpenRefund, so a refused
	// approval must leave the return decidable — a staff member who reduces
	// the claim can still approve it.
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read the return: %v", err)
	}
	if status != "requested" {
		t.Errorf("a refused approval left the return %s", status)
	}
}

// TestTheReturnQueueShowsWhatIsComingBack. The page said "3 件 · 可退 NT$4,500"
// and nothing else, so a staff member approved a return without being able to
// see what was in it. ReturnLines existed for exactly this and had no caller.
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

// TestPublishingAVersionCarriesItsZoneSurcharges holds that changing a base
// fee is not a statement about zones.
//
// The surcharges key on the VERSION, so a newly published one starts with
// none. A shop that raised 宅配 from NT$80 to NT$100 would then have shipped to
// 金門 for NT$100 — under-charging exactly where it was already losing money,
// and silently, because nothing on the form mentions zones.
func TestPublishingAVersionCarriesItsZoneSurcharges(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	var methodID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_methods WHERE code = 'home_delivery'`).Scan(&methodID); err != nil {
		t.Fatalf("find the method: %v", err)
	}
	// Its OWN surcharge rather than the seed's: another test clears the one on
	// the current version, so depending on it made this pass in file order and
	// fail under -shuffle. The guard below caught that rather than letting the
	// test pass with nothing to carry.
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
	// And the base fee is the new one, without which this test would pass on a
	// publish that did nothing at all.
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

// TestAZeroSurchargeClearsTheRowRatherThanStoringZero. Absence is what "no
// surcharge" means — the lookup coalesces a missing row to nothing — so a row
// saying zero would be a second way to write one state, and the CHECK refuses
// it anyway.
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

// TestTheTierWindowMatchesTheProgramme proves the back office counts a
// customer's spend over the window internal/loyalty publishes.
func TestTheTierWindowMatchesTheProgramme(t *testing.T) {
	if got, want := admin.MembershipWindowDays, loyalty.Days(loyalty.MembershipWindow); got != want {
		t.Errorf("the back office reads a %d-day window and the programme says %d", got, want)
	}
}

// TestTwoTiersCannotShareAThreshold. "Which tier is NT$50,000 in" must have one
// answer, and the index is what makes it so — the form reaches it as a refusal
// the page can show rather than as two bands that disagree.
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

// TestATierCannotEarnLessThanNoTier. A band earning fewer points than the base
// rate would be a punishment for spending more, which is refused by the schema
// and by the form's own bounds.
func TestATierCannotEarnLessThanNoTier(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	if err := s.CreateTier(ctx, "worse", "倒扣", "", 80000, 90); !errors.Is(err, admin.ErrInvalid) {
		t.Errorf("a band below the base rate answered %v, want ErrInvalid", err)
	}
	// The control: the same band at the base rate is accepted, without which a
	// CreateTier that refused everything would pass the check above.
	if err := s.CreateTier(ctx, "worse", "基本", "Base", 80000, 100); err != nil {
		t.Errorf("a band at the base rate was refused: %v", err)
	}
}

// TestOneUploadCanBeAttachedToTwoProducts holds what the composite key was
// shaped for.
//
// product_images_storage_key_key is (product_id, storage_key) rather than
// storage_key alone, precisely so a generic accessory shot can sit on two
// products — CLAUDE.md records the day it was the other way round and a shared
// image was a duplicate-key error. Until the picker existed, though, nothing
// could reach that: the only way to attach an image was to upload a file, so
// the staff member re-uploaded the same shot for every product. Content
// addressing deduplicated the bytes and not the work.
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
	// And once on each: attaching it twice to the SAME product is the duplicate
	// the index is actually for.
	if err := s.AttachImage(ctx, first, digest, "再一次", "", 800, 600); err == nil {
		t.Error("the same image was attached to one product twice")
	}
}

// twoProducts returns the slugs of two draft products of this test's own.
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

// TestADeliveryAddressCanBeCorrectedUntilItShips holds the window a correction
// is allowed in.
//
// A customer who typed the wrong street had no way to fix it and neither did
// the shop: the only option was cancel and re-order, which loses the payment
// and the stock hold with it.
//
// The cut-off is dispatch, and it is in the UPDATE's own WHERE clause. Once the
// parcel has left, rewriting the recorded address makes the record lie about
// where it went — which is worse than not being able to change it.
func TestADeliveryAddressCanBeCorrectedUntilItShips(t *testing.T) {
	ctx, staffID := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number := shippableOrder(t, "zh-Hant") // funded, sitting in picking

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

	// Ship it, and the same correction is refused.
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

// TestCorrectingADeliveryDoesNotWriteTheAddressIntoTheAuditTrail holds what the
// trail may record about a correction.
//
// audit_events is append-only and erase_user does not reach it, so a customer's
// address written there would outlive the erasure meant to remove it — the same
// reason staff_note is documented as no place for anything a customer wrote.
// The row records that the delivery changed, and which destination it is.
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

// TestCorrectingAPickupOrderCannotTurnItIntoAnAddressOne holds that the order
// decides its own destination.
//
// Which half applies is decided from the ORDER's shipping method, never from
// the form: a submission carrying a street address for a 超商取貨 order would
// otherwise reach order_private_data_one_destination as a constraint violation
// instead of being resolved before the write.
func TestCorrectingAPickupOrderCannotTurnItIntoAnAddressOne(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number := pickupOrderForCorrection(t)

	// Both halves submitted, as a hand-edited form would.
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

// pickupOrderForCorrection writes an unshipped 超商取貨 order.
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

// TestHidingAReviewIsReversibleAndAudited holds that a moderation decision can
// be taken back and that the trail records both halves.
//
// Moderation is a judgement, and a judgement made in a hurry is one somebody
// should be able to undo. Hide and show are separate paths rather than a toggle,
// so a double-submitted form cannot un-hide what a staff member just hid.
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
	// Hiding it again is the double-click, and it must not read as success that
	// changed something.
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

// TestTheReviewQueueShowsHiddenOnes. Un-hiding is not possible from a list that
// cannot display what is hidden.
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

// TestTheInboxPutsTheLongestWaitFirst holds the order the queue is worked in.
//
// contact_messages has been written by /contact since the site launched, and the
// dashboard has counted the unhandled ones — with no page to open them. A
// customer wrote in and the shop saw a number.
//
// Oldest first for the reason /admin/questions is: somebody who wrote three days
// ago is more urgent than somebody who wrote this morning, and newest-first
// buries them exactly as they stop being answerable in time.
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

	// Unhandled ahead of handled, and within those the longest wait first — so
	// the six-day-old HANDLED one must not lead.
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
			if !r.Handled || r.Waiting() != "已處理" {
				t.Errorf("a handled message reads as %q", r.Waiting())
			}
		}
	}
}

// TestHandlingAMessageIsReversibleAndAudited. Marking something handled by
// mistake is the ordinary kind of mistake, and a queue you cannot correct is one
// people stop trusting.
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
	// The double-click must not read as a change that happened.
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

// TestTheInboxDoesNotCopyTheMessageIntoTheAuditTrail. It is a customer's own
// words, and audit_events is append-only where contact_messages is not.
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

// messageAgedDays writes a message that has been waiting days days.
//
// An HOUR past the boundary, not exactly on it. A row aged exactly four days sits
// on the edge of a whole-day figure, and the age was being computed by subtracting
// Go's time.Now() from a timestamp the DATABASE wrote — so a container a few
// milliseconds ahead of its host reported three days, and one behind reported four.
// That defect is fixed (the arithmetic is in SQL now, one clock), but the test
// cannot LOCK the fix: neither implementation is distinguishable from the other
// except by skew nobody controls, so the boundary is moved off the edge and the
// clock choice is recorded here rather than dressed up as covered.
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

// TestAReturnPaysBackBothSources is the bug this split exists for.
//
// An order can be funded from two places at once — store credit plus a card — and
// paying one back is two acts. Before this, the refundable amount (the line
// prices) was compared to what the CARD captured, so a part-credit order claimed
// more than its capture and was refused outright. The customer sent the goods back
// and could not be paid at all.
//
// Card FIRST, credit last: card money is the customer's own, store credit is a
// claim on this shop, and returning real money first is what somebody expects.
func TestAReturnPaysBackBothSources(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	// 200,000 of goods: 60,000 paid with credit, 140,000 on the card.
	requestID, orderNumber, accountID := creditFundedReturn(t, 2, 60000)

	if err := s.Decide(ctx, requestID.String(), "approved", "退貨完成", uuid.NullUUID{}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	// The card got its whole capture back...
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

	// ...and the credit came back to the ledger as a POSITIVE entry on the order,
	// not as a reversal: this order shipped, and reversing its spend would say it
	// was never funded.
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
	// Which means the customer is whole: what they started with.
	// They were granted 60,000, spent all of it on the order, and the return gave
	// it back: whole again.
	if got := creditBalanceOf(t, accountID); got != 60000 {
		t.Errorf("balance = %d, want 60000 — the credit they spent came back", got)
	}
}

// TestAPartialReturnPaysTheCardFirst is where the commercial decision lives.
//
// A FULL return pays the same either way — the claim covers both sources — so it
// cannot tell card-first from credit-first. Only a partial one can, and the first
// version of this suite did not have one: reversing the order in splitRefund left
// every test green.
//
// Card money is the customer's own; store credit is a claim on this shop. Giving
// the real money back first is what somebody expects, and it is the more generous
// reading when only half the order comes back.
func TestAPartialReturnPaysTheCardFirst(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	// 200,000 of goods: 60,000 credit, 140,000 card. One of two units comes back,
	// so the claim is 100,000 — less than the card alone.
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
	// And nothing came back as credit: the card covered it.
	if got := creditBalanceOf(t, accountID); got != 0 {
		t.Errorf("balance = %d, want 0 — the card paid the whole claim, so none of "+
			"the credit was needed", got)
	}
}

// TestAWhollyCreditFundedReturnNeedsNoProvider proves the zero-owed path.
//
// An order paid entirely with store credit has NO payment row — that is what
// order_is_committed exists for — so the old code refused it with "the order has
// no captured payment" and there was no way to pay the customer back.
func TestAWhollyCreditFundedReturnNeedsNoProvider(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	// 200,000 of goods, all of it credit.
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

// TestCompensatingAReturnTwiceGivesCreditOnce proves the idempotency key.
//
// Keyed on the RETURN, so a retried decision — the ordinary shape of a
// double-click or a failed-then-retried approval — compensates once. Paying a
// customer twice is the failure that is hard to notice and impossible to undo.
func TestCompensatingAReturnTwiceGivesCreditOnce(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	requestID, _, accountID := creditFundedReturn(t, 2, 200000)

	if err := s.Decide(ctx, requestID.String(), "approved", "第一次", uuid.NullUUID{}); err != nil {
		t.Fatalf("first Decide: %v", err)
	}
	// A second decision is refused because the return is no longer 'requested' —
	// so drive the compensation itself, which is the statement a retry would reach.
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

// creditBalanceOf is what the ledger sums to for one account.
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

// creditFundedReturn is returnedOrder with part of the price paid from store
// credit: an owner with a balance, a spend against the order, and a card payment
// for whatever is left.
//
// creditCents == the whole price is the zero-owed case, and it deliberately opens
// NO payment — that is the state order_is_committed exists for, and the one the
// old refund path refused outright.
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
	// They start with the credit they are about to spend, so a balance of exactly
	// that after the return means they were made whole.
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
	// The spend, while the order is still an open unpaid checkout — which is the
	// only moment store_credit_guard allows it.
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
	// Out of pending, through the real transitions. It matters: a compensation is
	// legal only once the order is settled, and while it is still an open unpaid
	// checkout the right move is to reverse the spend instead. Returning goods that
	// shipped is the state this whole path is about anyway.
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

// TestTheBackOfficeCanFindAnOrder is a gap a shop hits every day.
//
// /admin/orders filtered by STATUS and took the newest fifty. A staff member on
// the phone to a customer could reach an order only by typing a URL they already
// knew, and by the customer's name or address not at all.
//
// One box, and what it matches depends on what it looks like: a number is exact,
// anything else is a prefix of the name or the address. Each path is index-backed,
// which is why they are told apart rather than OR-ed with leading wildcards.
func TestTheBackOfficeCanFindAnOrder(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number, recipient, addr := searchableOrder(t)

	for _, term := range []string{
		number,                  // the exact number
		strings.ToLower(number), // as somebody types it
		// Sliced by RUNE. The first draft used recipient[:2] and cut a Chinese
		// character in half, which PostgreSQL refused as invalid UTF-8 — bytes are
		// not characters, and that is the same reason MinSearchRunes counts runes.
		string([]rune(recipient)[:2]),             // a surname
		string([]rune(addr)[:8]),                  // the start of an address
		strings.ToUpper(string([]rune(addr)[:8])), // in the other case
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

// TestASearchIgnoresTheStatusFilter proves the tab does not hide the answer.
//
// Somebody on the phone wants THAT order, not that order if it happens to be in
// the tab they had open — and the number they were given is unique.
func TestASearchIgnoresTheStatusFilter(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	number, _, _ := searchableOrder(t)

	// The order is pending; the filter says shipped.
	view, err := s.Orders(ctx, "shipped", number)
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	if !hasOrder(view, number) {
		t.Error("a search was filtered away by the status tab")
	}
}

// TestATooShortSearchIsNotASearch proves the floor.
//
// Below two characters a prefix matches most of the table — a list rather than an
// answer, produced by scanning order history. The page falls back to the ordinary
// queue rather than pretending to have searched.
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

// TestAnErasedOrderIsNotFoundByItsOldAddress proves erasure reaches this door too.
//
// erase_user sets the name and the address to NULL, so both comparisons are NULL
// and nothing extra is needed — but a search added later is exactly the kind of
// new door that has to be checked against it.
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
	// Its NUMBER still finds it: the order survives as a financial record, and the
	// number is not personal data.
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

// searchableOrder places a pending order with a distinctive recipient and address.
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

// TestTheBackOfficeCanSeeOneCustomerWhole is the page that did not exist.
//
// Credit was granted on one page, orders listed on another, points and tier nowhere
// the shop could see. Answering "what is going on with this customer" meant three
// pages and a guess.
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

// TestLookingAtACustomerIsRecorded is the unusual decision, held to.
//
// audit_events is otherwise a record of WRITES. This read is in it because the
// customer page is the one whose entire content is somebody else's personal data,
// and a trail is the only thing that distinguishes looking because you are dealing
// with them from looking out of curiosity.
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

	// And the row names WHO was looked at, never what was read: audit_events is
	// append-only and erase_user does not reach it, so an address copied there
	// would outlive the erasure meant to remove it.
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

// TestACustomerSearchNeedsATerm proves nothing is listed until somebody asks.
//
// A customer list is a page of addresses, and a back office that opens on one
// invites reading it. The shop looks somebody up because they are dealing with them.
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

// TestACustomerIsFoundByTheStartOfTheirAddress is the search itself.
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

// creditedAccount makes a customer with a store-credit balance and returns their
// user id.
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
	// post_store_credit creates the account on first use. A zero grant would be
	// refused by store_credit_entries_amount_non_zero, so an account with no
	// balance is made directly — which is also the ordinary state of most accounts.
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

// TestACustomersSpendCountsOnlyCommittedOrders is the figure a shop reads to
// decide how it treats somebody, so it must not count an order that was called
// off.
//
// CLAUDE.md records this exact defect four ways over: committed_orders was split
// out of settled_orders because a cancelled order read as committed, and counted
// as revenue, as a best seller, and as a purchase earning 已購買. The customer page
// is a fifth place the same mistake fits, and "what has this person spent with us"
// is the figure it would be most embarrassing to overstate to their face.
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
	// Both orders are still THEIR orders, and the count says so. The page lists
	// what happened; only the money is filtered.
	if view.Orders != 2 {
		t.Errorf("order count is %d, want 2", view.Orders)
	}
}

// TestAPromotedCustomerIsStillFindable holds the reason neither customer query
// takes a role predicate.
//
// /admin/staff promotes an EXISTING customer rather than refusing them, because a
// shop hiring somebody who already shops there is the common case. That moves their
// role and leaves every order they have placed exactly where it was — so a
// `role = 'customer'` filter on this page would make a colleague's own order
// history unreachable from the one page built to answer questions about it.
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

// orderForCustomer places one order owned by userID. Paid means captured, which
// is what makes it committed.
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

// TestTheBackOfficeSeesWhoCancelled proves the distinction survived the note
// being deleted.
//
// RecordCancellation used to write '顧客自行取消' into order_events.note purely so
// the shop could tell its own cancel from the customer's — and the customer's own
// order page renders notes, so an English customer who cancelled read a Chinese
// sentence in their timeline forever. The absence of an actor carries the same
// fact, and each audience now gets it in their own language.
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
		if e.By() != "顧客" {
			t.Errorf("the back office says %q cancelled it, want 顧客", e.By())
		}
		if e.Note != "" {
			t.Errorf("the cancellation still stores words: %q", e.Note)
		}
	}
	if !found {
		t.Fatal("no cancellation in the order's history")
	}
}

// TestADeliveredOrderMarksItsParcelsDelivered closes a fact that was recorded
// twice and only half written.
//
// order_shipments.delivered_at was READ by the customer's own order page and by
// /admin/orders, and written by NOTHING. So an order marked 已送達 still showed
// every parcel as in transit — and the customer's page reads the parcel, which
// means the shop said delivered and the customer's screen did not.
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

	// Stamped once. A later transition — a double-submitted form, a staff member
	// pressing back, the move to 已完成 — must not move a date already recorded.
	//
	// Read from the ROW and not from the view: DeliveredAt is formatted to the
	// minute, so a second stamp taken in the same minute is invisible there. The
	// first version of this compared the view strings and stayed green with the
	// `delivered_at IS NULL` predicate deleted.
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

// TestAnOrderCompletedWithoutADeliveryStepStillStampsItsParcels closes the OTHER
// transition that ends an order's delivery story.
//
// orders_legal_transition permits shipped -> completed directly, and for 超商取貨
// that is the only honest move a shop can make: nobody at the shop witnesses the
// customer walking into the store, so there is no delivery event to record. The
// stamp was wired to 'delivered' alone, so a whole delivery channel's parcels
// were never stamped — CLAUDE.md #17, a guard naming one shape of a thing that
// has two.
//
// It is not cosmetic. /admin/returns decides whether a request is inside 消保法
// §19's seven days from max(delivered_at); an unstamped parcel renders 尚未送達,
// telling the shop the window has not started for goods that arrived.
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

	// Straight from shipped, with no delivered step in between.
	if _, err := s.Advance(ctx, number, "completed", actor); err != nil {
		t.Fatalf("Advance to completed: %v", err)
	}

	if deliveredAt(t, number).IsZero() {
		t.Error("an order completed without a delivery step left its parcel unstamped — " +
			"the customer's page says it never arrived and /admin/returns reads the " +
			"rescission window as never having started")
	}
}

// TestShippingIsRefusedWhenTheOrderHoldsNoStock stops a parcel leaving under an
// order whose stock has already gone back on the shelf.
//
// Ship consumed `held` by ranging over it, and an EMPTY slice ranges silently:
// the shipment row, the status move and the dispatch notice all landed while no
// inventory movement was posted at all, so the physical unit left the warehouse
// and stock_quantity stayed where it was — over-stating the shelf by that order
// forever, and overselling the next customer.
//
// The state is produced by hand because no application path reaches it any more:
// release_reservation refuses a committed order's hold and a funded one's. That
// is exactly why the guard is worth having rather than being redundant — this
// used to be reachable through the sweeper, and a silent oversell is the failure
// that must never be silent.
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

	// The whole transaction rolled back, or the refusal recorded half a dispatch.
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

// deliveredAt is when the order's one parcel was recorded as arriving.
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

// TestTheShopCanGiveAProductASpecTable is the door product_specs never had.
//
// 規格看得懂 is the whole promise of a 選品店 and /compare exists to put two of them
// side by side — and the table was written by the dev seed and by nothing else. The
// back office could create a product, price it, photograph it and publish it, and
// the comparison table for it was two empty columns.
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
	// In the order they were added, which is the order /compare and the PDP read:
	// a spec table is a document, and its rows are not alphabetical.
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("spec labels (-want +got):\n%s", diff)
	}

	// Removing one leaves the other. The DELETE is scoped to the product in its own
	// WHERE clause, so an id from somewhere else cannot be passed in.
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

	// A second product cannot delete this one's rows.
	other := draftProduct(t, ctx, s)
	if rmErr := s.RemoveSpec(ctx, other, after.Specs[0].ID); !errors.Is(rmErr, admin.ErrNotFound) {
		t.Errorf("removing another product's spec returned %v, want ErrNotFound", rmErr)
	}
}

// TestASpecIsRefusedRatherThanTruncated holds the bounds.
//
// In RUNES on both sides — Go and the CHECK — because a spec is a table cell on
// /compare, and a byte limit would give a Chinese label a third of the room an
// English one gets on a site whose specs are written in Chinese.
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
	// The English pair is optional, so "blank" is not an error there — but a bound
	// is still a bound, and it is the same header row.
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

	// And the bound is in the DATABASE too, so a caller that skips the form still
	// meets it. Exactly at the limit is accepted; one past it is refused by name.
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

// draftProduct creates a product with no variants and returns its slug.
//
// It takes the caller's ctx because every back-office write needs an actor:
// record_audit_event refuses a row attributed to nobody, and the store refuses to
// call it without one.
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

// TestACategoryCarriesItsEnglishName is the header of every page.
//
// A category name used to be treated as CONTENT — the shop's to say however it
// likes, like a product description — and the header carried its own hard-coded
// Chinese copy of five of them. So an English visitor met a Chinese navigation bar
// above a page whose every other word had been translated, and there were two
// answers to "what is this category called".
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

	// An UNTRANSLATED category falls back to Chinese rather than to blank. A blank
	// navigation item is broken; a Chinese one is readable and visibly untranslated,
	// which is the honest failure and what /admin/taxonomy badges.
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

	// And a rename can CLEAR the translation, because a shop that added one must be
	// able to take it back. Empty is the one state the column expresses as NULL.
	if err := s.Rename(ctx, "category", slug, "測試分類", ""); err != nil {
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

// TestTheShopCanGiveAProductAVariantPicker is the door product_options never had.
//
// 顏色 and 容量 and their values were read by the PDP's picker, by the cart line and
// by the facets — and written by the dev seed and by nothing else. A shop creating
// its own product could add variants and had no way to tell them apart: the picker
// had nothing to pick, so every variant after the first was unreachable.
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
	// An axis with no values is an axis the picker renders empty, and the page says
	// so rather than looking finished.
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
	// An untranslated value carries an empty label rather than a copy of itself, so
	// the page can tell the shop which ones still need doing.
	if view.Options[0].Values[1].Label != "" {
		t.Errorf("an untranslated value reports label %q",
			view.Options[0].Values[1].Label)
	}

	// A variant must name one value per axis.
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

// TestAVariantCannotBorrowAnotherProductsOptionValue holds the composite key from
// the Go side.
//
// variant_option_values carries product_id precisely so the foreign keys can refuse
// a variant of product A paired with a value of product B. The query resolves every
// id from the SKU and the value, so a caller cannot even present the mismatch — this
// is what proves the refusal reaches the staff member as a message rather than a 500.
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

	// The second product declares one axis of its own, so the count matches and the
	// only thing wrong with the submission is WHOSE value it is.
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
	// And nothing was left behind: the variant and its link share a transaction.
	var variants int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM product_variants WHERE sku = 'BORROW-1'`).Scan(&variants); err != nil {
		t.Fatalf("count variants: %v", err)
	}
	if variants != 0 {
		t.Errorf("%d variants named BORROW-1 survived the refusal", variants)
	}
}

// TestTheOptionValueIsAddedToTheRightProduct proves the value cannot land on
// another product's axis.
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

// TestARestockNoticeNamesTheProductInTheReadersLanguage closes the last place a
// half-translated letter could leave the building.
//
// The restock mail's words already followed stock_notifications.locale — that column
// exists for exactly this, because the worker is serving nobody and cannot read a
// request. The PRODUCT NAME did not: it was read once, in Chinese, and put in every
// recipient's payload. So an English subscriber got an English letter about 保護殼,
// which is the failure the locale feature exists to prevent, arriving where nobody
// would see it in review.
func TestARestockNoticeNamesTheProductInTheReadersLanguage(t *testing.T) {
	ctx := t.Context()
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	// A product whose two names differ, so the payload can be told apart.
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

// TestAltTextFollowsThePagesLanguage is the accessibility half of the locale work.
//
// A screen reader announces alt text in the language <html lang> declares. An
// English page whose alt text is Chinese is announced in the wrong voice or not at
// all — which is a failure only the people who depend on it ever meet, and nobody at
// the shop can see.
//
// Alt text is REQUIRED in Chinese and optional in English, and the fallback is the
// Chinese text: mispronounced is still better than silence, which is what a blank
// would give.
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

	// An image with no English alt text falls back rather than going silent.
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

// storeMedia puts one image in media_objects and returns its digest.
//
// Its own row, not the one another test uses: two tests sharing a digest is two
// tests sharing state, and product_images_storage_key_key is per product so the
// collision would only show up as an ordering-dependent failure.
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

// TestTheShopCanRunAPromotion is the door promo_banners never had.
//
// The strip is documented as a feature the shop runs — middleware decides which
// paths carry it, dismissing one writes a cookie keyed on a digest of its id, the
// design decision about it being in normal flow is recorded — and the only way to
// create one was SQL. check-layout seeds one with psql, which is the tell: a fixture
// that reaches past the application is a fixture for a feature with no entrance.
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

	// And the storefront shows it, in the visitor's language, through the same
	// middleware read the site uses.
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

	// Switching it off takes it off the storefront and leaves the row: a promotion
	// that ran is part of what the site said.
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

// TestAPromotionsButtonMustStayOnThisSite holds the one thing the schema cannot.
//
// The CTA href is typed by a person and rendered into a link at the top of every
// storefront page. An absolute URL there sends every visitor off-site from the top of
// the site, and `javascript:` puts script in it — the same rule the hero's CTA
// follows, through the same one owner of it.
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

	// And half a CTA is refused, because a button with no destination is worse than
	// no button — promo_banners_cta_complete says the same in the schema.
	errs, err := s.CreateBanner(ctx, &admin.BannerForm{Message: "測試", CTALabel: "看看"})
	if err != nil {
		t.Fatalf("CreateBanner: %v", err)
	}
	if errs["cta"] == "" {
		t.Errorf("a label with no href was accepted: %v", errs)
	}
}

// TestSupportCanAnswerAQuestionWithoutADeploy is the claim CLAUDE.md made and the
// door it never had.
//
// "/faq reads faq_entries, so support can answer a recurring question without a
// deploy" — and nothing could write faq_entries. The promise was the design intent;
// the entrance was never built. The third feature this sweep found in that state,
// after product_specs and promo_banners.
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

	// And the STOREFRONT shows it, in the visitor's language, through the same read
	// /faq does.
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

	// Rewriting it changes the page, which is the whole point of the door.
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

	// And deleting it takes it off: an answer the shop no longer stands behind
	// should stop being published.
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

// TestTwoFAQEntriesInOneCategoryDoNotCollide holds the position rule.
//
// faq_entries_position_key is unique on (category, position), so the position has to
// be computed inside the INSERT and WITHIN the category — two staff members adding to
// 訂單 would otherwise both read the same maximum, and the second would meet the
// index instead of the page.
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

// TestAShopCanOfferAThirdDeliveryMethod is the door shipping_methods never had.
//
// /admin/shipping could publish a new VERSION of a method the seed created and set a
// surcharge for a zone the seed created, and neither of the two things underneath. A
// shop could change its prices and not its carriers.
func TestAShopCanOfferAThirdDeliveryMethod(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	// What a method is OFFERED for now depends on the basket: a carrier that
	// refuses a 27-inch monitor is not a choice for a cart with one in it. An
	// empty cart asks the question this test is about — is the method there at
	// all — without any parcel getting in the way.
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

	// A method AND its first version, in one transaction: a method with no version
	// is one the checkout finds and cannot price, and shipping_method_versions is
	// append-only so there is no repairing that by editing.
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

	// And the CHECKOUT offers it, in both languages, through the site's own read.
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

	// Switching it off takes it out of the chooser and leaves the row.
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

// TestAShopCanSayWhichPostalCodesCostMore is the door shipping_zones never had.
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
	// Spaces, commas and both together all separate, because that is how a person
	// pastes a list.
	if diff := cmp.Diff([]string{"546", "552", "553"}, prefixes); diff != "" {
		t.Errorf("prefixes (-want +got):\n%s", diff)
	}

	// A prefix belongs to exactly one zone — it is the PRIMARY KEY — so assigning it
	// elsewhere MOVES it rather than failing.
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

// TestAZonePrefixMustBeThreeDigits refuses what the schema would, with a sentence.
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

	// And a zone nothing points at can be deleted, while one with prefixes cannot:
	// the DELETE's own WHERE clause decides, so the page can say which.
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

// TestTheStockLedgerCanBeRead closes a table written by the one door and read by
// nothing.
//
// inventory_movements is what record_inventory_movement writes, and that function is
// the ONLY way stock_quantity ever moves — which is what makes "one writer" true
// rather than aspirational. Nothing read it: a shop could see that a SKU has four
// units and could not see how it got there.
func TestTheStockLedgerCanBeRead(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)

	// A variant that ALREADY holds stock, deliberately: with a prior balance of zero
	// the running total and the movement's own delta are the same number, and the
	// assertion below cannot tell them apart. The first version picked any variant
	// and stayed green with the running total replaced by the delta.
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

	// A key unique to this run: inventory_movements_idempotency_key is what makes a
	// retried adjustment one adjustment, so a fixed key would make the SECOND run of
	// this suite a no-op and the row count below wrong.
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

	// Newest first: the question at a stock list is "what just happened", not "what
	// happened when this SKU was created".
	newest := after.Rows[0]
	if newest.Delta != 7 {
		t.Errorf("the newest movement is %d, want +7", newest.Delta)
	}
	if newest.Reason != "adjustment" || newest.ReasonText() != "人工調整" {
		t.Errorf("the movement reads as %q / %q", newest.Reason, newest.ReasonText())
	}
	if newest.By() == "系統" {
		t.Error("a hand adjustment is attributed to the system")
	}
	if newest.DeltaText() != "+7" {
		t.Errorf("the delta reads %q, want +7 — a bare 7 is half the story",
			newest.DeltaText())
	}
	// The running total is the stock this movement left behind, and it is computed
	// over the WHOLE ledger rather than the page, so a page showing the last fifty
	// rows still tells the truth.
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

// TestAReleaseInTheLedgerNamesItsOrder holds the join that makes the page legible.
//
// A HOLD points at the order directly, because hold_inventory has the order id in
// hand. A RELEASE does not: release_reservation knows only the reservation, so its
// movement carries source_type 'reservation' — and without reaching through it a
// staff member reading "+1 釋放回架" sees a reservation id they cannot look up.
//
// The first version of this test exercised a hold and stayed GREEN with the
// reservation join deleted, because the direct join already covered that case. It
// tests the case the join exists for now.
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

	// Cancelling releases the hold — through release_reservation, which is what
	// writes a movement pointing at the RESERVATION.
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

// TestTheOrderPageShowsTheInvoiceChoice closes the second write-only table.
//
// invoice_preferences is collected at checkout and was read by nothing, so a staff
// member packing an order could not see whether it needed a 統編 invoice. Issuing is
// not built — a real 統一發票 goes through a 加值中心 — which is exactly why showing
// it matters: until then somebody issues these by hand.
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
	if view.InvoiceText() != "公司統編 12345678" {
		t.Errorf("the page says %q, want 公司統編 12345678", view.InvoiceText())
	}

	// An order with no preference says nothing rather than an empty row.
	plain, err := s.Order(ctx, placeUnpaidOrder(t))
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if plain.HasInvoice() {
		t.Error("an order with no preference reports one")
	}
}

// TestTheShopSetsEachProductsWarrantyTerm closes a column read by a whole feature and
// written by nothing.
//
// products.warranty_months decides how long cover lasts, and warranty registration is
// REFUSED while it is NULL — expires_on is NOT NULL, so defaulting it would have goen
// invent a promise nobody made. The column was read by internal/warranty and set by
// no form, so every product was unset and NOBODY could register anything.
//
// It is per product because it is not one number: a phone and a braided cable do not
// carry the same cover.
func TestTheShopSetsEachProductsWarrantyTerm(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	slug := draftProduct(t, ctx, s)

	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	// A new product states no term, and the form shows an empty box rather than a 0 —
	// "no cover" and "we have not said" are different claims.
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

	// Clearing it puts the product back to "not stated", which is what NULL means —
	// and what makes registration refuse rather than invent a date.
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

	// And a term outside what products_warranty_months_sane allows is refused with a
	// sentence rather than a constraint name.
	form.WarrantyMonths = admin.MaxWarrantyMonths + 1
	errs, err := s.UpdateProduct(ctx, form)
	if err != nil {
		t.Fatalf("UpdateProduct: %v", err)
	}
	if errs["warranty_months"] == "" {
		t.Errorf("a %d-month term was accepted: %v", form.WarrantyMonths, errs)
	}
}

// returnedOrderWithStock is returnedOrder with a REAL variant behind its line,
// so a restock has somewhere to go.
//
// Its own product and variant per call rather than a seeded row: placing and
// returning consume and produce stock, and several tests here do both — a shared
// row makes the suite pass in file order and fail shuffled, which is the failure
// this repository has already had once.
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
	// Stock arrives through the ledger, never by writing the column: the dev seed
	// learned that lesson and so does every fixture after it.
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

// stockOf is a variant's shelf count.
func stockOf(t *testing.T, variantID uuid.UUID) int32 {
	t.Helper()
	var n int32
	if err := pool.QueryRow(t.Context(),
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, variantID).Scan(&n); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return n
}

// returnLineID is the order line one return request is about.
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

// TestAnInspectedReturnPutsTheSellableUnitsBack is the tail a return did not have.
//
// A return stopped at 同意: the money went back through Stripe and the GOODS were
// in a state nothing recorded. inventory_movements' 'return' reason had a CHECK,
// a delta-direction rule, a safety-stock exemption and a back-office label — four
// declarations and NO caller — so units coming back were indistinguishable from a
// staff member correcting a miscount, and in fact never came back at all.
//
// Two of the three units are sellable here and one is not, because that is the
// case a single figure cannot express: a test that restocked everything it
// received would pass with the restocked column ignored entirely.
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
	// Approving moves money, never stock. If this is already up, the restock
	// below is being measured against the wrong baseline.
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

	// The LEDGER, not just the count. A shop reading /admin/stock/{sku} has to be
	// able to see that these units came from a return rather than from somebody
	// correcting a miscount — which is the whole reason the reason exists.
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

// TestAReturnCannotCloseWithAnUninspectedLine holds the guard that makes
// 'completed' mean something.
//
// Without it 'completed' is a label somebody clicks while the goods behind it are
// in a state nobody recorded — and for a RETURN that is the whole question,
// because the units either went back on the shelf or did not.
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

	// The control: inspected, it closes. Without this a CompleteReturn that
	// refused everything would pass the assertion above.
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
	// And closing posts no second movement: the stock moved at INSPECTION, which
	// is when the goods physically reached the shelf.
	if got, want := stockOf(t, variantID), int32(5-0+2); got != want {
		t.Errorf("stock is %d after closing, want %d — completing a return must not "+
			"restock a second time", got, want)
	}
}

// TestInspectingIsRefusedBeforeApproval stops stock moving for goods the shop
// refused to take.
//
// A rejected return has no parcel coming. Recording one as received would put
// units on the shelf that nobody sent, and the count would be wrong in the
// direction that oversells.
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

// TestRestockingMoreThanArrivedIsRefused holds the arithmetic in words rather
// than as a constraint name.
//
// return_request_lines_restocked_bounded refuses it anyway; a staff member cannot
// act on that, and the whole point of checking it in Go as well is the sentence.
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

// TestALineIsInspectedOnceAndACorrectionIsAnAdjustment holds what happens when a
// staff member presses the form again.
//
// The restock posts a movement keyed on (request, line), so a second inspection
// either double-restocks or is swallowed by the unique index — and swallowed is
// the WORSE of the two: somebody who miscounted, corrected the figure and
// resubmitted would read the new number on screen with the stock still at the
// old one. The write refuses instead, and says where the correction lives.
//
// That door already exists and is already audited: /admin/stock/{sku} posts an
// 'adjustment' with an actor, which is exactly what a recount is.
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

	// A CORRECTED resubmission, which is the dangerous one: different numbers.
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

	// And the row still says what the FIRST inspection found, rather than the
	// numbers the refused one carried.
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

// twoLineOrderWithStock is a picking order holding two DIFFERENT variants, which
// is what a partial dispatch needs: one line goes in this parcel and the other
// waits.
//
// Its own products per call, for the reason returnedOrderWithStock has its own:
// shipping consumes stock, and a shared seeded row makes the suite pass in file
// order and fail shuffled.
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

	// Two lines, three units each, so a parcel can carry SOME of one line and
	// all of the other — the case a single figure per order cannot express.
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
		// The hold, through the same function checkout uses. Writing the
		// reservation directly would be a fixture for a state the application
		// cannot produce.
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

// heldFor is how many units an order still has reserved for one variant.
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

// shippedFor is how many units of one line have gone out across every parcel.
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

// TestAnOrderCanShipInTwoParcels is the capability the tables modelled and the
// application could not reach.
//
// order_shipments has held several parcels per order, order_shipment_lines their
// per-line quantities, and a composite key binding lines to their parcel since
// the schema was written — while Ship wrote every remaining line at once and
// CanShip admitted only 'picking', which nothing returns an order to. So one
// order could hold exactly ONE parcel, ever, and a shop with two of three things
// on the shelf either sent a parcel claiming all three or made the customer wait.
func TestAnOrderCanShipInTwoParcels(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, variants := twoLineOrderWithStock(t, "partial")

	// First parcel: two of the first line's three, none of the second.
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
	// The hold settles by the SAME amount, or the stock story and the parcel
	// disagree: one unit of line 1 and all three of line 2 are still spoken for.
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

	// Second parcel: the rest. This is the move that was impossible.
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

	// And a third is refused: there is nothing left, so it would be a tracking
	// number the customer chases for an empty box.
	err := s.Ship(ctx, number, admin.Dispatch{
		Carrier: "黑貓宅急便", Tracking: "P3-" + number,
	}, actor)
	if !errors.Is(err, admin.ErrRefused) {
		t.Errorf("a third parcel on a fully shipped order = %v, want ErrRefused", err)
	}
}

// TestAParcelCannotCarryMoreThanRemains holds the arithmetic that keeps the
// shipment lines and the holds telling the same story.
//
// consume_reservation_partial refuses it under a lock too. Saying it here as
// well is what turns a constraint name into a sentence a staff member can act
// on, and what stops the parcel row being written before the refusal lands.
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

	// Nothing at all was written: the shipment row is created before the lines
	// are packed, so a refusal that left it behind would be a parcel with no
	// contents — which return_within_shipment then reads as a zero ceiling.
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

// TestAnEmptyParcelIsRefused stops a tracking number going out for a box with
// nothing in it, which is what a form submitted with every quantity at zero
// would otherwise produce.
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

// TestEachParcelTellsTheCustomer proves a customer whose order arrives in two
// boxes hears about both.
//
// The dispatch notice is deduped on the TRACKING number rather than the order,
// which is what makes that true: keyed on the order, the second parcel's message
// would be swallowed by outbox_messages' (topic, dedupe_key) index and the
// customer would be told about one box and left to wonder about the other.
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

	// And the order's history records both, so the shop can see what went when.
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
