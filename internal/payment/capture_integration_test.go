//go:build integration

package payment_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/payment"
)

// TestCaptureMarksTheOrderPaid is the happy path, end to end through the real
// SECURITY DEFINER functions.
func TestCaptureMarksTheOrderPaid(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, id := order(t, 199900)
	session := "cs_test_" + number

	if err := s.OpenPayment(ctx, number, session, 199900); err != nil {
		t.Fatalf("open: %v", err)
	}

	var status string
	var captured *int64
	if err := pool.QueryRow(ctx,
		`SELECT status, captured_amount_cents FROM payments WHERE provider_ref = $1`,
		session).Scan(&status, &captured); err != nil {
		t.Fatalf("read payment: %v", err)
	}
	if status != "requires_payment" || captured != nil {
		t.Fatalf("an opened payment is status=%q captured=%v, want requires_payment and nothing captured",
			status, captured)
	}

	got, err := captureThroughWebhook(t, s, payment.Capture{
		SessionID: session, AmountRecv: 199900, CardBrand: "visa", CardLast4: "4242",
	})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if got != number {
		t.Errorf("capture reported order %q, want %q", got, number)
	}

	var paidAt *string
	if err := pool.QueryRow(ctx,
		`SELECT status, captured_amount_cents, paid_at::text FROM payments WHERE provider_ref = $1`,
		session).Scan(&status, &captured, &paidAt); err != nil {
		t.Fatalf("read payment: %v", err)
	}
	if status != "succeeded" || captured == nil || *captured != 199900 || paidAt == nil {
		t.Errorf("after capture: status=%q captured=%v paid_at=%v, want succeeded/199900/set",
			status, captured, paidAt)
	}

	var paid bool
	if err := pool.QueryRow(ctx,
		`SELECT order_is_committed($1)`, id).Scan(&paid); err != nil {
		t.Fatalf("read committed: %v", err)
	}
	if !paid {
		t.Error("order_is_committed is false after a successful capture")
	}
}

// TestReplayedWebhookIsProcessedOnce proves a redelivered event is claimed only
// on its first arrival. Stripe delivers at least once, not exactly once.
func TestReplayedWebhookIsProcessedOnce(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	ev := &payment.WebhookEvent{
		ID: "evt_replay", Type: "checkout.session.completed", ObjectRef: "cs_replay",
		Payload: []byte(`{"id":"evt_replay","object":"event"}`),
	}

	applied := 0
	count := func(context.Context, *payment.WebhookTx) error { applied++; return nil }

	first, err := s.ProcessWebhook(ctx, ev, count)
	if err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if !first {
		t.Fatal("the first delivery of an event was not claimed")
	}

	for i := range 3 {
		again, err := s.ProcessWebhook(ctx, ev, count)
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		if again {
			t.Fatalf("replay %d was claimed; its effect would be applied twice", i)
		}
	}
	if applied != 1 {
		t.Errorf("the effect ran %d times over four deliveries, want 1", applied)
	}

	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_webhook_events WHERE event_id = 'evt_replay'`).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d rows for one event id, want 1", rows)
	}
}

// TestAFailedEffectLeavesTheEventReprocessable holds the claim to its effect: a
// claim that commits alone tells Stripe's retry the event is already seen.
func TestAFailedEffectLeavesTheEventReprocessable(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	ev := &payment.WebhookEvent{
		ID: "evt_rollback", Type: "checkout.session.completed", ObjectRef: "cs_rollback",
		Payload: []byte(`{"id":"evt_rollback","object":"event"}`),
	}

	boom := errors.New("the effect failed")
	claimed, err := s.ProcessWebhook(ctx, ev, func(context.Context, *payment.WebhookTx) error {
		return boom
	})
	if !claimed {
		t.Fatal("the first delivery was not claimed at all")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("error is %v, want the effect's own error so the handler answers 500", err)
	}

	var rows int
	if countErr := pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_webhook_events WHERE event_id = 'evt_rollback'`).Scan(&rows); countErr != nil {
		t.Fatalf("count: %v", countErr)
	}
	if rows != 0 {
		t.Fatalf("%d rows remain after a failed effect; Stripe's retry will be told "+
			"the event is already processed and the capture is lost", rows)
	}

	ran := false
	claimed, err = s.ProcessWebhook(ctx, ev, func(context.Context, *payment.WebhookTx) error {
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !claimed || !ran {
		t.Errorf("retry claimed=%v ran=%v, want both true", claimed, ran)
	}

	var processed bool
	if scanErr := pool.QueryRow(ctx,
		`SELECT processed_at IS NOT NULL FROM payment_webhook_events WHERE event_id = 'evt_rollback'`).
		Scan(&processed); scanErr != nil {
		t.Fatalf("read processed: %v", scanErr)
	}
	if !processed {
		t.Error("the successful retry did not stamp processed_at")
	}
}

// TestWebhookCapabilityCannotOperateAnotherProviderReference binds the lock to
// the callback capability. An event for A must not capture B: ProcessWebhook
// holds only A's advisory lock, so accepting B would reopen the exact identity
// race the provider-ref fence closes.
func TestWebhookCapabilityCannotOperateAnotherProviderReference(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, _ := order(t, 76000)
	lockedRef := "cs_capability_a_" + uuid.NewString()[:12]
	otherRef := "cs_capability_b_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, otherRef, 76000); err != nil {
		t.Fatalf("open other payment: %v", err)
	}

	eventID := "evt_capability_" + uuid.NewString()[:12]
	claimed, err := s.ProcessWebhook(ctx, &payment.WebhookEvent{
		ID: eventID, Type: "checkout.session.completed", ObjectRef: lockedRef,
		Payload: []byte(`{"object":"event"}`),
	}, func(ctx context.Context, tx *payment.WebhookTx) error {
		_, captureErr := tx.Capture(ctx, payment.Capture{
			SessionID: otherRef, AmountRecv: 76000,
		})
		return captureErr
	})
	if !claimed {
		t.Fatal("mismatched webhook was not initially claimed")
	}
	if err == nil || !strings.Contains(err.Error(), "cannot capture session") {
		t.Fatalf("mismatched webhook capture = %v, want provider identity refusal", err)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, otherRef).Scan(&status); err != nil {
		t.Fatalf("read protected payment: %v", err)
	}
	if status != "requires_payment" {
		t.Errorf("mismatched callback changed payment B to %q, want requires_payment", status)
	}
	var eventRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_webhook_events WHERE event_id = $1`, eventID).
		Scan(&eventRows); err != nil {
		t.Fatalf("count rolled-back event: %v", err)
	}
	if eventRows != 0 {
		t.Errorf("mismatched callback left %d event rows, want rollback for Stripe retry", eventRows)
	}
}

// TestTheSchemaRefusesACaptureThatIsNotWhatTheOrderOwes proves the trigger, not
// the Go check, stops a capture for the wrong figure.
func TestTheSchemaRefusesACaptureThatIsNotWhatTheOrderOwes(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, id := order(t, 199900)
	session := "cs_mismatch_" + number

	if err := s.OpenPayment(ctx, number, session, 199900); err != nil {
		t.Fatalf("open: %v", err)
	}

	for _, amount := range []int64{1, 199899, 199901, 999900} {
		// Straight at the posting function: the Go check is not what is measured.
		_, err := pool.Exec(ctx,
			`SELECT capture_payment($1, $2, NULL, NULL)`, session, amount)
		if err == nil {
			t.Errorf("a capture of %d against an order owing 199900 was accepted", amount)
			continue
		}
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		if !ok || pgErr.ConstraintName != "payments_capture_matches_order" {
			t.Errorf("capture of %d refused by %v, want payments_capture_matches_order", amount, err)
		}
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, session).Scan(&status); err != nil {
		t.Fatalf("read payment: %v", err)
	}
	if status != "requires_payment" {
		t.Errorf("payment is %q after four refused captures, want requires_payment", status)
	}
	_ = id
}

// TestCaptureRefusesAnAmountThatIsNotTheIntent covers what the schema's guard
// cannot: payments_capture_matches_order compares the capture to what the order
// owes NOW, and the Go check to what the session was opened for.
func TestCaptureRefusesAnAmountThatIsNotTheIntent(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, id := order(t, 100000)
	session := "cs_intent_" + number

	if err := s.OpenPayment(ctx, number, session, 100000); err != nil {
		t.Fatalf("open: %v", err)
	}

	// The order changes afterwards, so the schema would now accept 130000.
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET shipping_cents = 30000 WHERE id = $1`, id); err != nil {
		t.Fatalf("adjust order: %v", err)
	}

	_, err := captureThroughWebhook(t, s, payment.Capture{SessionID: session, AmountRecv: 130000})
	if err == nil {
		t.Fatal("a capture of 130000 was accepted against a session opened for 100000")
	}
	if !strings.Contains(err.Error(), "against an intent of 100000") {
		t.Errorf("refused with %v, want a message naming the intent it did not match", err)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, session).Scan(&status); err != nil {
		t.Fatalf("read payment: %v", err)
	}
	if status != "requires_payment" {
		t.Errorf("payment is %q, want requires_payment — nothing should have posted", status)
	}
}

// TestCaptureWithNoCardDetails proves a capture succeeds when Stripe sends no
// card brand or last4, because unknown is NULL and never "".
func TestCaptureWithNoCardDetails(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, _ := order(t, 88800)
	session := "cs_nocard_" + number

	if err := s.OpenPayment(ctx, number, session, 88800); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := captureThroughWebhook(t, s, payment.Capture{SessionID: session, AmountRecv: 88800}); err != nil {
		t.Fatalf("a capture carrying no card details was refused: %v", err)
	}

	var brand, last4 *string
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status, card_brand, card_last4 FROM payments WHERE provider_ref = $1`,
		session).Scan(&status, &brand, &last4); err != nil {
		t.Fatalf("read payment: %v", err)
	}
	if status != "succeeded" {
		t.Errorf("status is %q, want succeeded", status)
	}
	if brand != nil || last4 != nil {
		t.Errorf("card is %v/%v, want NULL/NULL — an unknown card is absent, not blank", brand, last4)
	}
}

func TestCaptureForAnUnknownSessionIsNotFound(t *testing.T) {
	s := payment.NewStore(pool)
	_, err := captureThroughWebhook(t, s, payment.Capture{SessionID: "cs_never_opened", AmountRecv: 100})
	if err == nil {
		t.Fatal("a capture for a session goen never opened was accepted")
	}
	if !errors.Is(err, payment.ErrNotFound) {
		t.Errorf("error is %v, want ErrNotFound so the handler answers 200 and stops retrying", err)
	}
}

// TestACaptureIsRefusedForACancelledOrder holds the guard that binds money to the
// order's own state: start a payment, cancel in the other tab, pay at Stripe.
func TestACaptureIsRefusedForACancelledOrder(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, id := order(t, 88800)
	session := "cs_cancelled_" + number

	if err := s.OpenPayment(ctx, number, session, 88800); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE id = $1`, id); err != nil {
		t.Fatalf("cancel the order: %v", err)
	}

	// Straight at the posting function, so the schema's guard is what is measured.
	_, err := pool.Exec(ctx, `SELECT capture_payment($1, $2::bigint, NULL, NULL)`, session, int64(88800))
	if err == nil {
		t.Fatal("a capture against a cancelled order was accepted")
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "payments_refuse_cancelled_order" {
		t.Fatalf("refused by %v, want payments_refuse_cancelled_order", err)
	}

	// The one capture failure Stripe must not retry, because it never succeeds.
	if _, err := captureThroughWebhook(t, s, payment.Capture{SessionID: session, AmountRecv: 88800}); !errors.Is(err, payment.ErrOrderCancelled) {
		t.Errorf("Capture returned %v, want ErrOrderCancelled so the webhook records "+
			"the event and stops Stripe retrying something that can never succeed", err)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, session).Scan(&status); err != nil {
		t.Fatalf("read payment: %v", err)
	}
	if status != "requires_payment" {
		t.Errorf("payment is %q after a refused capture, want requires_payment", status)
	}
}

// TestStoreCannotWriteASucceededPaymentDirectly proves the store role cannot
// forge a payment, which is the privilege boundary every guard above rests on.
func TestStoreCannotWriteASucceededPaymentDirectly(t *testing.T) {
	ctx := t.Context()
	number, id := order(t, 12345)
	_ = number

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET ROLE store`); err != nil {
		t.Fatalf("set role: %v", err)
	}
	// Proof the role took: as the owner every write below succeeds.
	var who string
	if err := tx.QueryRow(ctx, `SELECT current_user`).Scan(&who); err != nil {
		t.Fatalf("current_user: %v", err)
	}
	if who != "store" {
		t.Fatalf("running as %q, not store; this test would prove nothing", who)
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT probe`); err != nil {
		t.Fatalf("savepoint: %v", err)
	}

	tests := []struct {
		name string
		sql  string
		args []any
	}{
		{"insert a born-succeeded payment", `
			INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents,
			                      captured_amount_cents, paid_at)
			VALUES ($1, 'cs_forged', 'succeeded', 12345, 12345, now())`, []any{id}},
		{"update an existing payment", `UPDATE payments SET status = 'succeeded'`, nil},
		{"delete a payment", `DELETE FROM payments`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tx.Exec(ctx, tt.sql, tt.args...)
			if err == nil {
				t.Fatal("store performed a write that must be revoked")
			}
			// WHICH refusal matters: a born-succeeded payment also violates
			// payments_succeeded_is_captured, so any-error stays green with
			// every REVOKE removed.
			pgErr, ok := errors.AsType[*pgconn.PgError](err)
			if !ok || pgErr.Code != "42501" {
				t.Errorf("refused by %v, want SQLSTATE 42501 insufficient_privilege", err)
			}
		})
		if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT probe`); err != nil {
			t.Fatalf("rollback to savepoint: %v", err)
		}
	}
}

// TestPickupOrderCanBePaid holds that a convenience-store pickup order reaches
// the till: payments_require_complete_order has to name BOTH destinations.
func TestPickupOrderCanBePaid(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, _ := pickupOrder(t, 120000)
	session := "cs_pickup_" + number

	if err := s.OpenPayment(ctx, number, session, 120000); err != nil {
		t.Fatalf("open payment on a pickup order: %v", err)
	}
	if _, err := captureThroughWebhook(t, s, payment.Capture{SessionID: session, AmountRecv: 120000}); err != nil {
		t.Fatalf("capture on a pickup order: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox_messages WHERE topic = 'order.paid' AND dedupe_key = $1`,
		number).Scan(&n); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	if n != 1 {
		t.Errorf("%d receipts for a paid pickup order, want 1", n)
	}
}

// pickupOrder writes an order collected from a convenience store: the chain and
// no street address at all, as order_private_data_one_destination requires. No
// store number either — checkout asks for the chain alone, so payment has to
// find such an order complete.
func pickupOrder(t *testing.T, totalCents int64) (number string, id uuid.UUID) {
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
		WHERE sm.destination_kind = 'pickup_point'
		ORDER BY v.effective_at DESC LIMIT 1
		RETURNING id, order_number`).Scan(&id, &number); err != nil {
		t.Fatalf("create pickup order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'PICKUP-SKU', '測試商品', $2, 1)`, id, totalCents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                pickup_chain)
		VALUES ($1, 'pickup@example.com', '收件', '0912345678', 'family_mart')`, id); err != nil {
		t.Fatalf("create pickup private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, id
}
