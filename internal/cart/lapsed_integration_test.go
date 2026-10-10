//go:build integration

package cart_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/outbox"
)

// lapsedOrder places an order through the real checkout for a customer who
// spends credit and a single-use coupon on it, then ages its holds past expiry.
func lapsedOrder(t *testing.T, label string) (number string, accountID, couponID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, label)
	userID, accountID := creditedAccount(t, 30000)
	code := "LAPSE-" + strings.ToUpper(uuid.NewString()[:8])
	if err := pool.QueryRow(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents, max_redemptions, per_customer_limit)
		VALUES ($1, '只能用一次', 'amount', 20000, 1, 1)
		RETURNING id`, code).Scan(&couponID); err != nil {
		t.Fatalf("create coupon: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &order.Delivery{
		Email: "lapsed@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	cartID := newCart(t, s)
	if err := s.Add(ctx, cartID, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	number, err := placeOrder(t, s, ctx, cartID, uuid.NullUUID{UUID: userID, Valid: true},
		shipID, addr, code, label+"-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	if got := creditBalance(t, accountID); got != 0 {
		t.Fatalf("balance after checkout = %d, want 0; the fixture spends no credit", got)
	}
	expireHolds(t, number)
	return number, accountID, couponID
}

func expireHolds(t *testing.T, number string) {
	t.Helper()
	tag, err := pool.Exec(t.Context(), `
		UPDATE inventory_reservations
		SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 minute'
		WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`, number)
	if err != nil || tag.RowsAffected() == 0 {
		t.Fatalf("expire holds of %s: %v (%d rows)", number, err, tag.RowsAffected())
	}
}

// storeApplicationPool connects as the role the sweeper runs as in production,
// under a name waitForApplicationLock can find.
func storeApplicationPool(t *testing.T, name string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse store-role pool config: %v", err)
	}
	cfg.MaxConns = 2
	cfg.ConnConfig.RuntimeParams["application_name"] = name
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, execErr := conn.Exec(ctx, `SET ROLE store`)
		return execErr
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open store-role pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// sweepLog keeps what one sweep logged, so a failure the sweeper only logs is
// still a failure here.
type sweepLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *sweepLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *sweepLog) errors() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for line := range strings.SplitSeq(l.buf.String(), "\n") {
		if strings.Contains(line, "level=ERROR") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func sweepAs(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	var logged sweepLog
	if _, _, err := cart.NewStore(p).Sweep(t.Context(), slog.New(slog.NewTextHandler(&logged, nil))); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if failures := logged.errors(); failures != "" {
		t.Fatalf("sweep logged failures:\n%s", failures)
	}
}

type cancellationFacts struct {
	status                          string
	events, notices, reversals      int
	couponSlotsHeld, heldHolds      int
	deadlineNotices, customerNotice int
}

func factsOf(t *testing.T, number string, couponID uuid.UUID) cancellationFacts {
	t.Helper()
	var f cancellationFacts
	if err := pool.QueryRow(t.Context(), `
		SELECT o.fulfillment_status,
		       (SELECT count(*) FROM order_events e WHERE e.order_id = o.id AND e.kind = 'cancelled'),
		       (SELECT count(*) FROM outbox_messages m WHERE m.topic = $2 AND m.payload->>'order_id' = o.id::text),
		       (SELECT count(*) FROM outbox_messages m WHERE m.topic = $2 AND m.payload->>'order_id' = o.id::text AND m.payload->>'kind' = $3),
		       (SELECT count(*) FROM outbox_messages m WHERE m.topic = $2 AND m.payload->>'order_id' = o.id::text AND m.payload->>'kind' = $4),
		       (SELECT count(*) FROM store_credit_entries r
		          JOIN store_credit_entries spend ON spend.id = r.reverses_id
		         WHERE spend.order_id = o.id),
		       (SELECT count(*) FROM coupon_redemptions cr JOIN orders co ON co.id = cr.order_id
		         WHERE cr.coupon_id = $5 AND co.fulfillment_status <> 'cancelled'),
		       (SELECT count(*) FROM inventory_reservations r WHERE r.order_id = o.id AND r.state = 'held')
		FROM orders o WHERE o.order_number = $1`,
		number, outbox.TopicOrderTerminal.Name(), string(email.TerminalCancelledByPaymentDeadline),
		string(email.TerminalCancelledByCustomer), couponID).Scan(
		&f.status, &f.events, &f.notices, &f.deadlineNotices, &f.customerNotice,
		&f.reversals, &f.couponSlotsHeld, &f.heldHolds); err != nil {
		t.Fatalf("read cancellation facts of %s: %v", number, err)
	}
	return f
}

func TestTheSweepCancelsAnUnpaidOrderWhoseHoldsLapsed(t *testing.T) {
	number, accountID, couponID := lapsedOrder(t, "lapsed-cancel")
	store := storeRolePool(t)

	for range 2 {
		sweepAs(t, store)
		f := factsOf(t, number, couponID)
		if f.status != "cancelled" || f.heldHolds != 0 {
			t.Fatalf("after the sweep the order is %s with %d held holds, want cancelled with none", f.status, f.heldHolds)
		}
		if f.events != 1 || f.notices != 1 || f.deadlineNotices != 1 || f.reversals != 1 {
			t.Errorf("cancellation recorded %d events, %d notices (%d deadline), %d credit reversals; want 1 of each",
				f.events, f.notices, f.deadlineNotices, f.reversals)
		}
		if f.couponSlotsHeld != 0 {
			t.Errorf("the cancelled order still holds %d coupon slots", f.couponSlotsHeld)
		}
		if got := creditBalance(t, accountID); got != 30000 {
			t.Errorf("balance after the sweep = %d, want the 30000 spent on the order back", got)
		}
		if noticeRefunded(t, number) {
			t.Error("an order no money reached is noticed as refunded")
		}
	}
}

// noticeRefunded is what the order's terminal notice tells the mail worker.
func noticeRefunded(t *testing.T, number string) bool {
	t.Helper()
	var refunded bool
	if err := pool.QueryRow(t.Context(), `
		SELECT (m.payload->>'refunded')::boolean
		FROM outbox_messages m JOIN orders o ON m.payload->>'order_id' = o.id::text
		WHERE m.topic = $1 AND o.order_number = $2`,
		outbox.TopicOrderTerminal.Name(), number).Scan(&refunded); err != nil {
		t.Fatalf("read the terminal notice of %s: %v", number, err)
	}
	return refunded
}

// TestALateRefundedPaymentIsNotCalledNothingCharged: money reached Stripe after
// the hold lapsed, was refused, and staff refunded it and released the alarm.
// Whoever cancels the order afterwards, its notice says the money comes back.
func TestALateRefundedPaymentIsNotCalledNothingCharged(t *testing.T) {
	for _, bySweep := range []bool{true, false} {
		name := "customer cancel"
		if bySweep {
			name = "sweep"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			orderID := heldOrder(t, freshVariant(t, "lapse-refunded"), time.Hour, false)
			number := numberOf(t, orderID)
			ref := "cs_lapse_refund_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
			if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, ref); err != nil {
				t.Fatalf("open payment: %v", err)
			}
			eventID := "evt_lapse_refund_" + uuid.NewString()[:12]
			if _, err := pool.Exec(ctx, `
				INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload, processed_at, unreconciled)
				VALUES ('stripe', $1, 'checkout.session.completed', $2, '{}', now(),
				        'refused_capture: payments_capture_refuses_released_stock')`, eventID, ref); err != nil {
				t.Fatalf("record the refused capture: %v", err)
			}
			var released bool
			if err := pool.QueryRow(ctx, `SELECT release_payment_event($1)`, eventID).Scan(&released); err != nil || !released {
				t.Fatalf("staff release after refund = %t, %v", released, err)
			}

			if bySweep {
				sweepAs(t, storeRolePool(t))
			} else if err := cart.NewStore(storeRolePool(t)).CancelOrder(ctx, number); err != nil {
				t.Fatalf("customer cancel: %v", err)
			}
			if f := factsOf(t, number, uuid.Nil); f.status != "cancelled" || f.notices != 1 {
				t.Fatalf("order is %s with %d notices, want cancelled with 1", f.status, f.notices)
			}
			if !noticeRefunded(t, number) {
				t.Error("the notice of an order whose late payment was refunded says nothing was charged")
			}
		})
	}
}

// TestTheSweepLeavesAnOrderThatMayStillTakeMoney: each case is an expired hold
// on an order the sweeper must not cancel, because money has arrived or may.
func TestTheSweepLeavesAnOrderThatMayStillTakeMoney(t *testing.T) {
	ctx := t.Context()
	openPayment := func(t *testing.T, orderID uuid.UUID) string {
		t.Helper()
		ref := "cs_lapse_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
		if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, ref); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		return ref
	}
	for _, tc := range []struct {
		name  string
		order func(t *testing.T, vid uuid.UUID) uuid.UUID
	}{
		{"hold still live", func(t *testing.T, vid uuid.UUID) uuid.UUID {
			t.Helper()
			return heldOrder(t, vid, -10*time.Minute, false)
		}},
		{"paid", func(t *testing.T, vid uuid.UUID) uuid.UUID {
			t.Helper()
			return heldOrder(t, vid, time.Hour, true)
		}},
		{"paid in full with store credit", func(t *testing.T, vid uuid.UUID) uuid.UUID {
			t.Helper()
			return creditFundedHeldOrder(t, vid, time.Hour)
		}},
		{"checkout session open", func(t *testing.T, vid uuid.UUID) uuid.UUID {
			t.Helper()
			id := heldOrder(t, vid, time.Hour, false)
			openPayment(t, id)
			return id
		}},
		{"payment requires action", func(t *testing.T, vid uuid.UUID) uuid.UUID {
			t.Helper()
			id := heldOrder(t, vid, time.Hour, false)
			if _, err := pool.Exec(ctx, `UPDATE payments SET status = 'requires_action' WHERE provider_ref = $1`, openPayment(t, id)); err != nil {
				t.Fatal(err)
			}
			return id
		}},
		{"payment processing", func(t *testing.T, vid uuid.UUID) uuid.UUID {
			t.Helper()
			id := heldOrder(t, vid, time.Hour, false)
			if _, err := pool.Exec(ctx, `UPDATE payments SET status = 'processing' WHERE provider_ref = $1`, openPayment(t, id)); err != nil {
				t.Fatal(err)
			}
			return id
		}},
		{"complete session awaiting its webhook", func(t *testing.T, vid uuid.UUID) uuid.UUID {
			t.Helper()
			id := heldOrder(t, vid, time.Hour, false)
			ref := "cs_lapse_complete_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
			if _, err := pool.Exec(ctx, `SELECT record_complete_payment($1, $2, 100000)`, id, ref); err != nil {
				t.Fatal(err)
			}
			return id
		}},
		{"unreconciled provider event", func(t *testing.T, vid uuid.UUID) uuid.UUID {
			t.Helper()
			id := heldOrder(t, vid, time.Hour, false)
			ref := openPayment(t, id)
			if _, err := pool.Exec(ctx, `SELECT cancel_payment($1)`, ref); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload, processed_at, unreconciled)
				VALUES ('stripe', $1, 'checkout.session.completed', $2, '{}', now(), 'refused_capture: lapse fixture')`,
				"evt_lapse_"+uuid.NewString()[:12], ref); err != nil {
				t.Fatal(err)
			}
			return id
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := tc.order(t, freshVariant(t, "lapse-keep"))
			sweepAs(t, storeRolePool(t))
			f := factsOf(t, numberOf(t, id), uuid.Nil)
			if f.status == "cancelled" || f.events != 0 || f.notices != 0 {
				t.Errorf("the sweep cancelled it: status %s, %d cancelled events, %d notices", f.status, f.events, f.notices)
			}
		})
	}
}

// TestTheSweepRereadsPaymentsAfterTakingTheOrderLock: open_payment holds the
// order row, locked but not updated, while the sweeper waits for it. The
// payment it commits must stop the cancellation.
func TestTheSweepRereadsPaymentsAfterTakingTheOrderLock(t *testing.T) {
	ctx := t.Context()
	orderID := heldOrder(t, freshVariant(t, "lapse-lock"), time.Hour, false)
	number := numberOf(t, orderID)
	if _, err := pool.Exec(ctx, `
		SELECT release_reservation(id) FROM inventory_reservations
		WHERE order_id = $1 AND state = 'held'`, orderID); err != nil {
		t.Fatalf("release the lapsed hold: %v", err)
	}

	opener, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin payment open: %v", err)
	}
	defer func() { _ = opener.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := opener.Exec(ctx, `SELECT open_payment($1, $2, 100000)`,
		orderID, "cs_lapse_lock_"+strings.ReplaceAll(uuid.NewString(), "-", "")[:12]); err != nil {
		t.Fatalf("open payment: %v", err)
	}

	sweeper := cart.NewStore(storeApplicationPool(t, "lapse-sweep-behind-open"))
	done := make(chan error, 1)
	go func() {
		_, _, sweepErr := sweeper.Sweep(ctx, slog.New(slog.DiscardHandler))
		done <- sweepErr
	}()
	waitForApplicationLock(t, "lapse-sweep-behind-open", done)
	if err := opener.Commit(ctx); err != nil {
		t.Fatalf("commit payment open: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if f := factsOf(t, number, uuid.Nil); f.status != "pending" || f.events != 0 || f.notices != 0 {
		t.Errorf("the sweep cancelled an order whose checkout opened while it waited: status %s, %d events, %d notices",
			f.status, f.events, f.notices)
	}
}

// TestTheSweepAndTheCustomerCancelOnce: both reach the same lapsed order behind
// one lock, in either order. Exactly one cancels it, the other stands down
// without an error, and the credit comes back once.
func TestTheSweepAndTheCustomerCancelOnce(t *testing.T) {
	for _, sweepFirst := range []bool{true, false} {
		name := "customer first"
		if sweepFirst {
			name = "sweep first"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			number, accountID, couponID := lapsedOrder(t, "lapse-race")
			if _, err := pool.Exec(ctx, `
				SELECT release_reservation(r.id) FROM inventory_reservations r
				JOIN orders o ON o.id = r.order_id
				WHERE o.order_number = $1 AND r.state = 'held'`, number); err != nil {
				t.Fatalf("release the lapsed holds: %v", err)
			}

			blocker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin blocker: %v", err)
			}
			defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
			if _, err := blocker.Exec(ctx, `SELECT 1 FROM orders WHERE order_number = $1 FOR UPDATE`, number); err != nil {
				t.Fatalf("lock order: %v", err)
			}

			sweeper := cart.NewStore(storeApplicationPool(t, "lapse-race-sweep"))
			customer := cart.NewStore(storeApplicationPool(t, "lapse-race-cancel"))
			var logged sweepLog
			sweepDone, cancelDone := make(chan error, 1), make(chan error, 1)
			sweep := func() {
				go func() {
					_, _, sweepErr := sweeper.Sweep(ctx, slog.New(slog.NewTextHandler(&logged, nil)))
					sweepDone <- sweepErr
				}()
				waitForApplicationLock(t, "lapse-race-sweep", sweepDone)
			}
			cancel := func() {
				go func() {
					cancelErr := customer.CancelOrder(ctx, number)
					cancelDone <- cancelErr
				}()
				waitForApplicationLock(t, "lapse-race-cancel", cancelDone)
			}
			// The first waiter behind the blocker is granted the row first.
			if sweepFirst {
				sweep()
				cancel()
			} else {
				cancel()
				sweep()
			}

			if err := blocker.Commit(ctx); err != nil {
				t.Fatalf("release blocker: %v", err)
			}
			if err := <-sweepDone; err != nil {
				t.Fatalf("sweep: %v", err)
			}
			if err := <-cancelDone; err != nil && !errors.Is(err, cart.ErrNotCancellable) {
				t.Fatalf("customer cancel: %v", err)
			}
			if failures := logged.errors(); failures != "" {
				t.Errorf("the sweep logged failures:\n%s", failures)
			}

			f := factsOf(t, number, couponID)
			if f.status != "cancelled" || f.events != 1 || f.notices != 1 || f.reversals != 1 {
				t.Errorf("race left status %s, %d cancelled events, %d notices, %d credit reversals; want cancelled and 1 of each",
					f.status, f.events, f.notices, f.reversals)
			}
			if got := creditBalance(t, accountID); got != 30000 {
				t.Errorf("balance after the race = %d, want 30000", got)
			}
		})
	}
}

// TestACancelBehindACaptureIsRefusedNotFailed: capture_payment holds the order
// lock without updating the row. The customer's cancel waits for it, and must
// then see the payment and answer ErrNotCancellable rather than reach the
// transition trigger, whose helper the store role may not execute.
func TestACancelBehindACaptureIsRefusedNotFailed(t *testing.T) {
	ctx := t.Context()
	orderID := heldOrder(t, freshVariant(t, "cancel-capture"), time.Hour, false)
	number := numberOf(t, orderID)
	session := "cs_cancel_capture_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, session); err != nil {
		t.Fatalf("open payment: %v", err)
	}

	capturer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin capture: %v", err)
	}
	defer func() { _ = capturer.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := capturer.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`, session); err != nil {
		t.Fatalf("capture: %v", err)
	}

	customer := cart.NewStore(storeApplicationPool(t, "cancel-behind-capture"))
	done := make(chan error, 1)
	go func() {
		cancelErr := customer.CancelOrder(ctx, number)
		done <- cancelErr
	}()
	waitForApplicationLock(t, "cancel-behind-capture", done)
	if err := capturer.Commit(ctx); err != nil {
		t.Fatalf("commit capture: %v", err)
	}
	if err := <-done; !errors.Is(err, cart.ErrNotCancellable) {
		t.Errorf("cancel behind a capture = %v; want ErrNotCancellable", err)
	}
	if f := factsOf(t, number, uuid.Nil); f.status == "cancelled" {
		t.Error("a paid order was cancelled")
	}
}
