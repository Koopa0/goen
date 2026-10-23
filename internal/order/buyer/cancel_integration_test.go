//go:build integration

package buyer_test

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

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestStore_CancelOrderReleasesStock: CancelOrder cancels an unpaid held order
// and puts its held unit back on the shelf: the order ends cancelled and its
// reservation released.
func TestStore_CancelOrderReleasesStock(t *testing.T) {
	ctx := t.Context()
	s := buyer.NewStore(pool)
	vid := ordertest.FreshVariant(t, pool, "stockfix-8")

	orderID := ordertest.HeldOrder(t, pool, vid, -time.Hour, false) // held, not yet expired, unpaid
	number := ordertest.NumberOf(t, pool, orderID)
	held := ordertest.StockOf(t, pool, vid)

	if err := s.CancelOrder(ctx, number); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if got := ordertest.StockOf(t, pool, vid); got != held+1 {
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

// TestStore_CancelOrderSweepWonLock: a sweeper's reservation release that wins
// the order lock does not poison a cancellation waiting behind it: CancelOrder
// takes a fresh held snapshot and succeeds, and the unit goes back once.
//
// It forces the stale-list interleaving: a sweeper queues first on the order,
// cancellation queues behind it, and the release remains uncommitted long
// enough to prove cancellation is still waiting. Once it commits, cancellation
// must take a fresh held snapshot and succeed instead of trying to release the
// now-settled reservation again.
func TestStore_CancelOrderSweepWonLock(t *testing.T) {
	ctx := t.Context()
	variantID := ordertest.FreshVariant(t, pool, "sweep-before-cancel")
	orderID := ordertest.HeldOrder(t, pool, variantID, -time.Hour, false)
	number := ordertest.NumberOf(t, pool, orderID)
	before := ordertest.StockOf(t, pool, variantID)
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
		cancelErr := buyer.NewStore(cancelPool).CancelOrder(ctx, number)
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

	if got := ordertest.StockOf(t, pool, variantID); got != before+1 {
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

// TestStore_CancelOrderFunded: CancelOrder refuses a paid order with
// ErrNotCancellable and leaves its stock and its pending status untouched.
func TestStore_CancelOrderFunded(t *testing.T) {
	ctx := t.Context()
	s := buyer.NewStore(pool)
	vid := ordertest.FreshVariant(t, pool, "stockfix-9")

	orderID := ordertest.HeldOrder(t, pool, vid, -time.Hour, true) // paid
	number := ordertest.NumberOf(t, pool, orderID)
	before := ordertest.StockOf(t, pool, vid)

	if err := s.CancelOrder(ctx, number); !errors.Is(err, buyer.ErrNotCancellable) {
		t.Fatalf("a paid order was cancellable: %v", err)
	}
	if got := ordertest.StockOf(t, pool, vid); got != before {
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

// TestStore_CancelOrderTwice: a second cancellation of the same order is
// refused with ErrNotCancellable and does not release its stock again.
func TestStore_CancelOrderTwice(t *testing.T) {
	ctx := t.Context()
	s := buyer.NewStore(pool)
	vid := ordertest.FreshVariant(t, pool, "stockfix-10")

	number := ordertest.NumberOf(t, pool, ordertest.HeldOrder(t, pool, vid, -time.Hour, false))
	if err := s.CancelOrder(ctx, number); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	after := ordertest.StockOf(t, pool, vid)

	if err := s.CancelOrder(ctx, number); !errors.Is(err, buyer.ErrNotCancellable) {
		t.Errorf("the second cancellation was accepted: %v", err)
	}
	if got := ordertest.StockOf(t, pool, vid); got != after {
		t.Errorf("the second cancellation moved stock to %d, want %d — it released twice", got, after)
	}
}

// TestStore_CancelOrderPicking: CancelOrder refuses an order that is being
// picked with ErrNotCancellable.
func TestStore_CancelOrderPicking(t *testing.T) {
	ctx := t.Context()
	s := buyer.NewStore(pool)
	vid := ordertest.FreshVariant(t, pool, "stockfix-11")

	orderID := ordertest.HeldOrder(t, pool, vid, -time.Hour, true) // funded, so it may leave pending
	number := ordertest.NumberOf(t, pool, orderID)
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move to picking: %v", err)
	}

	if err := s.CancelOrder(ctx, number); !errors.Is(err, buyer.ErrNotCancellable) {
		t.Errorf("an order being picked was cancellable: %v", err)
	}
}

// TestStore_CancelOrderUnknown: CancelOrder answers an unknown order number
// with ErrNotCancellable, the refusal an order that cannot be cancelled gets.
func TestStore_CancelOrderUnknown(t *testing.T) {
	s := buyer.NewStore(pool)
	if err := s.CancelOrder(t.Context(), "GO-990101-999999"); !errors.Is(err, buyer.ErrNotCancellable) {
		t.Errorf("an unknown order answered %v, want ErrNotCancellable", err)
	}
}

// TestStore_CancelOrderFundedNoHold: CancelOrder still refuses a funded order
// whose stock is no longer held, so the funding guard in CancelOrderByCustomer
// refuses it, and the order stays pending.
//
// With stock held the release in release_reservation refuses first, and only
// the WHERE clause is left here.
func TestStore_CancelOrderFundedNoHold(t *testing.T) {
	ctx := t.Context()
	s := buyer.NewStore(pool)
	vid := ordertest.FreshVariant(t, pool, "stockfix-12")

	orderID := ordertest.HeldOrder(t, pool, vid, -time.Hour, true) // funded
	number := ordertest.NumberOf(t, pool, orderID)
	var reservation uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM inventory_reservations WHERE order_id = $1`, orderID).Scan(&reservation); err != nil {
		t.Fatalf("read reservation: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT consume_reservation($1)`, reservation); err != nil {
		t.Fatalf("consume: %v", err)
	}

	if err := s.CancelOrder(ctx, number); !errors.Is(err, buyer.ErrNotCancellable) {
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

// TestHandler_CancelOrderStranger: CancelOrder answers a browser without the
// order's grant with 404 and moves neither the order nor its stock, while the
// browser that placed the order is redirected with 303 and its stock is
// released.
func TestHandler_CancelOrderStranger(t *testing.T) {
	ctx := t.Context()
	s := buyer.NewStore(pool)
	vid := ordertest.FreshVariant(t, pool, "stockfix-13")
	orderID := ordertest.HeldOrder(t, pool, vid, -time.Hour, false)
	number := ordertest.NumberOf(t, pool, orderID)
	before := ordertest.StockOf(t, pool, vid)

	h := buyer.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), testLimiter(), false)

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
	if got := ordertest.StockOf(t, pool, vid); got != before {
		t.Errorf("a stranger's cancellation moved stock to %d, want %d", got, before)
	}

	placer := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/"+number+"/cancel", http.NoBody)
	placer.SetPathValue("number", number)
	placedBy(t, placer, number)
	ok := httptest.NewRecorder()
	h.CancelOrder(ok, placer)

	if ok.Code != http.StatusSeeOther {
		t.Fatalf("the browser that placed the order got %d, want 303", ok.Code)
	}
	if got := ordertest.StockOf(t, pool, vid); got != before+1 {
		t.Errorf("the accepted cancellation left stock at %d, want %d", got, before+1)
	}
}

// TestHandler_CancelOrderRefusedRecovery: a refused cancellation of a paid
// order answers 422 with a notice linking back to the order and to the contact
// page without prefilling, and changes no stock, in both locales.
func TestHandler_CancelOrderRefusedRecovery(t *testing.T) {
	vid := ordertest.FreshVariant(t, pool, "cancel-recovery")
	orderID := ordertest.HeldOrder(t, pool, vid, -time.Hour, true)
	number := ordertest.NumberOf(t, pool, orderID)
	appPool := storeRolePool(t)
	h := buyer.NewHandler(buyer.NewStore(appPool), access.NewStore(appPool, false), slog.New(slog.DiscardHandler), testLimiter(), false)
	before := ordertest.StockOf(t, pool, vid)
	for _, locale := range i18n.Locales() {
		r := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), locale), http.MethodPost, "/orders/"+number+"/cancel", http.NoBody)
		r.SetPathValue("number", number)
		placedBy(t, r, number)
		w := httptest.NewRecorder()
		h.CancelOrder(w, r)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("refused cancellation status=%d, want 422", w.Code)
		}
		actions := orderRecoveryActions(t, w.Body.String(), "/orders/"+number)
		if !strings.Contains(actions, `href="/contact"`) || strings.Contains(actions, "/contact?") {
			t.Error("refused cancellation has no contact link without prefilling")
		}
		if !strings.Contains(actions, i18n.T(r.Context(), i18n.KeyBackToOrder)) {
			t.Error("refused cancellation has no back-to-order label")
		}
	}
	if got := ordertest.StockOf(t, pool, vid); got != before {
		t.Errorf("refused cancellation changed stock from %d to %d", before, got)
	}
}

func orderRecoveryActions(t *testing.T, body, primary string) string {
	t.Helper()
	_, actions, ok := strings.Cut(body, `class="notice__actions"`)
	if !ok {
		t.Fatal("missing order recovery actions")
	}
	actions, _, _ = strings.Cut(actions, "</div>")
	_, href, _ := strings.Cut(actions, `href="`)
	href, _, _ = strings.Cut(href, `"`)
	if href != primary {
		t.Errorf("primary recovery href=%q, want %q", href, primary)
	}
	return actions
}
