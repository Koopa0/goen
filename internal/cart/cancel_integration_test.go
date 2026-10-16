//go:build integration

package cart_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/ratelimit"
)

func TestCancellingAnOrderPutsTheStockBack(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-8")

	orderID := heldOrder(t, vid, -time.Hour, false) // held, not yet expired, unpaid
	number := numberOf(t, orderID)
	held := stockOf(t, vid)

	if err := s.CancelOrder(ctx, number); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if got := stockOf(t, vid); got != held+1 {
		t.Errorf("stock is %d after cancelling, want %d — the hold was not released", got, held+1)
	}
	var status, state string
	if err := pool.QueryRow(ctx, `
		SELECT o.fulfillment_status,
		       (SELECT state FROM inventory_reservations WHERE order_id = o.id LIMIT 1)
		FROM orders o WHERE o.id = $1`, orderID).Scan(&status, &state); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if status != "cancelled" || state != "released" {
		t.Errorf("order is %s with a %s hold, want cancelled/released", status, state)
	}
}

// TestExpiryWinningTheOrderLockDoesNotPoisonCancellation forces the stale-list
// interleaving: a sweeper queues first on the order, cancellation queues behind
// it, and the release remains uncommitted long enough to prove cancellation is
// still waiting. Once it commits, cancellation must take a fresh held snapshot
// and succeed instead of trying to release the now-settled reservation again.
func TestExpiryWinningTheOrderLockDoesNotPoisonCancellation(t *testing.T) {
	ctx := t.Context()
	variantID := freshVariant(t, "sweep-before-cancel")
	orderID := heldOrder(t, variantID, -time.Hour, false)
	number := numberOf(t, orderID)
	before := stockOf(t, variantID)
	var reservationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT id FROM inventory_reservations
		WHERE order_id = $1 AND state = 'held'`, orderID).Scan(&reservationID); err != nil {
		t.Fatalf("read held reservation: %v", err)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin order blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, lockErr := blocker.Exec(ctx,
		`SELECT 1 FROM orders WHERE id = $1 FOR UPDATE`, orderID); lockErr != nil {
		t.Fatalf("lock order: %v", lockErr)
	}

	sweepPool := applicationPool(t, "sweep-before-cancel")
	sweepTx, err := sweepPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin sweep release: %v", err)
	}
	defer func() { _ = sweepTx.Rollback(context.WithoutCancel(ctx)) }()
	sweepStatementDone := make(chan error, 1)
	go func() {
		_, releaseErr := sweepTx.Exec(ctx, `SELECT release_reservation($1)`, reservationID)
		sweepStatementDone <- releaseErr
	}()
	waitForApplicationLock(t, "sweep-before-cancel", sweepStatementDone)

	cancelPool := applicationPool(t, "cancel-behind-sweep")
	cancelDone := make(chan error, 1)
	go func() {
		cancelErr := cart.NewStore(cancelPool).CancelOrder(ctx, number)
		cancelDone <- cancelErr
	}()
	waitForApplicationLock(t, "cancel-behind-sweep", cancelDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release order blocker: %v", err)
	}
	if err := <-sweepStatementDone; err != nil {
		t.Fatalf("sweeper release after order unlock: %v", err)
	}
	// The release statement has changed the reservation but still owns the
	// order row until this explicit commit, so cancellation cannot yet pass.
	select {
	case err := <-cancelDone:
		t.Fatalf("cancellation passed an uncommitted sweep release: %v", err)
	default:
	}
	if err := sweepTx.Commit(ctx); err != nil {
		t.Fatalf("commit sweep release: %v", err)
	}
	if err := <-cancelDone; err != nil {
		t.Fatalf("cancel after sweep won the lock: %v", err)
	}

	if got := stockOf(t, variantID); got != before+1 {
		t.Errorf("stock is %d after one release, want %d", got, before+1)
	}
	var status, state string
	if err := pool.QueryRow(ctx, `
		SELECT o.fulfillment_status, r.state
		FROM orders o JOIN inventory_reservations r ON r.order_id = o.id
		WHERE o.id = $1`, orderID).Scan(&status, &state); err != nil {
		t.Fatalf("read final order and reservation: %v", err)
	}
	if status != "cancelled" || state != "released" {
		t.Errorf("final state is order=%s reservation=%s, want cancelled/released", status, state)
	}
}

func TestAFundedOrderCannotBeCancelledByItsCustomer(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-9")

	orderID := heldOrder(t, vid, -time.Hour, true) // paid
	number := numberOf(t, orderID)
	before := stockOf(t, vid)

	if err := s.CancelOrder(ctx, number); !errors.Is(err, cart.ErrNotCancellable) {
		t.Fatalf("a paid order was cancellable: %v", err)
	}
	if got := stockOf(t, vid); got != before {
		t.Errorf("stock moved to %d on a refused cancellation, want %d", got, before)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if status != "pending" {
		t.Errorf("a refused cancellation moved the order to %s", status)
	}
}

func TestCancellingTwiceIsRefusedTheSecondTime(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-10")

	number := numberOf(t, heldOrder(t, vid, -time.Hour, false))
	if err := s.CancelOrder(ctx, number); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	after := stockOf(t, vid)

	if err := s.CancelOrder(ctx, number); !errors.Is(err, cart.ErrNotCancellable) {
		t.Errorf("the second cancellation was accepted: %v", err)
	}
	if got := stockOf(t, vid); got != after {
		t.Errorf("the second cancellation moved stock to %d, want %d — it released twice", got, after)
	}
}

func TestCancellingAnOrderThatIsBeingPickedIsRefused(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-11")

	orderID := heldOrder(t, vid, -time.Hour, true) // funded, so it may leave pending
	number := numberOf(t, orderID)
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move to picking: %v", err)
	}

	if err := s.CancelOrder(ctx, number); !errors.Is(err, cart.ErrNotCancellable) {
		t.Errorf("an order being picked was cancellable: %v", err)
	}
}

func TestCancellingAnOrderThatDoesNotExistIsTheSameRefusal(t *testing.T) {
	s := cart.NewStore(pool)
	if err := s.CancelOrder(t.Context(), "GO-990101-999999"); !errors.Is(err, cart.ErrNotCancellable) {
		t.Errorf("an unknown order answered %v, want ErrNotCancellable", err)
	}
}

// TestAFundedOrderWithNoHeldStockIsStillRefused proves the funding guard in
// CancelOrderByCustomer rather than the one in release_reservation: with stock
// held the release refuses first, and only the WHERE clause is left here.
func TestAFundedOrderWithNoHeldStockIsStillRefused(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-12")

	orderID := heldOrder(t, vid, -time.Hour, true) // funded
	number := numberOf(t, orderID)
	var reservation uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM inventory_reservations WHERE order_id = $1`, orderID).Scan(&reservation); err != nil {
		t.Fatalf("read reservation: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT consume_reservation($1)`, reservation); err != nil {
		t.Fatalf("consume: %v", err)
	}

	if err := s.CancelOrder(ctx, number); !errors.Is(err, cart.ErrNotCancellable) {
		t.Fatalf("a paid order with no held stock was cancellable: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if status != "pending" {
		t.Errorf("a refused cancellation moved the order to %s", status)
	}
}

func TestAStrangerCannotCancelSomebodyElsesOrder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-13")
	orderID := heldOrder(t, vid, -time.Hour, false)
	number := numberOf(t, orderID)
	before := stockOf(t, vid)

	h := cart.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}), nil, nil)

	stranger := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/"+number+"/cancel", http.NoBody)
	stranger.SetPathValue("number", number)
	res := httptest.NewRecorder()
	h.CancelOrder(res, stranger)

	if res.Code != http.StatusNotFound {
		t.Errorf("a stranger's cancellation answered %d, want 404", res.Code)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if status != "pending" {
		t.Errorf("a stranger moved the order to %s", status)
	}
	if got := stockOf(t, vid); got != before {
		t.Errorf("a stranger's cancellation moved stock to %d, want %d", got, before)
	}

	placer := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/"+number+"/cancel", http.NoBody)
	placer.SetPathValue("number", number)
	placer.AddCookie(placedCookie(t, number))
	ok := httptest.NewRecorder()
	h.CancelOrder(ok, placer)

	if ok.Code != http.StatusSeeOther {
		t.Fatalf("the browser that placed the order got %d, want 303", ok.Code)
	}
	if got := stockOf(t, vid); got != before+1 {
		t.Errorf("the accepted cancellation left stock at %d, want %d", got, before+1)
	}
}

// TestACancelledOrdersStockComesBackByEveryDoor. Cancelled is SETTLED and not
// committed: reading any non-pending status as committed puts a cancelled order
// on the wrong side of both doors, and its units come back by no route at all.
func TestACancelledOrdersStockComesBackByEveryDoor(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-14")

	orderID := heldOrder(t, vid, time.Hour, true) // expired hold, paid
	refundAndCancel(t, orderID)

	var before int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&before); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	released, _, err := s.Sweep(ctx, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if released == 0 {
		t.Fatal("the sweeper released nothing — a cancelled order's hold is still unreachable")
	}

	var after int32
	var state string
	if err := pool.QueryRow(ctx, `
		SELECT pv.stock_quantity,
		       (SELECT r.state FROM inventory_reservations r WHERE r.order_id = $2 LIMIT 1)
		FROM product_variants pv WHERE pv.id = $1`, vid, orderID).Scan(&after, &state); err != nil {
		t.Fatalf("read stock after: %v", err)
	}
	if after != before+1 {
		t.Errorf("stock is %d after the sweep, want %d", after, before+1)
	}
	if state != "released" {
		t.Errorf("the hold is %s, want released", state)
	}
}

// refundAndCancel cancels a paid order the one way the schema admits: a full
// refund before shipment whose card refund has settled.
func refundAndCancel(t *testing.T, orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	var staff, returnID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role)
		VALUES ('refund-' || gen_random_uuid() || '@goen.invalid', 'staff') RETURNING id`).Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT open_refund_before_shipment(order_number, 'test', $2, 'test')
		FROM orders WHERE id = $1`, orderID, staff).Scan(&returnID); err != nil {
		t.Fatalf("refund before shipment: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO refunds (payment_id, return_request_id, request_key, status, amount_cents,
		                      reason, provider_ref, succeeded_at)
		 SELECT p.id, r.id, 'return:' || r.id, 'succeeded', r.card_refund_cents,
		        r.resolution, 're_' || r.id, now()
		 FROM return_requests r JOIN payments p ON p.order_id = r.order_id AND p.status = 'succeeded'
		 WHERE r.id = $1`,
		`INSERT INTO order_events (order_id, kind, note, return_request_id)
		 SELECT order_id, 'refunded', 're_' || id, id FROM return_requests WHERE id = $1`,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE id = (SELECT order_id FROM return_requests WHERE id = $1)`,
	} {
		if _, err := pool.Exec(ctx, stmt, returnID); err != nil {
			t.Fatalf("settle the refund and cancel: %v", err)
		}
	}
}

func TestTheShoppersTimelineNeverShowsTheProvidersRefundID(t *testing.T) {
	ctx := t.Context()
	vid := freshVariant(t, "refundid-1")
	orderID := heldOrderFor(t, uuid.NullUUID{}, vid, -time.Hour, true)
	refundAndCancel(t, orderID)

	var number, staffNote string
	if err := pool.QueryRow(ctx, `
		SELECT o.order_number, e.note FROM orders o
		JOIN order_events e ON e.order_id = o.id AND e.kind = 'refunded'
		WHERE o.id = $1`, orderID).Scan(&number, &staffNote); err != nil || !strings.HasPrefix(staffNote, "re_") {
		t.Fatalf("the fixture must keep the refund id on the event: %q, %v", staffNote, err)
	}
	view, err := cart.NewStore(pool).Order(ctx, number)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	var sawRefund bool
	for _, e := range view.Timeline {
		if e.Kind == "refunded" {
			sawRefund = true
		}
		if strings.Contains(e.Note, "re_") {
			t.Errorf("%s event shows the provider refund id %q", e.Kind, e.Note)
		}
	}
	if !sawRefund {
		t.Error("the refunded event is missing from the shopper's timeline")
	}
}

func TestACancelledOrderIsNotAVerifiedPurchase(t *testing.T) {
	ctx := t.Context()
	vid := freshVariant(t, "stockfix-15")

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ('cancelbadge@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	orderID := heldOrderFor(t, uuid.NullUUID{UUID: userID, Valid: true}, vid, -time.Hour, true) // funded

	var productID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT product_id FROM product_variants WHERE id = $1`, vid).Scan(&productID); err != nil {
		t.Fatalf("read product: %v", err)
	}
	review := func() error {
		_, err := pool.Exec(ctx, `
			INSERT INTO product_reviews (product_id, user_id, rating, body, is_verified_purchase)
			VALUES ($1, $2, 5, '測試評價', true)
			ON CONFLICT (product_id, user_id) DO UPDATE SET is_verified_purchase = true`,
			productID, userID)
		return err
	}

	if err := review(); err != nil {
		t.Fatalf("a real purchase could not claim 已購買: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM product_reviews WHERE product_id = $1 AND user_id = $2`,
		productID, userID); err != nil {
		t.Fatalf("clear review: %v", err)
	}

	refundAndCancel(t, orderID)
	err := review()
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "product_reviews_verified_is_real" {
		t.Errorf("a cancelled order still earned 已購買: %v", err)
	}
}
