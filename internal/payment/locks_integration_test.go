//go:build integration

package payment_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/payment"
)

// TestStore_OpenPaymentCaptureRace: Capture winning the open race refuses a
// second session. It covers the window between Handler.Order (which may still
// have read Paid=false) and OpenPayment. The database recheck under the order
// lock is the correctness boundary: by the time Stripe has returned a
// newly-created session, another webhook may already have funded the order.
func TestStore_OpenPaymentCaptureRace(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, id := order(t, 99000)
	oldSession := "cs_funding_race_old_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, oldSession, 99000); err != nil {
		t.Fatalf("open original payment: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin capture: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`SELECT capture_payment($1, 99000, NULL, NULL)`, oldSession); err != nil {
		t.Fatalf("capture while holding order lock: %v", err)
	}

	newSession := "cs_funding_race_new_" + uuid.NewString()[:12]
	returned := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		defer close(returned)
		done <- payment.NewStore(pool).OpenPayment(ctx, number, newSession, 99000)
	}()
	waitForSQLLock(t, ctx, returned, "%SELECT open_payment(%")

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit capture: %v", err)
	}
	select {
	case openErr := <-done:
		if !errors.Is(openErr, payment.ErrNotOpenable) {
			t.Fatalf("OpenPayment after concurrent capture = %v, want ErrNotOpenable", openErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OpenPayment stayed blocked after the capture committed")
	}

	var attempts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE order_id = $1`, id).
		Scan(&attempts); err != nil {
		t.Fatalf("count payment attempts: %v", err)
	}
	if attempts != 1 {
		t.Errorf("capture race left %d payment rows, want only the funded attempt", attempts)
	}
}

// TestCapturePaymentLockOrder: The capture_payment function locks the order
// before the payment. It fixes the lock order shared with open_payment. If
// capture holds the payment row while waiting for the order, an opener can hold
// that order while its unique-index check waits for the same payment:
// PostgreSQL must kill one side of the cycle. The NOWAIT probe makes the order
// explicit rather than hoping a stress loop happens to deadlock.
func TestCapturePaymentLockOrder(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, id := order(t, 99000)
	session := "cs_lock_order_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, session, 99000); err != nil {
		t.Fatalf("open payment: %v", err)
	}

	orderTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin order lock: %v", err)
	}
	defer func() { _ = orderTx.Rollback(ctx) }()
	if _, lockErr := orderTx.Exec(ctx, `SELECT 1 FROM orders WHERE id = $1 FOR UPDATE`, id); lockErr != nil {
		t.Fatalf("lock order: %v", lockErr)
	}

	returned := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		defer close(returned)
		_, captureErr := pool.Exec(ctx,
			`SELECT capture_payment($1, 99000, NULL, NULL)`, session)
		done <- captureErr
	}()
	waitForSQLLock(t, ctx, returned, "%SELECT capture_payment(%")

	probe, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin payment-lock probe: %v", err)
	}
	if _, err := probe.Exec(ctx,
		`SELECT 1 FROM payments WHERE provider_ref = $1 FOR UPDATE NOWAIT`, session); err != nil {
		_ = probe.Rollback(ctx)
		t.Fatalf("capture locked payment before the order: %v", err)
	}
	if err := probe.Rollback(ctx); err != nil {
		t.Fatalf("release payment-lock probe: %v", err)
	}

	if err := orderTx.Commit(ctx); err != nil {
		t.Fatalf("release order lock: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("capture after order unlock: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("capture stayed blocked after the order lock was released")
	}
}

// TestStore_ProcessWebhookRefLock: A webhook and an open share the provider
// reference lock. It proves both outcomes at the network/database boundary.
// Whichever transaction owns the Stripe reference first becomes visible in full
// before the other can decide whether the reference has a local payment
// identity.
func TestStore_ProcessWebhookRefLock(t *testing.T) {
	type webhookResult struct {
		claimed bool
		err     error
	}

	t.Run("webhook first leaves a permanent event fence", func(t *testing.T) {
		ctx := t.Context()
		s := payment.NewStore(pool)
		number, id := order(t, 91000)
		session := "cs_webhook_lock_first_" + uuid.NewString()[:12]
		eventID := "evt_webhook_lock_first_" + uuid.NewString()[:12]

		callbackReady := make(chan error, 1)
		releaseWebhook := make(chan struct{})
		release := func() {
			select {
			case <-releaseWebhook:
			default:
				close(releaseWebhook)
			}
		}
		t.Cleanup(release)

		webhookDone := make(chan webhookResult, 1)
		go func() {
			claimed, err := s.ProcessWebhook(ctx, &payment.WebhookEvent{
				ID: eventID, Type: "checkout.session.completed", ObjectRef: session,
				Payload: []byte(`{"object":"event"}`),
			}, func(ctx context.Context, tx *payment.WebhookTx) error {
				_, captureErr := tx.Capture(ctx, payment.Capture{
					SessionID: session, AmountRecv: 91000,
				})
				if !errors.Is(captureErr, payment.ErrNotFound) {
					err := fmt.Errorf("unattributed capture = %w, want ErrNotFound", captureErr)
					callbackReady <- err
					return err
				}
				markErr := tx.Unreconciled(ctx,
					"unattributed_capture: provider-reference lock test")
				callbackReady <- markErr
				if markErr != nil {
					return markErr
				}
				select {
				case <-releaseWebhook:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			webhookDone <- webhookResult{claimed: claimed, err: err}
		}()

		if readyErr := <-callbackReady; readyErr != nil {
			t.Fatalf("prepare uncommitted webhook: %v", readyErr)
		}

		openReturned := make(chan struct{})
		openDone := make(chan error, 1)
		go func() {
			defer close(openReturned)
			openDone <- payment.NewStore(pool).OpenPayment(ctx, number, session, 91000)
		}()
		waitForSQLLock(t, ctx, openReturned, "%SELECT open_payment(%")

		release()
		select {
		case result := <-webhookDone:
			if result.err != nil || !result.claimed {
				t.Fatalf("webhook result = claimed %v, %v; want true/nil", result.claimed, result.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("webhook stayed blocked after its callback was released")
		}

		select {
		case openErr := <-openDone:
			if !errors.Is(openErr, payment.ErrNotOpenable) {
				t.Fatalf("OpenPayment after concurrent webhook = %v, want ErrNotOpenable", openErr)
			}
			pgErr, ok := errors.AsType[*pgconn.PgError](openErr)
			if !ok || pgErr.ConstraintName != "payments_open_refuses_seen_provider_ref" {
				t.Fatalf("concurrent late-link refusal = %v, want payments_open_refuses_seen_provider_ref", openErr)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("OpenPayment stayed blocked after the webhook committed")
		}

		var paymentRows int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM payments WHERE order_id = $1`, id).Scan(&paymentRows); err != nil {
			t.Fatalf("count payments after webhook-first race: %v", err)
		}
		if paymentRows != 0 {
			t.Errorf("webhook-first race left %d payment rows, want 0", paymentRows)
		}
	})

	t.Run("open first gives the webhook a committed identity", func(t *testing.T) {
		ctx := t.Context()
		number, id := order(t, 92000)
		session := "cs_open_lock_first_" + uuid.NewString()[:12]
		eventID := "evt_open_lock_first_" + uuid.NewString()[:12]

		openTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin open transaction: %v", err)
		}
		defer func() { _ = openTx.Rollback(ctx) }()
		if _, err := openTx.Exec(ctx,
			`SELECT open_payment($1, $2, 92000)`, id, session); err != nil {
			t.Fatalf("open payment without committing: %v", err)
		}

		webhookReturned := make(chan struct{})
		webhookDone := make(chan webhookResult, 1)
		go func() {
			defer close(webhookReturned)
			claimed, processErr := payment.NewStore(pool).ProcessWebhook(ctx, &payment.WebhookEvent{
				ID: eventID, Type: "checkout.session.completed", ObjectRef: session,
				Payload: []byte(`{"object":"event"}`),
			}, func(ctx context.Context, tx *payment.WebhookTx) error {
				got, captureErr := tx.Capture(ctx, payment.Capture{
					SessionID: session, AmountRecv: 92000, Currency: payment.Currency,
				})
				if captureErr != nil {
					return captureErr
				}
				if got != number {
					return fmt.Errorf("capture attributed order %q, want %q", got, number)
				}
				return nil
			})
			webhookDone <- webhookResult{claimed: claimed, err: processErr}
		}()
		waitForSQLLock(t, ctx, webhookReturned, "%lock_payment_provider_ref(%")

		if err := openTx.Commit(ctx); err != nil {
			t.Fatalf("commit opening payment: %v", err)
		}
		select {
		case result := <-webhookDone:
			if result.err != nil || !result.claimed {
				t.Fatalf("webhook after open = claimed %v, %v; want true/nil", result.claimed, result.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("webhook stayed blocked after open_payment committed")
		}

		var status string
		var processed bool
		if err := pool.QueryRow(ctx, `
			SELECT p.status, e.processed_at IS NOT NULL
			FROM payments p
			JOIN payment_webhook_events e
			  ON e.provider = p.provider AND e.object_ref = p.provider_ref
			WHERE p.order_id = $1 AND e.event_id = $2`, id, eventID).
			Scan(&status, &processed); err != nil {
			t.Fatalf("read open-first result: %v", err)
		}
		if status != "succeeded" || !processed {
			t.Errorf("open-first result = payment %q, processed %v; want succeeded/true",
				status, processed)
		}
	})
}

// TestStore_OpenPaymentOrderLock: Cancellation and payment opening share the
// order lock.
func TestStore_OpenPaymentOrderLock(t *testing.T) {
	ctx := t.Context()

	t.Run("opening wins and cancellation is refused", func(t *testing.T) {
		number, id := order(t, 88800)
		session := "cs_open_wins_" + number
		tx1, beginErr := pool.Begin(ctx)
		if beginErr != nil {
			t.Fatalf("begin opening transaction: %v", beginErr)
		}
		defer func() { _ = tx1.Rollback(ctx) }()
		if _, openErr := tx1.Exec(ctx, `SELECT open_payment($1, $2, $3::bigint)`,
			id, session, int64(88800)); openErr != nil {
			t.Fatalf("open payment while holding its order lock: %v", openErr)
		}

		type cancelResult struct {
			err error
		}
		returned := make(chan struct{})
		done := make(chan cancelResult, 1)
		go func() {
			defer close(returned)
			done <- cancelResult{err: buyer.NewStore(pool).CancelOrder(ctx, number)}
		}()
		waitForSQLLock(t, ctx, returned,
			"%FROM orders WHERE order_number = $1 FOR UPDATE%")

		if commitErr := tx1.Commit(ctx); commitErr != nil {
			t.Fatalf("commit opening transaction: %v", commitErr)
		}
		var result cancelResult
		select {
		case result = <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Cancel stayed blocked after open_payment committed")
		}
		if !errors.Is(result.err, buyer.ErrNotCancellable) {
			t.Fatalf("Cancel = %v; want ErrNotCancellable", result.err)
		}
		var fulfillment, paymentStatus string
		if err := pool.QueryRow(ctx, `
			SELECT o.fulfillment_status, p.status
			FROM orders o JOIN payments p ON p.order_id = o.id
			WHERE o.id = $1 AND p.provider_ref = $2`, id, session).
			Scan(&fulfillment, &paymentStatus); err != nil {
			t.Fatal(err)
		}
		if fulfillment != "pending" || paymentStatus != "requires_payment" {
			t.Errorf("opening first = order %q, payment %q; want pending/requires_payment", fulfillment, paymentStatus)
		}
	})

	t.Run("cancellation wins and opening is refused", func(t *testing.T) {
		number, id := order(t, 88800)
		session := "cs_cancel_wins_" + number
		tx1, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin cancellation transaction: %v", err)
		}
		defer func() { _ = tx1.Rollback(ctx) }()
		if _, err := tx1.Exec(ctx, `
			UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
			WHERE id = $1`, id); err != nil {
			t.Fatalf("cancel while holding the order lock: %v", err)
		}

		returned := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			defer close(returned)
			done <- payment.NewStore(pool).OpenPayment(ctx, number, session, 88800)
		}()
		waitForSQLLock(t, ctx, returned, "%SELECT open_payment(%")

		if err := tx1.Commit(ctx); err != nil {
			t.Fatalf("commit cancellation: %v", err)
		}
		var openErr error
		select {
		case openErr = <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("OpenPayment stayed blocked after cancellation committed")
		}
		if !errors.Is(openErr, payment.ErrNotOpenable) {
			t.Fatalf("OpenPayment returned %v, want ErrNotOpenable", openErr)
		}

		var rows int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM payments WHERE provider_ref = $1`, session).Scan(&rows); err != nil {
			t.Fatalf("count payment rows after refused open: %v", err)
		}
		if rows != 0 {
			t.Errorf("the cancellation-winning order gained %d payment rows, want none", rows)
		}
	})
}
