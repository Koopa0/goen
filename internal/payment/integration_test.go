//go:build integration

package payment_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/payment"
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

// order writes a placed order and returns its number and id. One transaction,
// because orders_has_lines refuses an order with no lines yet.
func order(t *testing.T, totalCents int64) (number string, id uuid.UUID) {
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
		RETURNING id, order_number`).Scan(&id, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'PAY-SKU', '測試商品', $2, 1)`, id, totalCents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'pay@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		id); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit order: %v", err)
	}
	return number, id
}

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

	got, err := s.Capture(ctx, &payment.Capture{
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
	count := func(context.Context, *payment.Store) error { applied++; return nil }

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
	claimed, err := s.ProcessWebhook(ctx, ev, func(context.Context, *payment.Store) error {
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
	claimed, err = s.ProcessWebhook(ctx, ev, func(context.Context, *payment.Store) error {
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

	_, err := s.Capture(ctx, &payment.Capture{SessionID: session, AmountRecv: 130000})
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
	if _, err := s.Capture(ctx, &payment.Capture{SessionID: session, AmountRecv: 88800}); err != nil {
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

// TestCaptureForAnUnknownSessionIsNotFound proves a webhook naming a session
// goen never opened creates no payment.
func TestCaptureForAnUnknownSessionIsNotFound(t *testing.T) {
	s := payment.NewStore(pool)
	_, err := s.Capture(t.Context(), &payment.Capture{SessionID: "cs_never_opened", AmountRecv: 100})
	if err == nil {
		t.Fatal("a capture for a session goen never opened was accepted")
	}
	if !errors.Is(err, payment.ErrNotFound) {
		t.Errorf("error is %v, want ErrNotFound so the handler answers 200 and stops retrying", err)
	}
}

// TestOpeningTheSamePaymentTwiceIsOneRow proves opening is idempotent.
func TestOpeningTheSamePaymentTwiceIsOneRow(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, id := order(t, 50000)
	session := "cs_idem_" + number

	for range 3 {
		if err := s.OpenPayment(ctx, number, session, 50000); err != nil {
			t.Fatalf("open: %v", err)
		}
	}

	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE order_id = $1`, id).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d payment rows after opening the same session three times, want 1", rows)
	}
}

// TestASecondPaymentAttemptReusesTheOpenSession is the double-charge defect and
// the read that closes it: open_payment dedupes on (order_id, provider_ref) and
// every session brings its own id, so two tabs are two real charges.
func TestASecondPaymentAttemptReusesTheOpenSession(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, id := order(t, 149900)

	first, err := s.PaymentAttempt(ctx, number, 149900)
	if err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	if first.SessionID != "" {
		t.Errorf("an order with no payments reports session %q open", first.SessionID)
	}
	if first.Prior != 0 {
		t.Errorf("an order with no payments reports %d prior attempts, want 0", first.Prior)
	}

	session := "cs_reuse_" + number
	if openErr := s.OpenPayment(ctx, number, session, 149900); openErr != nil {
		t.Fatalf("open: %v", openErr)
	}

	second, err := s.PaymentAttempt(ctx, number, 149900)
	if err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if second.SessionID != session {
		t.Fatalf("a second payment attempt reports session %q, want %q — a second "+
			"Checkout Session would be created and the customer charged twice",
			second.SessionID, session)
	}
	if second.Prior != 1 {
		t.Errorf("after one payment the attempt count is %d, want 1 — the idempotency "+
			"key would not move when a dead session has to be replaced", second.Prior)
	}

	// What the order owes can move; a stale session must not be handed back.
	stale, err := s.PaymentAttempt(ctx, number, 119900)
	if err != nil {
		t.Fatalf("attempt at a new figure: %v", err)
	}
	if stale.SessionID != "" {
		t.Errorf("a session opened for 149900 was offered for an order owing 119900")
	}

	// A session Stripe has finished with cannot take anybody's money.
	if _, cancelErr := pool.Exec(ctx, `SELECT cancel_payment($1)`, session); cancelErr != nil {
		t.Fatalf("cancel the session: %v", cancelErr)
	}
	dead, err := s.PaymentAttempt(ctx, number, 149900)
	if err != nil {
		t.Fatalf("attempt after cancellation: %v", err)
	}
	if dead.SessionID != "" {
		t.Errorf("a cancelled payment was offered as a live session to return to")
	}

	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE order_id = $1`, id).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d payment rows for one order, want 1 — each extra row is a "+
			"Checkout Session the customer can still pay", rows)
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
	if _, err := s.Capture(ctx, &payment.Capture{SessionID: session, AmountRecv: 88800}); !errors.Is(err, payment.ErrOrderCancelled) {
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

// TestTheSessionExpiryComesFromTheEarliestLiveHold binds payment to stock: the
// EARLIEST hold is the deadline, never the latest.
func TestTheSessionExpiryComesFromTheEarliestLiveHold(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, id := order(t, 60000)

	o, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if !o.HoldExpiresAt.IsZero() {
		t.Errorf("an order holding nothing reports a hold until %v", o.HoldExpiresAt)
	}
	if o.HoldCoversASession(time.Now()) {
		t.Error("an order whose stock has gone back on the shelf would still open a checkout")
	}

	late := time.Now().Add(45 * time.Minute).Truncate(time.Microsecond)
	early := time.Now().Add(35 * time.Minute).Truncate(time.Microsecond)
	hold(t, id, 0, late, "late:"+number)
	hold(t, id, 1, early, "early:"+number)

	o, err = s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order after the holds: %v", err)
	}
	if !o.HoldExpiresAt.Equal(early) {
		t.Errorf("the hold deadline is %v, want the EARLIER of the two holds, %v",
			o.HoldExpiresAt, early)
	}
	if !o.SessionExpiry().Equal(early) {
		t.Errorf("SessionExpiry() is %v, want %v — Stripe would go on taking money "+
			"after the stock was released", o.SessionExpiry(), early)
	}
	if !o.HoldCoversASession(time.Now()) {
		t.Error("35 minutes of hold is not enough to open a 30-minute session")
	}
}

// hold reserves one unit of the nth-largest-stock variant for an order.
func hold(t *testing.T, orderID uuid.UUID, nth int, until time.Time, key string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		SELECT hold_inventory($1,
			(SELECT id FROM product_variants ORDER BY stock_quantity DESC, id LIMIT 1 OFFSET $2),
			1, $3, $4)`, orderID, nth, until, key); err != nil {
		t.Fatalf("hold stock: %v", err)
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

// TestACaptureEnqueuesTheReceipt holds that money arriving produces a receipt,
// written in the capture's own transaction.
func TestACaptureEnqueuesTheReceipt(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, _ := order(t, 149900)
	session := "cs_receipt_" + number

	// A capture runs from a webhook, where nobody is reading, so a receipt that
	// is not English read its locale from somewhere it never may.
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET locale = 'en' WHERE order_number = $1`, number); err != nil {
		t.Fatalf("set the order locale: %v", err)
	}

	if err := s.OpenPayment(ctx, number, session, 149900); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.Capture(ctx, &payment.Capture{
		SessionID: session, AmountRecv: 149900, CardBrand: "visa", CardLast4: "4242",
	}); err != nil {
		t.Fatalf("capture: %v", err)
	}

	var payload []byte
	if err := pool.QueryRow(ctx,
		`SELECT payload FROM outbox_messages WHERE topic = 'order.paid' AND dedupe_key = $1`,
		number).Scan(&payload); err != nil {
		t.Fatalf("no receipt was enqueued for %s: %v", number, err)
	}
	var got email.OrderPaid
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	// Carried in the payload rather than read at delivery: erase_user blanks
	// order_private_data.
	want := email.OrderPaid{
		OrderNumber: number, Email: "pay@example.com", Name: "收件",
		AmountCents: 149900, Card: "visa ****4242",
		Locale: "en",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("receipt (-want +got):\n%s", diff)
	}
}

// TestARedeliveredWebhookSendsOneReceipt holds the dedupe key that turns
// at-least-once delivery into one email.
func TestARedeliveredWebhookSendsOneReceipt(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, _ := order(t, 99900)
	session := "cs_dupe_" + number

	if err := s.OpenPayment(ctx, number, session, 99900); err != nil {
		t.Fatalf("open: %v", err)
	}
	capture := func() error {
		_, err := s.Capture(ctx, &payment.Capture{SessionID: session, AmountRecv: 99900})
		return err
	}
	// Twice, deliberately: the dedupe key is what is left if the claim fails.
	if err := capture(); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := capture(); err != nil {
		t.Fatalf("second capture: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox_messages WHERE topic = 'order.paid' AND dedupe_key = $1`,
		number).Scan(&n); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	if n != 1 {
		t.Errorf("%d receipts enqueued for one order, want 1", n)
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
	if _, err := s.Capture(ctx, &payment.Capture{SessionID: session, AmountRecv: 120000}); err != nil {
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

// pickupOrder writes an order collected from a convenience store: no street
// address at all, as order_private_data_one_destination requires.
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
		                                pickup_brand, pickup_store_code, pickup_store_name)
		VALUES ($1, 'pickup@example.com', '收件', '0912345678',
		        'family_mart', '012345', '台北車站門市')`, id); err != nil {
		t.Fatalf("create pickup private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, id
}

// TestPointsAreMultipliedByTheCustomersTier holds the rate an order earns at:
// the multiplier comes from the spend the customer had BEFORE this order.
func TestPointsAreMultipliedByTheCustomersTier(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)

	// A threshold of its own: membership_tiers_min_spend_key is one tier a band.
	if _, err := pool.Exec(ctx, `
		INSERT INTO membership_tiers (code, name, min_spend_cents, points_multiplier_bp, position)
		VALUES ('tiertest', '測試等級', 1100000, 20000, 9)
		ON CONFLICT (code) DO NOTHING`); err != nil {
		t.Fatalf("create the tier: %v", err)
	}

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ('tiered@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// The first order earns the tier and is captured at the base rate.
	first := payForOwnedOrder(t, s, userID, 1200000, "cs_tier_first")
	if first != 120 {
		t.Errorf("the order that earned the tier awarded %d points, want 120 at the "+
			"base rate — it was paid at the tier it created", first)
	}

	second := payForOwnedOrder(t, s, userID, 1200000, "cs_tier_second")
	if second != 240 {
		t.Errorf("the second order awarded %d points, want 240 at 2x", second)
	}
}

// payForOwnedOrder places and captures an order, and reports the points awarded.
func payForOwnedOrder(t *testing.T, s *payment.Store, userID uuid.UUID, cents int64, session string) int64 {
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
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'TIER-SKU', '測試商品', $2, 1)`, orderID, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'tiered@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := s.OpenPayment(ctx, number, session, cents); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.Capture(ctx, &payment.Capture{SessionID: session, AmountRecv: cents}); err != nil {
		t.Fatalf("capture: %v", err)
	}

	var points int64
	if err := pool.QueryRow(ctx,
		`SELECT coalesce(sum(points), 0) FROM loyalty_entries WHERE order_id = $1`,
		orderID).Scan(&points); err != nil {
		t.Fatalf("read points: %v", err)
	}
	return points
}

// TestAPartCreditOrderIsChargedOnlyWhatItOwes binds the figure the payment page
// sends to Stripe to the figure this database accepts: charging the gross takes
// the money and leaves the order unpaid forever.
func TestAPartCreditOrderIsChargedOnlyWhatItOwes(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, orderID := order(t, 100000)

	userID := creditedUser(t, 30000)
	if _, err := pool.Exec(ctx,
		`SELECT post_store_credit($1, $2, '結帳折抵', $3, $4, NULL)`,
		userID, int64(-30000), orderID, "spend:"+number); err != nil {
		t.Fatalf("spend credit on the order: %v", err)
	}

	o, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if o.TotalCents != 70000 {
		t.Fatalf("the payment page would charge %d, want 70000 — the gross total less "+
			"the credit already spent", o.TotalCents)
	}
	if o.FullyFunded() {
		t.Error("an order still owing NT$700 reports itself fully funded")
	}

	// A page that offered the gross is refused here, after the customer paid it.
	const ref = "cs_part_credit"
	if err := s.OpenPayment(ctx, number, ref, o.TotalCents); err != nil {
		t.Fatalf("OpenPayment: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT capture_payment($1, $2::bigint, NULL, NULL)`,
		ref, o.TotalCents); err != nil {
		t.Fatalf("capturing what the page asked for was refused: %v", err)
	}
}

// TestAFullyFundedOrderIsNeverSentToStripe holds the other half: an order can
// legally owe nothing, and Stripe refuses a zero-amount session.
func TestAFullyFundedOrderIsNeverSentToStripe(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, orderID := order(t, 50000)

	userID := creditedUser(t, 50000)
	if _, err := pool.Exec(ctx,
		`SELECT post_store_credit($1, $2, '結帳折抵', $3, $4, NULL)`,
		userID, int64(-50000), orderID, "spend-all:"+number); err != nil {
		t.Fatalf("spend credit on the order: %v", err)
	}

	o, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if o.TotalCents != 0 {
		t.Errorf("a fully credited order still owes %d", o.TotalCents)
	}
	if !o.FullyFunded() {
		t.Error("a fully credited order does not report itself funded")
	}
}

// creditedUser makes a customer holding cents of store credit.
func creditedUser(t *testing.T, cents int64) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('credit-'||gen_random_uuid()||'@goen.invalid', 'customer', '付款測試')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT post_store_credit($1, $2, '測試發放', NULL, $3, NULL)`,
		id, cents, "grant:"+id.String()); err != nil {
		t.Fatalf("grant credit: %v", err)
	}
	return id
}

// alwaysPlacedHere is the one method internal/payment needs from internal/cart.
// The webhook does not use it, and nothing here asserts on it.
type alwaysPlacedHere struct{}

func (alwaysPlacedHere) PlacedHere(context.Context, *http.Request, string, bool) bool {
	return true
}

// TestTheWebhookRoutesEachEventToItsEffect is the HTTP-level lock on the routing
// switch: a case deleted from it falls to `default` and every other test here
// stays green.
func TestTheWebhookRoutesEachEventToItsEffect(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
		slog.New(slog.DiscardHandler), false)

	tests := []struct {
		name       string
		eventType  string
		payStatus  string
		wantStatus string
		wantPaid   bool
	}{
		{
			name: "a paid checkout captures", eventType: "checkout.session.completed",
			payStatus: "paid", wantStatus: "succeeded", wantPaid: true,
		},
		{
			name:      "a delayed method that cleared captures too",
			eventType: "checkout.session.async_payment_succeeded",
			payStatus: "paid", wantStatus: "succeeded", wantPaid: true,
		},
		{
			// The alarm branch: money is in flight, so the row stays open.
			name:      "a delayed method still in flight does neither",
			eventType: "checkout.session.completed",
			payStatus: "unpaid", wantStatus: "requires_payment",
		},
		{
			name:      "an expired session closes the payment row",
			eventType: "checkout.session.expired",
			payStatus: "unpaid", wantStatus: "cancelled",
		},
		{
			name:      "a delayed method that failed closes it too",
			eventType: "checkout.session.async_payment_failed",
			payStatus: "unpaid", wantStatus: "cancelled",
		},
		{
			name:      "an event goen records and does not act on",
			eventType: "payment_intent.processing",
			payStatus: "unpaid", wantStatus: "requires_payment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			number, _ := order(t, 199900)
			session := "cs_route_" + uuid.NewString()[:12]
			if err := s.OpenPayment(ctx, number, session, 199900); err != nil {
				t.Fatalf("open: %v", err)
			}

			eventID := "evt_" + uuid.NewString()[:12]
			ev := typed(sessionEvent(eventID, session, tt.payStatus, 199900), tt.eventType)
			body, header := signed(t, ev)
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/webhooks/stripe", bytes.NewReader(body))
			req.Header.Set("Stripe-Signature", header)
			w := httptest.NewRecorder()
			h.Webhook(w, req)

			// 200 for every one of them: a 5xx gets the endpoint disabled.
			if w.Code != http.StatusOK {
				t.Fatalf("Webhook() status = %d, want 200", w.Code)
			}

			var status string
			if err := pool.QueryRow(ctx,
				`SELECT status FROM payments WHERE provider_ref = $1`, session).Scan(&status); err != nil {
				t.Fatalf("read payment: %v", err)
			}
			if status != tt.wantStatus {
				t.Errorf("after %s the payment is %q, want %q", tt.eventType, status, tt.wantStatus)
			}

			var fulfilment string
			if err := pool.QueryRow(ctx,
				`SELECT fulfillment_status FROM orders WHERE order_number = $1`,
				number).Scan(&fulfilment); err != nil {
				t.Fatalf("read order: %v", err)
			}
			paid := status == "succeeded"
			if paid != tt.wantPaid {
				t.Errorf("after %s the order reads paid=%v, want %v — the switch sent "+
					"this event to the wrong branch, or to none", tt.eventType, paid, tt.wantPaid)
			}

			// Recorded whatever branch it took: that row is the idempotency.
			var seen int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM payment_webhook_events WHERE event_id = $1`,
				eventID).Scan(&seen); err != nil {
				t.Fatalf("read event: %v", err)
			}
			if seen != 1 {
				t.Errorf("the event was recorded %d times, want once", seen)
			}
		})
	}
}
