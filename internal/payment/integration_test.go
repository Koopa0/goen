//go:build integration

package payment_test

import (
	"bytes"
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
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/outbox"
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

// captureThroughWebhook applies c through the same transaction boundary as a
// real webhook. Every call uses a distinct event because these tests exercise
// capture behavior, not webhook redelivery.
func captureThroughWebhook(t *testing.T, s *payment.Store, c payment.Capture) (string, error) {
	t.Helper()
	eventID := "evt_capture_test_" + uuid.NewString()
	var orderNumber string
	claimed, err := s.ProcessWebhook(t.Context(), &payment.WebhookEvent{
		ID: eventID, Type: "checkout.session.completed", ObjectRef: c.SessionID,
		Payload: []byte(`{"object":"event"}`),
	}, func(ctx context.Context, tx *payment.WebhookTx) error {
		var captureErr error
		orderNumber, captureErr = tx.Capture(ctx, c)
		return captureErr
	})
	if err != nil {
		return "", err
	}
	if !claimed {
		return "", fmt.Errorf("unique capture webhook %s was not claimed", eventID)
	}
	return orderNumber, nil
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
	if first.MatchesOwed {
		t.Error("an order with no session reports that its session matches the amount")
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
	if !second.MatchesOwed {
		t.Error("the existing session was not recognized as matching what the order owes")
	}
	if second.Prior != 1 {
		t.Errorf("after one payment the attempt count is %d, want 1 — the idempotency "+
			"key would not move when a dead session has to be replaced", second.Prior)
	}

	// What the order owes can move. The stale session must be returned AS A
	// BLOCKER rather than hidden: until Stripe expires it, its URL can still take
	// the customer's money.
	stale, err := s.PaymentAttempt(ctx, number, 119900)
	if err != nil {
		t.Fatalf("attempt at a new figure: %v", err)
	}
	if stale.SessionID != session || stale.MatchesOwed {
		t.Errorf("stale attempt = session %q, matches=%v; want %q/false",
			stale.SessionID, stale.MatchesOwed, session)
	}

	// The database is the final defence after the remote create call: even two
	// callers that both passed the read above cannot leave two payable sessions.
	if secondOpenErr := s.OpenPayment(ctx, number, "cs_second_active_"+number, 149900); !errors.Is(secondOpenErr, payment.ErrNotOpenable) {
		t.Errorf("opening a second active session = %v, want ErrNotOpenable", secondOpenErr)
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

func TestOpeningAPaymentIsRefusedOnACancelledOrder(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, id := order(t, 88800)
	session := "cs_race_" + number

	if _, err := pool.Exec(ctx, `
		UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		WHERE id = $1`, id); err != nil {
		t.Fatalf("cancel the order: %v", err)
	}
	if err := s.OpenPayment(ctx, number, session, 88800); !errors.Is(err, payment.ErrNotOpenable) {
		t.Fatalf("OpenPayment returned %v, want ErrNotOpenable", err)
	}

	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE provider_ref = $1`, session).Scan(&rows); err != nil {
		t.Fatalf("count refused payment rows: %v", err)
	}
	if rows != 0 {
		t.Errorf("a cancelled order gained %d payment rows, want none", rows)
	}

	// Straight at the posting function: the schema's named refusal, rather
	// than the store's sentinel mapping, is what this half measures.
	_, err := pool.Exec(ctx, `SELECT open_payment($1, $2, $3::bigint)`,
		id, "cs_race_raw_"+number, int64(88800))
	if err == nil {
		t.Fatal("the raw open_payment call accepted a cancelled order")
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "payments_open_refuses_settled_order" {
		t.Fatalf("open_payment was refused by %v, want payments_open_refuses_settled_order", err)
	}

	// The missing-order branch is distinct from a settled real order, and its
	// name is part of the schema contract too.
	_, err = pool.Exec(ctx, `SELECT open_payment($1, $2, $3::bigint)`,
		uuid.New(), "cs_race_missing_"+number, int64(88800))
	if err == nil {
		t.Fatal("the raw open_payment call accepted an absent order")
	}
	pgErr, ok = errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "payments_open_needs_order" {
		t.Fatalf("missing order was refused by %v, want payments_open_needs_order", err)
	}
}

// TestCaptureWinningTheOpenRaceRefusesASecondSession covers the window between
// Handler.Order (which may still have read Paid=false) and OpenPayment. The
// database recheck under the order lock is the correctness boundary: by the
// time Stripe has returned a newly-created session, another webhook may already
// have funded the order.
func TestCaptureWinningTheOpenRaceRefusesASecondSession(t *testing.T) {
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

// TestCaptureLocksTheOrderBeforeThePayment fixes the lock order shared with
// open_payment. If capture holds the payment row while waiting for the order,
// an opener can hold that order while its unique-index check waits for the same
// payment: PostgreSQL must kill one side of the cycle. The NOWAIT probe makes
// the order explicit rather than hoping a stress loop happens to deadlock.
func TestCaptureLocksTheOrderBeforeThePayment(t *testing.T) {
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

func TestExpiredPaymentTombstoneIsIdempotentAndNeverRegressesCapturedMoney(t *testing.T) {
	ctx := t.Context()

	t.Run("an uncertain committed open converges to one cancelled row", func(t *testing.T) {
		number, id := order(t, 99000)
		session := "cs_expired_existing_" + uuid.NewString()[:12]
		if err := payment.NewStore(pool).OpenPayment(ctx, number, session, 99000); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		for range 2 {
			if _, err := pool.Exec(ctx,
				`SELECT record_expired_payment($1, $2, 99000)`, id, session); err != nil {
				t.Fatalf("record expired payment: %v", err)
			}
		}
		var rows int
		var status string
		if err := pool.QueryRow(ctx, `
			SELECT count(*), max(status) FROM payments WHERE provider_ref = $1`, session).
			Scan(&rows, &status); err != nil {
			t.Fatalf("read expired tombstone: %v", err)
		}
		if rows != 1 || status != "cancelled" {
			t.Errorf("expired tombstone = %d rows/status %q, want 1/cancelled", rows, status)
		}
	})

	t.Run("captured money is never downgraded", func(t *testing.T) {
		number, id := order(t, 99000)
		session := "cs_expired_captured_" + uuid.NewString()[:12]
		if err := payment.NewStore(pool).OpenPayment(ctx, number, session, 99000); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`SELECT capture_payment($1, 99000, 'visa', '4242')`, session); err != nil {
			t.Fatalf("capture payment: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`SELECT record_expired_payment($1, $2, 99000)`, id, session); err != nil {
			t.Fatalf("record impossible late expiry: %v", err)
		}
		var status string
		var captured int64
		if err := pool.QueryRow(ctx, `
			SELECT status, captured_amount_cents FROM payments WHERE provider_ref = $1`, session).
			Scan(&status, &captured); err != nil {
			t.Fatalf("read captured payment after expiry record: %v", err)
		}
		if status != "succeeded" || captured != 99000 {
			t.Errorf("captured payment became %q/%d, want succeeded/99000", status, captured)
		}
	})
}

// TestPaymentPostingFunctionsNameEveryAdmissionRefusal binds the error names
// that Store maps to ErrNotOpenable. A generic refusal is insufficient here:
// each branch protects a different double-charge or identity invariant.
func TestPaymentPostingFunctionsNameEveryAdmissionRefusal(t *testing.T) {
	ctx := t.Context()
	assertConstraint := func(t *testing.T, want string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("operation succeeded, want %s", want)
		}
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		if !ok || pgErr.ConstraintName != want {
			t.Fatalf("operation failed with %v, want constraint %s", err, want)
		}
	}

	t.Run("amount still matches the locked order", func(t *testing.T) {
		_, id := order(t, 101000)
		_, err := pool.Exec(ctx, `SELECT open_payment($1, $2, 101001)`,
			id, "cs_wrong_open_amount_"+uuid.NewString()[:12])
		assertConstraint(t, "payments_open_matches_order", err)
	})

	t.Run("captured money cannot gain another checkout", func(t *testing.T) {
		number, id := order(t, 102000)
		session := "cs_already_funded_" + uuid.NewString()[:12]
		if err := payment.NewStore(pool).OpenPayment(ctx, number, session, 102000); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		if _, err := pool.Exec(ctx, `SELECT capture_payment($1, 102000, NULL, NULL)`, session); err != nil {
			t.Fatalf("capture payment: %v", err)
		}
		_, err := pool.Exec(ctx, `SELECT open_payment($1, $2, 102000)`,
			id, "cs_after_funding_"+uuid.NewString()[:12])
		assertConstraint(t, "payments_open_refuses_funded_order", err)
	})

	t.Run("an alarm blocks a replacement checkout", func(t *testing.T) {
		number, id := order(t, 103000)
		session := "cs_needs_reconciliation_" + uuid.NewString()[:12]
		if err := payment.NewStore(pool).OpenPayment(ctx, number, session, 103000); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO payment_webhook_events
				(provider, event_id, type, object_ref, payload, processed_at, unreconciled)
			VALUES ('stripe', $1, 'checkout.session.completed', $2, '{}'::jsonb, now(),
			        'refused_capture: admission rule test')`,
			"evt_needs_reconciliation_"+uuid.NewString()[:12], session); err != nil {
			t.Fatalf("record reconciliation alarm: %v", err)
		}
		_, err := pool.Exec(ctx, `SELECT open_payment($1, $2, 103000)`,
			id, "cs_during_reconciliation_"+uuid.NewString()[:12])
		assertConstraint(t, "payments_open_needs_reconciliation", err)
	})

	t.Run("an expired reference cannot change identity", func(t *testing.T) {
		_, id := order(t, 104000)
		session := "cs_expired_identity_" + uuid.NewString()[:12]
		if _, err := pool.Exec(ctx, `SELECT record_expired_payment($1, $2, 104000)`, id, session); err != nil {
			t.Fatalf("record expired payment: %v", err)
		}
		_, err := pool.Exec(ctx, `SELECT record_expired_payment($1, $2, 104001)`, id, session)
		assertConstraint(t, "payments_expired_identity_matches", err)
	})

	t.Run("a complete reference cannot change identity", func(t *testing.T) {
		_, id := order(t, 104500)
		session := "cs_complete_identity_" + uuid.NewString()[:12]
		if _, err := pool.Exec(ctx, `SELECT record_complete_payment($1, $2, 104500)`, id, session); err != nil {
			t.Fatalf("record complete payment: %v", err)
		}
		_, err := pool.Exec(ctx, `SELECT record_complete_payment($1, $2, 104501)`, id, session)
		assertConstraint(t, "payments_complete_identity_matches", err)
	})
}

// TestReconciliationRolesHaveOneDoor keeps the webhook alarm monotone. The
// store may raise it but cannot clear or rewrite it; admin may resolve it only
// through the function that atomically closes the linked live payment.
func TestReconciliationRolesHaveOneDoor(t *testing.T) {
	ctx := t.Context()
	number, _ := order(t, 105000)
	session := "cs_role_reconcile_" + uuid.NewString()[:12]
	if err := payment.NewStore(pool).OpenPayment(ctx, number, session, 105000); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	eventID := "evt_role_reconcile_" + uuid.NewString()[:12]
	processedEventID := "evt_role_processed_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events
			(provider, event_id, type, object_ref, payload, processed_at)
		VALUES
			('stripe', $1, 'checkout.session.completed', $2, '{}'::jsonb, NULL),
			('stripe', $3, 'checkout.session.completed', $2, '{}'::jsonb, now())`,
		eventID, session, processedEventID); err != nil {
		t.Fatalf("record webhook event: %v", err)
	}

	conn, acquireErr := pool.Acquire(ctx)
	if acquireErr != nil {
		t.Fatalf("acquire role connection: %v", acquireErr)
	}
	defer conn.Release()
	defer func() {
		if _, resetErr := conn.Exec(context.WithoutCancel(ctx), `RESET ROLE`); resetErr != nil {
			t.Errorf("reset role: %v", resetErr)
		}
	}()
	requireDenied := func(t *testing.T, err error) {
		t.Helper()
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		if !ok || pgErr.Code != "42501" {
			t.Fatalf("operation returned %v, want SQLSTATE 42501 insufficient_privilege", err)
		}
	}

	if _, err := conn.Exec(ctx, `SET ROLE store`); err != nil {
		t.Fatalf("set store role: %v", err)
	}
	var marked bool
	if err := conn.QueryRow(ctx, `SELECT mark_payment_event_unreconciled($1, $2)`,
		eventID, "refused_capture: role boundary test").Scan(&marked); err != nil || !marked {
		t.Fatalf("store mark_payment_event_unreconciled = %v, %v; want true", marked, err)
	}
	if err := conn.QueryRow(ctx, `SELECT mark_payment_event_unreconciled($1, $2)`,
		processedEventID, "refused_capture: must not rewrite a completed outcome").Scan(&marked); err != nil {
		t.Fatalf("mark an already processed event: %v", err)
	} else if marked {
		t.Fatal("mark_payment_event_unreconciled rewrote an already processed clean event")
	}
	var processedReason *string
	if err := conn.QueryRow(ctx, `SELECT unreconciled FROM payment_webhook_events
		WHERE provider = 'stripe' AND event_id = $1`, processedEventID).Scan(&processedReason); err != nil {
		t.Fatalf("read already processed event: %v", err)
	}
	if processedReason != nil {
		t.Errorf("already processed event gained alarm %q", *processedReason)
	}
	if _, err := conn.Exec(ctx, `UPDATE payment_webhook_events SET processed_at = now()
		WHERE provider = 'stripe' AND event_id = $1`, eventID); err != nil {
		t.Fatalf("store cannot stamp processed_at: %v", err)
	}
	_, deniedErr := conn.Exec(ctx, `UPDATE payment_webhook_events SET unreconciled = NULL
		WHERE provider = 'stripe' AND event_id = $1`, eventID)
	requireDenied(t, deniedErr)
	_, deniedErr = conn.Exec(ctx, `UPDATE payment_webhook_events SET reconciled_at = now()
		WHERE provider = 'stripe' AND event_id = $1`, eventID)
	requireDenied(t, deniedErr)
	var reconciled bool
	deniedErr = conn.QueryRow(ctx, `SELECT release_payment_event($1)`, eventID).Scan(&reconciled)
	requireDenied(t, deniedErr)

	if _, err := conn.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatalf("reset store role: %v", err)
	}
	if _, err := conn.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatalf("switch to admin role: %v", err)
	}
	_, deniedErr = conn.Exec(ctx, `UPDATE payment_webhook_events SET reconciled_at = now()
		WHERE provider = 'stripe' AND event_id = $1`, eventID)
	requireDenied(t, deniedErr)
	deniedErr = conn.QueryRow(ctx, `SELECT mark_payment_event_unreconciled($1, $2)`,
		eventID, "admin must not rewrite the alarm").Scan(&marked)
	requireDenied(t, deniedErr)
	if err := conn.QueryRow(ctx, `SELECT release_payment_event($1)`, eventID).Scan(&reconciled); err != nil || !reconciled {
		t.Fatalf("admin release_payment_event = %v, %v; want true", reconciled, err)
	}
	if _, err := conn.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatalf("reset admin role: %v", err)
	}

	var status, reason string
	var wasReconciled bool
	if err := conn.QueryRow(ctx, `
		SELECT p.status, e.unreconciled, e.reconciled_at IS NOT NULL
		FROM payments p
		JOIN payment_webhook_events e
		  ON e.provider = p.provider AND e.object_ref = p.provider_ref
		WHERE e.provider = 'stripe' AND e.event_id = $1`, eventID).
		Scan(&status, &reason, &wasReconciled); err != nil {
		t.Fatalf("read reconciled state: %v", err)
	}
	if status != "cancelled" || reason != "refused_capture: role boundary test" || !wasReconciled {
		t.Errorf("reconciled state = %q/%q/%v, want cancelled/original reason/true",
			status, reason, wasReconciled)
	}
}

func TestCompletePaymentResolutionIsAdminOnly(t *testing.T) {
	ctx := t.Context()
	_, orderID := order(t, 105500)
	providerRef := "cs_complete_role_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx,
		`SELECT record_complete_payment($1, $2, 105500)`, orderID, providerRef); err != nil {
		t.Fatalf("record complete payment: %v", err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire role connection: %v", err)
	}
	defer conn.Release()
	defer func() {
		if _, resetErr := conn.Exec(context.WithoutCancel(ctx), `RESET ROLE`); resetErr != nil {
			t.Errorf("reset role: %v", resetErr)
		}
	}()

	if _, err := conn.Exec(ctx, `SET ROLE store`); err != nil {
		t.Fatalf("set store role: %v", err)
	}
	for _, statement := range []string{
		`SELECT release_complete_payment($1)`,
		`SELECT * FROM attribute_complete_payment_paid($1)`,
	} {
		_, roleErr := conn.Exec(ctx, statement, providerRef)
		pgErr, ok := errors.AsType[*pgconn.PgError](roleErr)
		if !ok || pgErr.Code != "42501" {
			t.Errorf("store %q = %v, want SQLSTATE 42501", statement, roleErr)
		}
	}

	if _, err := conn.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatalf("reset store role: %v", err)
	}
	if _, err := conn.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatalf("set admin role: %v", err)
	}
	var released bool
	if err := conn.QueryRow(ctx, `SELECT release_complete_payment($1)`, providerRef).
		Scan(&released); err != nil || !released {
		t.Fatalf("admin release_complete_payment = %v, %v; want true", released, err)
	}

	if _, err := conn.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatalf("reset admin role: %v", err)
	}
	var status string
	if err := conn.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, providerRef).Scan(&status); err != nil {
		t.Fatalf("read complete payment: %v", err)
	}
	if status != "reconciled" {
		t.Errorf("admin resolution left status %q, want reconciled", status)
	}
}

// waitForSQLLock makes a concurrent overlap observable at the
// database, instead of treating "the goroutine started" as evidence that its
// statement reached the expected row or advisory lock.
func waitForSQLLock(t *testing.T, ctx context.Context, returned <-chan struct{}, queryPattern string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-returned:
			t.Fatal("the concurrent operation returned before it waited on the expected database lock")
		case <-tick.C:
			var waiting bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM pg_stat_activity
					WHERE pid <> pg_backend_pid()
					  AND datname = current_database()
					  AND wait_event_type = 'Lock'
					  AND query LIKE $1
				)`, queryPattern).Scan(&waiting); err != nil {
				t.Fatalf("observe the concurrent lock wait: %v", err)
			}
			if waiting {
				return
			}
		case <-deadline.C:
			t.Fatalf("the concurrent query %q never became a database lock wait", queryPattern)
		}
	}
}

// TestWebhookAndOpenShareProviderReferenceLock proves both outcomes at the
// network/database boundary. Whichever transaction owns the Stripe reference
// first becomes visible in full before the other can decide whether the
// reference has a local payment identity.
func TestWebhookAndOpenShareProviderReferenceLock(t *testing.T) {
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
					SessionID: session, AmountRecv: 92000,
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

func TestACancelledOrderLeavesNoSessionUnclosed(t *testing.T) {
	ctx := t.Context()

	t.Run("opening wins and cancellation returns its session", func(t *testing.T) {
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
			sessions []string
			err      error
		}
		returned := make(chan struct{})
		done := make(chan cancelResult, 1)
		go func() {
			defer close(returned)
			sessions, cancelErr := cart.NewStore(pool).Cancel(ctx, number)
			done <- cancelResult{sessions: sessions, err: cancelErr}
		}()
		waitForSQLLock(t, ctx, returned,
			"%UPDATE orders SET fulfillment_status = 'cancelled'%")

		if commitErr := tx1.Commit(ctx); commitErr != nil {
			t.Fatalf("commit opening transaction: %v", commitErr)
		}
		var result cancelResult
		select {
		case result = <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Cancel stayed blocked after open_payment committed")
		}
		if result.err != nil {
			t.Fatalf("Cancel: %v", result.err)
		}
		containsSession := false
		for _, returned := range result.sessions {
			if returned == session {
				containsSession = true
				break
			}
		}
		if !containsSession {
			t.Errorf("Cancel returned sessions %v, want the concurrently opened %q", result.sessions, session)
		}

		rows, err := pool.Query(ctx, `
			SELECT p.provider_ref
			FROM payments p
			WHERE p.order_id = $1 AND p.status = 'requires_payment'`, id)
		if err != nil {
			t.Fatalf("read sessions left open after cancellation: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var open string
			if err := rows.Scan(&open); err != nil {
				t.Fatalf("scan open session: %v", err)
			}
			returned := false
			for _, candidate := range result.sessions {
				if candidate == open {
					returned = true
					break
				}
			}
			if !returned {
				t.Errorf("requires_payment session %q was not in Cancel's expire-list %v", open, result.sessions)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("walk open sessions: %v", err)
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

	late := hold(t, id, 0, 45*time.Minute, "late:"+number)
	early := hold(t, id, 1, 35*time.Minute, "early:"+number)
	if !early.Before(late) {
		t.Fatalf("fixture expiry order is early=%v, late=%v", early, late)
	}

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
}

func TestHoldAdmissionUsesTheDatabaseTransactionClock(t *testing.T) {
	ctx := t.Context()
	_, orderID := order(t, 10000)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin clock transaction: %v", err)
	}
	cleanupCtx := context.WithoutCancel(t.Context())
	defer func() { _ = tx.Rollback(cleanupCtx) }()

	expires := hold(t, orderID, 0, 150*time.Millisecond, "db-clock:"+uuid.NewString())
	time.Sleep(250 * time.Millisecond)
	if !expires.Before(time.Now().Add(50 * time.Millisecond)) {
		t.Fatal("fixture did not cross the process-clock admission boundary")
	}
	row, err := db.New(tx).OrderHoldExpiry(ctx, db.OrderHoldExpiryParams{
		OrderID: orderID,
		RequiredLifetime: pgtype.Interval{
			Microseconds: (50 * time.Millisecond).Microseconds(), Valid: true,
		},
	})
	if err != nil {
		t.Fatalf("read hold against transaction clock: %v", err)
	}
	if !row.CoversSession {
		t.Fatal("hold admission was recomputed against the process clock instead of DB now()")
	}
}

// hold reserves one unit of the nth-largest-stock variant for an order.
func hold(t *testing.T, orderID uuid.UUID, nth int, forDuration time.Duration, key string) time.Time {
	t.Helper()
	var reservationID uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		SELECT hold_inventory($1,
			(SELECT id FROM product_variants ORDER BY stock_quantity DESC, id LIMIT 1 OFFSET $2),
			1, $3::interval, $4)`, orderID, nth, pgtype.Interval{
		Microseconds: int64(forDuration / time.Microsecond), Valid: true,
	}, key).Scan(&reservationID); err != nil {
		t.Fatalf("hold stock: %v", err)
	}
	var expiresAt time.Time
	if err := pool.QueryRow(t.Context(),
		`SELECT expires_at FROM inventory_reservations WHERE id = $1`, reservationID).
		Scan(&expiresAt); err != nil {
		t.Fatalf("read hold expiry: %v", err)
	}
	return expiresAt
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
	if _, err := captureThroughWebhook(t, s, payment.Capture{
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
		_, err := captureThroughWebhook(t, s, payment.Capture{SessionID: session, AmountRecv: 99900})
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

// TestConcurrentCapturesSerializeTierAwards holds the per-member lock between
// two different paid orders. Without it, both award statements can read the
// same pre-payment spend and mint at the base rate even though one payment must
// precede the other.
func TestConcurrentCapturesSerializeTierAwards(t *testing.T) {
	ctx := t.Context()
	const orderCents = int64(1200000) // NT$12,000.

	// Crossing NT$11,000 promotes the member to 2x on the next order. Reuse the
	// same exact band as the sequential tier test so the unique spend boundary
	// remains valid whether this test is run alone or with the whole package.
	if _, err := pool.Exec(ctx, `
		INSERT INTO membership_tiers (code, name, min_spend_cents, points_multiplier_bp, position)
		VALUES ('tiertest', '測試等級', 1100000, 20000, 9)
		ON CONFLICT (code) DO NOTHING`); err != nil {
		t.Fatalf("create the tier: %v", err)
	}

	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email)
		VALUES ('concurrent-tier-' || gen_random_uuid() || '@goen.invalid')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}

	firstNumber, firstOrderID := ownedOrder(t, userID, orderCents)
	secondNumber, secondOrderID := ownedOrder(t, userID, orderCents)
	firstSession := "cs_tier_concurrent_first_" + uuid.NewString()[:12]
	secondSession := "cs_tier_concurrent_second_" + uuid.NewString()[:12]
	s := payment.NewStore(pool)
	if err := s.OpenPayment(ctx, firstNumber, firstSession, orderCents); err != nil {
		t.Fatalf("open first payment: %v", err)
	}
	if err := s.OpenPayment(ctx, secondNumber, secondSession, orderCents); err != nil {
		t.Fatalf("open second payment: %v", err)
	}

	firstTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin first capture: %v", err)
	}
	cleanupCtx := context.WithoutCancel(ctx)
	defer func() { _ = firstTx.Rollback(cleanupCtx) }()
	if _, err := firstTx.Exec(ctx,
		`SELECT capture_payment($1, $2::bigint, NULL, NULL)`, firstSession, orderCents); err != nil {
		t.Fatalf("post first capture: %v", err)
	}
	var firstPoints int64
	if err := firstTx.QueryRow(ctx,
		`SELECT award_loyalty_points($1)`, firstOrderID).Scan(&firstPoints); err != nil {
		t.Fatalf("award first capture: %v", err)
	}
	if firstPoints != 120 {
		t.Fatalf("first concurrent order awarded %d points, want the base 120", firstPoints)
	}

	type captureResult struct {
		points int64
		err    error
	}
	returned := make(chan struct{})
	done := make(chan captureResult, 1)
	go func() {
		defer close(returned)
		secondTx, beginErr := pool.Begin(ctx)
		if beginErr != nil {
			done <- captureResult{err: fmt.Errorf("begin second capture: %w", beginErr)}
			return
		}
		defer func() { _ = secondTx.Rollback(cleanupCtx) }()
		if _, captureErr := secondTx.Exec(ctx,
			`SELECT capture_payment($1, $2::bigint, NULL, NULL)`, secondSession, orderCents); captureErr != nil {
			done <- captureResult{err: fmt.Errorf("post second capture: %w", captureErr)}
			return
		}
		var points int64
		if awardErr := secondTx.QueryRow(ctx,
			`SELECT award_loyalty_points($1)`, secondOrderID).Scan(&points); awardErr != nil {
			done <- captureResult{err: fmt.Errorf("award second capture: %w", awardErr)}
			return
		}
		if commitErr := secondTx.Commit(ctx); commitErr != nil {
			done <- captureResult{err: fmt.Errorf("commit second capture: %w", commitErr)}
			return
		}
		done <- captureResult{points: points}
	}()

	// Distinct orders and payment rows share no capture lock. Seeing this query
	// wait proves the overlap reached award_loyalty_points and serialized on the
	// member account created by the still-uncommitted first award.
	waitForSQLLock(t, ctx, returned, "%SELECT award_loyalty_points(%")
	if err := firstTx.Commit(ctx); err != nil {
		t.Fatalf("commit first capture: %v", err)
	}

	var second captureResult
	select {
	case second = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second award stayed blocked after the first member award committed")
	}
	if second.err != nil {
		t.Fatalf("second capture transaction: %v", second.err)
	}
	if second.points != 240 {
		t.Errorf("second concurrent order awarded %d points, want 240 at 2x", second.points)
	}

	var totalPoints int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(points), 0)
		FROM loyalty_entries
		WHERE order_id = ANY($1::uuid[])`, []uuid.UUID{firstOrderID, secondOrderID}).
		Scan(&totalPoints); err != nil {
		t.Fatalf("read concurrent awards: %v", err)
	}
	if totalPoints != 360 {
		t.Errorf("concurrent orders awarded %d points total, want one base 120 plus one 2x 240", totalPoints)
	}
}

// ownedOrder places an order for a signed-in customer without opening payment.
func ownedOrder(t *testing.T, userID uuid.UUID, cents int64) (number string, orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

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
	return number, orderID
}

// payForOwnedOrder places and captures an order, and reports the points awarded.
func payForOwnedOrder(t *testing.T, s *payment.Store, userID uuid.UUID, cents int64, session string) int64 {
	t.Helper()
	ctx := t.Context()
	number, orderID := ownedOrder(t, userID, cents)

	if err := s.OpenPayment(ctx, number, session, cents); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := captureThroughWebhook(t, s, payment.Capture{SessionID: session, AmountRecv: cents}); err != nil {
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

// TestAnOrderThatEarnsNothingIsStillCaptured is the customer-order sibling of
// loyalty's guest-order precedent: neither kind of zero award may abort money
// that Stripe has already captured.
func TestAnOrderThatEarnsNothingIsStillCaptured(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role) VALUES ('zero-' || gen_random_uuid() || '@goen.invalid', 'customer')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	number, orderID := ownedOrder(t, userID, 4900)
	session := "cs_zero_" + number
	if err := s.OpenPayment(ctx, number, session, 4900); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := captureThroughWebhook(t, s, payment.Capture{SessionID: session, AmountRecv: 4900}); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			if pgErr.ConstraintName != "loyalty_entries_points_nonzero" {
				t.Fatalf("capture failed at constraint %q, want loyalty_entries_points_nonzero: %v",
					pgErr.ConstraintName, err)
			}
			t.Fatalf("capture hit loyalty_entries_points_nonzero; a legitimate zero award aborted paid money: %v", err)
		}
		t.Fatalf("capture: %v", err)
	}

	var paid bool
	if err := pool.QueryRow(ctx, `SELECT order_is_committed($1)`, orderID).Scan(&paid); err != nil {
		t.Fatalf("read paid state: %v", err)
	}
	if !paid {
		t.Error("the paid order is not committed")
	}
	for _, assertion := range []struct {
		name string
		sql  string
		args []any
		want int
	}{
		{"paid event", `SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'paid'`, []any{orderID}, 1},
		{"receipt", `SELECT count(*) FROM outbox_messages WHERE topic = 'order.paid' AND dedupe_key = $1`, []any{number}, 1},
		{"loyalty award", `SELECT count(*) FROM loyalty_entries WHERE order_id = $1`, []any{orderID}, 0},
	} {
		var got int
		if err := pool.QueryRow(ctx, assertion.sql, assertion.args...).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", assertion.name, err)
		}
		if got != assertion.want {
			t.Errorf("%s rows = %d, want %d", assertion.name, got, assertion.want)
		}
	}
}

// TestTheAwardedPointsUseWholeHundreds binds every integer boundary to the
// database's single production definition.
func TestTheAwardedPointsUseWholeHundreds(t *testing.T) {
	for _, cents := range []int64{4999, 5000, 9999, 10000, 19999, 100000, 2590000} {
		t.Run(strconv.FormatInt(cents, 10), func(t *testing.T) {
			var userID uuid.UUID
			if err := pool.QueryRow(t.Context(), `
				INSERT INTO users (email, role)
				VALUES ('boundary-' || gen_random_uuid() || '@goen.invalid', 'customer')
				RETURNING id`).Scan(&userID); err != nil {
				t.Fatalf("create customer: %v", err)
			}
			s := payment.NewStore(pool)
			got := payForOwnedOrder(t, s, userID, cents, fmt.Sprintf("cs_boundary_%d_%s", cents, userID))
			if want := cents / 10000; got != want {
				t.Errorf("%d cents awarded %d points, want %d", cents, got, want)
			}
		})
	}
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

// gatewayRecordingCalls gives an external-package integration test a real
// Gateway whose SDK backend is a local server. The global backend is restored
// before this helper returns; the client retains the injected backend.
func gatewayRecordingCalls(t *testing.T, sessionID string) (gateway *payment.Gateway, calls *int) {
	t.Helper()
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions" {
			callCount++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w,
			`{"id":%q,"object":"checkout.session","status":"open",`+
				`"url":"https://checkout.stripe.com/c/pay/%s"}`,
			sessionID, sessionID)
	}))
	t.Cleanup(srv.Close)

	original := stripe.GetBackend(stripe.APIBackend)
	noRetries := int64(0)
	backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL: stripe.String(srv.URL), MaxNetworkRetries: &noRetries,
	})
	stripe.SetBackend(stripe.APIBackend, backend)
	defer stripe.SetBackend(stripe.APIBackend, original)

	gateway, err := payment.NewGateway("sk_test_notreal", testWebhookSecret, "https://goen.example")
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	return gateway, &callCount
}

// TestPaidCompleteResolutionCannotOpenSecondSession covers the irreversible
// branch of the operator door. A complete Session confirmed paid is captured
// through the same database invariants and side effects as a signed webhook;
// Start must then stop before making any second Stripe Session.
func TestPaidCompleteResolutionCannotOpenSecondSession(t *testing.T) {
	ctx := t.Context()
	const amount = int64(125000)

	var customerID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role)
		VALUES ('complete-paid-' || gen_random_uuid() || '@goen.invalid', 'customer')
		RETURNING id`).Scan(&customerID); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	number, orderID := ownedOrder(t, customerID, amount)
	hold(t, orderID, 0, 60*time.Minute, "complete-paid:"+number)
	providerRef := "cs_complete_paid_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx,
		`SELECT record_complete_payment($1, $2, $3)`, orderID, providerRef, amount); err != nil {
		t.Fatalf("record complete payment: %v", err)
	}
	backOffice := admin.NewStore(pool, admin.NewRefunder(""), nil, nil)
	if err := backOffice.ReconcileCompletePayment(
		ctx, providerRef, admin.CompletePaymentPaid,
	); !errors.Is(err, admin.ErrNoActor) {
		t.Fatalf("paid attribution without an auditable actor = %v, want ErrNoActor", err)
	}
	var rolledBackStatus string
	var rolledBackEffects int64
	if err := pool.QueryRow(ctx, `
		SELECT p.status,
		       (SELECT count(*) FROM order_events e
		        WHERE e.order_id = p.order_id AND e.kind = 'paid')
		FROM payments p WHERE p.provider_ref = $1`, providerRef).
		Scan(&rolledBackStatus, &rolledBackEffects); err != nil {
		t.Fatalf("read failed attribution transaction: %v", err)
	}
	if rolledBackStatus != "requires_reconciliation" || rolledBackEffects != 0 {
		t.Fatalf("failed audit left capture/effects = %q/%d, want requires_reconciliation/0",
			rolledBackStatus, rolledBackEffects)
	}

	var actorID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role)
		VALUES ('complete-operator-' || gen_random_uuid() || '@goen.invalid', 'admin')
		RETURNING id`).Scan(&actorID); err != nil {
		t.Fatalf("create operator: %v", err)
	}
	adminCtx := account.WithUser(ctx, account.User{ID: actorID.String(), Role: "admin"})
	if err := backOffice.ReconcileCompletePayment(
		adminCtx, providerRef, admin.CompletePaymentPaid,
	); err != nil {
		t.Fatalf("attribute complete payment as paid: %v", err)
	}

	var status string
	var captured int64
	if err := pool.QueryRow(ctx, `
		SELECT status, captured_amount_cents
		FROM payments WHERE provider_ref = $1`, providerRef).Scan(&status, &captured); err != nil {
		t.Fatalf("read attributed payment: %v", err)
	}
	if status != "succeeded" || captured != amount {
		t.Fatalf("attributed payment = %q/%d, want succeeded/%d", status, captured, amount)
	}

	assertSideEffects := func() {
		t.Helper()
		for _, assertion := range []struct {
			name string
			sql  string
			args []any
			want int64
		}{
			{"paid timeline event", `SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'paid'`, []any{orderID}, 1},
			{"paid receipt", `SELECT count(*) FROM outbox_messages WHERE topic = 'order.paid' AND dedupe_key = $1`, []any{number}, 1},
			{"loyalty points", `SELECT coalesce(sum(points), 0) FROM loyalty_entries WHERE order_id = $1`, []any{orderID}, amount / 10000},
		} {
			var got int64
			if err := pool.QueryRow(ctx, assertion.sql, assertion.args...).Scan(&got); err != nil {
				t.Fatalf("read %s: %v", assertion.name, err)
			}
			if got != assertion.want {
				t.Errorf("%s = %d, want %d", assertion.name, got, assertion.want)
			}
		}
	}
	assertSideEffects()

	var auditResolution string
	if err := pool.QueryRow(ctx, `
		SELECT after->>'resolution' FROM audit_events
		WHERE action = 'payment.reconciled' AND after->>'provider_ref' = $1
		ORDER BY occurred_at DESC LIMIT 1`, providerRef).Scan(&auditResolution); err != nil {
		t.Fatalf("read paid attribution audit: %v", err)
	}
	if auditResolution != "paid_attributed" {
		t.Errorf("audit resolution = %q, want paid_attributed", auditResolution)
	}

	gateway, createCalls := gatewayRecordingCalls(t, "cs_must_not_open")
	h := payment.NewHandler(payment.NewStore(pool), gateway, alwaysPlacedHere{},
		slog.New(slog.DiscardHandler), false)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/"+number+"/pay", http.NoBody)
	req.SetPathValue("number", number)
	res := httptest.NewRecorder()
	h.Start(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/orders/"+number {
		t.Fatalf("Start after paid attribution = %d %q, want 303 to order",
			res.Code, res.Header().Get("Location"))
	}
	if *createCalls != 0 {
		t.Fatalf("Start created %d Stripe Sessions after paid attribution, want 0", *createCalls)
	}

	// A later signed paid event is still processed, but manual attribution and
	// its effects are idempotent under the shared provider-reference lock.
	if _, err := captureThroughWebhook(t, payment.NewStore(pool), payment.Capture{
		SessionID: providerRef, AmountRecv: amount,
	}); err != nil {
		t.Fatalf("late paid webhook: %v", err)
	}
	assertSideEffects()
}

// TestAdmittedCompleteSessionsBecomeVisibleAndResolvable covers both ways an
// already-admitted Checkout can be complete before its webhook arrives. The
// matching-amount resume and obsolete-amount retirement must both consume the
// original intent as requires_reconciliation, appear in admin health, and only
// advance a generation after the explicit unpaid/refunded outcome.
func TestAdmittedCompleteSessionsBecomeVisibleAndResolvable(t *testing.T) {
	for _, tt := range []struct {
		name            string
		shippingCents   int64
		firstHTTPStatus int
	}{
		{name: "matching amount", firstHTTPStatus: http.StatusSeeOther},
		{name: "obsolete amount", shippingCents: 30000, firstHTTPStatus: http.StatusConflict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			const originalAmount = int64(100000)
			number, orderID := order(t, originalAmount)
			hold(t, orderID, 0, 60*time.Minute, "admitted-complete:"+number)
			oldSession := "cs_admitted_complete_" + uuid.NewString()[:12]
			s := payment.NewStore(pool)
			if err := s.OpenPayment(ctx, number, oldSession, originalAmount); err != nil {
				t.Fatalf("open admitted payment: %v", err)
			}
			if tt.shippingCents != 0 {
				if _, err := pool.Exec(ctx,
					`UPDATE orders SET shipping_cents = $2 WHERE id = $1`, orderID, tt.shippingCents); err != nil {
					t.Fatalf("change amount owed: %v", err)
				}
			}
			currentAmount := originalAmount + tt.shippingCents
			replacement := "cs_after_complete_" + uuid.NewString()[:12]

			var remote struct {
				mu         sync.Mutex
				createKeys []string
				oldReads   int
				newReads   int
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/"+oldSession:
					remote.mu.Lock()
					remote.oldReads++
					remote.mu.Unlock()
					_, _ = fmt.Fprintf(w,
						`{"id":%q,"object":"checkout.session","status":"complete",`+
							`"payment_status":"unpaid"}`, oldSession)
				case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
					remote.mu.Lock()
					remote.createKeys = append(remote.createKeys, r.Header.Get("Idempotency-Key"))
					remote.mu.Unlock()
					_, _ = fmt.Fprintf(w,
						`{"id":%q,"object":"checkout.session","status":"open",`+
							`"url":"https://checkout.stripe.com/c/pay/%s"}`, replacement, replacement)
				case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/"+replacement:
					remote.mu.Lock()
					remote.newReads++
					remote.mu.Unlock()
					_, _ = fmt.Fprintf(w,
						`{"id":%q,"object":"checkout.session","status":"open",`+
							`"url":"https://checkout.stripe.com/c/pay/%s"}`, replacement, replacement)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)

			originalBackend := stripe.GetBackend(stripe.APIBackend)
			noRetries := int64(0)
			stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(
				stripe.APIBackend,
				&stripe.BackendConfig{URL: stripe.String(srv.URL), MaxNetworkRetries: &noRetries},
			))
			gateway, err := payment.NewGateway(
				"sk_test_notreal", testWebhookSecret, "https://goen.example",
			)
			stripe.SetBackend(stripe.APIBackend, originalBackend)
			if err != nil {
				t.Fatalf("gateway: %v", err)
			}
			h := payment.NewHandler(s, gateway, alwaysPlacedHere{},
				slog.New(slog.DiscardHandler), false)
			post := func() *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequestWithContext(ctx, http.MethodPost,
					"/orders/"+number+"/pay", http.NoBody)
				req.SetPathValue("number", number)
				res := httptest.NewRecorder()
				h.Start(res, req)
				return res
			}

			first := post()
			if first.Code != tt.firstHTTPStatus {
				t.Fatalf("first Start = %d, want %d", first.Code, tt.firstHTTPStatus)
			}
			remote.mu.Lock()
			firstCreates, oldReads := len(remote.createKeys), remote.oldReads
			remote.mu.Unlock()
			if firstCreates != 0 || oldReads != 1 {
				t.Fatalf("complete admission called create/read %d/%d, want 0/1", firstCreates, oldReads)
			}

			var oldStatus string
			var oldIntent int64
			if queryErr := pool.QueryRow(ctx, `
				SELECT status, intended_amount_cents FROM payments WHERE provider_ref = $1`,
				oldSession).Scan(&oldStatus, &oldIntent); queryErr != nil {
				t.Fatalf("read durable complete attempt: %v", queryErr)
			}
			if oldStatus != "requires_reconciliation" || oldIntent != originalAmount {
				t.Fatalf("durable complete attempt = %q/%d, want requires_reconciliation/%d",
					oldStatus, oldIntent, originalAmount)
			}

			backOffice := admin.NewStore(pool, admin.NewRefunder(""), nil, nil)
			health, err := backOffice.WorkerHealth(
				ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)),
			)
			if err != nil {
				t.Fatalf("read health: %v", err)
			}
			visible := false
			for _, issue := range health.UnreconciledCompletePayments {
				if issue.ProviderRef == oldSession && issue.OrderNumber == number {
					visible = true
				}
			}
			if !visible {
				t.Fatalf("health did not expose complete attempt %s for %s", oldSession, number)
			}

			var actorID uuid.UUID
			if err := pool.QueryRow(ctx, `
				INSERT INTO users (email, role)
				VALUES ('complete-release-' || gen_random_uuid() || '@goen.invalid', 'admin')
				RETURNING id`).Scan(&actorID); err != nil {
				t.Fatalf("create operator: %v", err)
			}
			adminCtx := account.WithUser(ctx, account.User{ID: actorID.String(), Role: "admin"})
			if err := backOffice.ReconcileCompletePayment(
				adminCtx, oldSession, admin.CompletePaymentUnpaidOrRefunded,
			); err != nil {
				t.Fatalf("release complete attempt: %v", err)
			}

			second := post()
			wantLocation := "https://checkout.stripe.com/c/pay/" + replacement
			if second.Code != http.StatusSeeOther || second.Header().Get("Location") != wantLocation {
				t.Fatalf("Start after safe resolution = %d %q, want 303 %q",
					second.Code, second.Header().Get("Location"), wantLocation)
			}
			remote.mu.Lock()
			keys := append([]string(nil), remote.createKeys...)
			newReads := remote.newReads
			remote.mu.Unlock()
			if diff := cmp.Diff(
				[]string{payment.SessionKey(number, currentAmount, 1)}, keys,
			); diff != "" {
				t.Errorf("fresh generation key (-want +got):\n%s", diff)
			}
			if newReads != 1 {
				t.Errorf("replacement Session read %d times, want 1", newReads)
			}
		})
	}
}

// TestPaymentReconciliationPinsExpiredStock serializes the stock consequence
// with the money outcome. An expired hold cannot return to the shelf while a
// complete Session may be paid; paid attribution commits it with stock still
// held, while the explicit unpaid/refunded outcome makes release legal again.
func TestPaymentReconciliationPinsExpiredStock(t *testing.T) {
	assertNamedConstraint := func(t *testing.T, want string, err error) {
		t.Helper()
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		if !ok || pgErr.ConstraintName != want {
			t.Fatalf("error = %v, want constraint %s", err, want)
		}
	}
	for _, tt := range []struct {
		name             string
		resolution       admin.CompletePaymentResolution
		wantPayment      string
		wantReservation  string
		secondConstraint string
	}{
		{
			name: "paid attribution keeps stock for fulfilment", resolution: admin.CompletePaymentPaid,
			wantPayment: "succeeded", wantReservation: "held",
			secondConstraint: "inventory_reservation_committed_no_release",
		},
		{
			name: "unpaid or refunded releases stock", resolution: admin.CompletePaymentUnpaidOrRefunded,
			wantPayment: "reconciled", wantReservation: "released",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			const amount = int64(110000)
			number, orderID := order(t, amount)
			hold(t, orderID, 0, 60*time.Minute, "reconcile-stock:"+number)
			var reservationID uuid.UUID
			if err := pool.QueryRow(ctx, `
				UPDATE inventory_reservations
				SET created_at = now() - interval '2 hours',
				    expires_at = now() - interval '1 hour'
				WHERE order_id = $1 AND state = 'held'
				RETURNING id`, orderID).Scan(&reservationID); err != nil {
				t.Fatalf("expire reservation: %v", err)
			}
			providerRef := "cs_reconcile_stock_" + uuid.NewString()[:12]
			if _, err := pool.Exec(ctx,
				`SELECT record_complete_payment($1, $2, $3)`, orderID, providerRef, amount); err != nil {
				t.Fatalf("record complete payment: %v", err)
			}

			_, releaseErr := pool.Exec(ctx, `SELECT release_reservation($1)`, reservationID)
			assertNamedConstraint(t, "inventory_reservation_payment_reconciliation_no_release", releaseErr)
			if _, _, err := cart.NewStore(pool).Sweep(ctx, slog.New(slog.DiscardHandler)); err != nil {
				t.Fatalf("sweep during reconciliation: %v", err)
			}
			var state string
			if err := pool.QueryRow(ctx,
				`SELECT state FROM inventory_reservations WHERE id = $1`, reservationID).Scan(&state); err != nil {
				t.Fatalf("read pinned reservation: %v", err)
			}
			if state != "held" {
				t.Fatalf("reconciliation sweep changed reservation to %q, want held", state)
			}

			var actorID uuid.UUID
			if err := pool.QueryRow(ctx, `
				INSERT INTO users (email, role)
				VALUES ('stock-resolution-' || gen_random_uuid() || '@goen.invalid', 'admin')
				RETURNING id`).Scan(&actorID); err != nil {
				t.Fatalf("create operator: %v", err)
			}
			adminCtx := account.WithUser(ctx, account.User{ID: actorID.String(), Role: "admin"})
			if err := admin.NewStore(pool, admin.NewRefunder(""), nil, nil).
				ReconcileCompletePayment(adminCtx, providerRef, tt.resolution); err != nil {
				t.Fatalf("resolve complete payment: %v", err)
			}

			var paymentStatus string
			if err := pool.QueryRow(ctx,
				`SELECT status FROM payments WHERE provider_ref = $1`, providerRef).Scan(&paymentStatus); err != nil {
				t.Fatalf("read resolved payment: %v", err)
			}
			if paymentStatus != tt.wantPayment {
				t.Errorf("resolved payment = %q, want %q", paymentStatus, tt.wantPayment)
			}

			_, releaseErr = pool.Exec(ctx, `SELECT release_reservation($1)`, reservationID)
			if tt.secondConstraint != "" {
				assertNamedConstraint(t, tt.secondConstraint, releaseErr)
			} else if releaseErr != nil {
				t.Fatalf("release after safe resolution: %v", releaseErr)
			}
			if err := pool.QueryRow(ctx,
				`SELECT state FROM inventory_reservations WHERE id = $1`, reservationID).Scan(&state); err != nil {
				t.Fatalf("read resolved reservation: %v", err)
			}
			if state != tt.wantReservation {
				t.Errorf("resolved reservation = %q, want %q", state, tt.wantReservation)
			}
		})
	}
}

// TestReleasedStockMakesLateMoneyARefundCase fixes the other order-lock
// outcome from TestPaymentReconciliationPinsExpiredStock. A Session can become
// complete at Stripe before its deadline while the webhook/recovery write is
// delayed until after the sweeper. If release wins the order lock, neither the
// late webhook nor manual paid attribution may manufacture a paid order whose
// goods are already back on sale.
func TestReleasedStockMakesLateMoneyARefundCase(t *testing.T) {
	expireAndSweep := func(t *testing.T, orderID uuid.UUID) {
		t.Helper()
		ctx := t.Context()
		if _, err := pool.Exec(ctx, `
			UPDATE inventory_reservations
			SET created_at = now() - interval '2 hours',
			    expires_at = now() - interval '1 hour'
			WHERE order_id = $1 AND state = 'held'`, orderID); err != nil {
			t.Fatalf("expire reservation: %v", err)
		}
		if released, _, err := cart.NewStore(pool).Sweep(
			ctx, slog.New(slog.DiscardHandler),
		); err != nil || released < 1 {
			t.Fatalf("sweep before payment outcome = released %d, error %v; want at least 1, nil",
				released, err)
		}
		var state string
		if err := pool.QueryRow(ctx, `
			SELECT state FROM inventory_reservations WHERE order_id = $1
			ORDER BY created_at DESC LIMIT 1`, orderID).Scan(&state); err != nil {
			t.Fatalf("read swept reservation: %v", err)
		}
		if state != "released" {
			t.Fatalf("swept reservation = %q, want released", state)
		}
	}

	t.Run("late signed capture is durable and cannot reopen payment", func(t *testing.T) {
		ctx := t.Context()
		const amount = int64(117000)
		number, orderID := order(t, amount)
		hold(t, orderID, 0, 60*time.Minute, "late-stock-webhook:"+number)
		providerRef := "cs_late_stock_webhook_" + uuid.NewString()[:12]
		s := payment.NewStore(pool)
		if err := s.OpenPayment(ctx, number, providerRef, amount); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		expireAndSweep(t, orderID)

		eventID := "evt_late_stock_" + uuid.NewString()[:12]
		body, header := signed(t, typed(
			sessionEvent(eventID, providerRef, "paid", amount),
			"checkout.session.completed",
		))
		h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
			slog.New(slog.DiscardHandler), false)
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/webhooks/stripe", bytes.NewReader(body))
		req.Header.Set("Stripe-Signature", header)
		res := httptest.NewRecorder()
		h.Webhook(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("late Webhook status = %d, want 200 with a durable refund alarm", res.Code)
		}

		var status string
		var reason string
		var committed bool
		if err := pool.QueryRow(ctx, `
			SELECT p.status, e.unreconciled, order_is_committed(p.order_id)
			FROM payments p
			JOIN payment_webhook_events e
			  ON e.provider = p.provider AND e.object_ref = p.provider_ref
			WHERE p.provider_ref = $1 AND e.event_id = $2`, providerRef, eventID).
			Scan(&status, &reason, &committed); err != nil {
			t.Fatalf("read late capture outcome: %v", err)
		}
		if status != "requires_payment" || committed ||
			!strings.Contains(reason, "payments_capture_refuses_released_stock") {
			t.Fatalf("late capture = status %q, committed %v, reason %q; "+
				"want requires_payment/false/released-stock refusal", status, committed, reason)
		}

		gateway, calls := gatewayRecordingCalls(t, "cs_must_not_replace_"+uuid.NewString()[:12])
		start := payment.NewHandler(s, gateway, alwaysPlacedHere{},
			slog.New(slog.DiscardHandler), false)
		startReq := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/orders/"+number+"/pay", http.NoBody)
		startReq.SetPathValue("number", number)
		startRes := httptest.NewRecorder()
		start.Start(startRes, startReq)
		if startRes.Code != http.StatusConflict || *calls != 0 {
			t.Fatalf("Start after late released-stock capture = %d, Stripe creates %d; want 409/0",
				startRes.Code, *calls)
		}
	})

	t.Run("manual paid attribution is refused until staff refund and release", func(t *testing.T) {
		ctx := t.Context()
		const amount = int64(118000)
		number, orderID := order(t, amount)
		hold(t, orderID, 0, 60*time.Minute, "late-stock-admin:"+number)
		providerRef := "cs_late_stock_admin_" + uuid.NewString()[:12]
		s := payment.NewStore(pool)
		if err := s.OpenPayment(ctx, number, providerRef, amount); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		expireAndSweep(t, orderID)
		if _, err := pool.Exec(ctx,
			`SELECT record_complete_payment($1, $2, $3)`, orderID, providerRef, amount); err != nil {
			t.Fatalf("record delayed complete state: %v", err)
		}

		var actorID uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (email, role)
			VALUES ('late-stock-operator-' || gen_random_uuid() || '@goen.invalid', 'admin')
			RETURNING id`).Scan(&actorID); err != nil {
			t.Fatalf("create operator: %v", err)
		}
		adminCtx := account.WithUser(ctx, account.User{ID: actorID.String(), Role: "admin"})
		backOffice := admin.NewStore(pool, admin.NewRefunder(""), nil, nil)
		paidErr := backOffice.ReconcileCompletePayment(
			adminCtx, providerRef, admin.CompletePaymentPaid,
		)
		pgErr, ok := errors.AsType[*pgconn.PgError](paidErr)
		if !errors.Is(paidErr, admin.ErrPaymentRequiresRefund) || !ok ||
			pgErr.ConstraintName != "payments_capture_refuses_released_stock" {
			t.Fatalf("paid attribution after stock release = %v, want released-stock refusal", paidErr)
		}

		var status string
		var committed bool
		var effects int64
		if err := pool.QueryRow(ctx, `
			SELECT p.status, order_is_committed(p.order_id),
			       (SELECT count(*) FROM order_events e
			        WHERE e.order_id = p.order_id AND e.kind = 'paid')
			FROM payments p WHERE p.provider_ref = $1`, providerRef).
			Scan(&status, &committed, &effects); err != nil {
			t.Fatalf("read refused admin attribution: %v", err)
		}
		if status != "requires_reconciliation" || committed || effects != 0 {
			t.Fatalf("refused admin attribution = %q/committed %v/effects %d, "+
				"want requires_reconciliation/false/0", status, committed, effects)
		}
		health, err := backOffice.WorkerHealth(
			adminCtx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)),
		)
		if err != nil {
			t.Fatalf("read health after refused attribution: %v", err)
		}
		visibleAsRefundOnly := false
		for _, issue := range health.UnreconciledCompletePayments {
			if issue.ProviderRef == providerRef && !issue.PaidAttributionAllowed {
				visibleAsRefundOnly = true
			}
		}
		if !visibleAsRefundOnly {
			t.Fatal("released-stock payment was not exposed as refund-only in admin health")
		}

		// After an actual provider refund, this is the only permitted conclusion.
		if err := backOffice.ReconcileCompletePayment(
			adminCtx, providerRef, admin.CompletePaymentUnpaidOrRefunded,
		); err != nil {
			t.Fatalf("release after confirmed refund: %v", err)
		}
		if err := pool.QueryRow(ctx,
			`SELECT status FROM payments WHERE provider_ref = $1`, providerRef).Scan(&status); err != nil {
			t.Fatalf("read refunded resolution: %v", err)
		}
		if status != "reconciled" {
			t.Fatalf("refunded resolution = %q, want reconciled", status)
		}
	})
}

// TestACompleteSessionRejectedAfterItsWebhookAdvancesGeneration exercises the
// full distributed race: Stripe sends paid before Start can persist the Session,
// then returns that now-complete Session to Create. The seen-ref fence must not
// late-link it as payable, and complete must not be mislabeled expired. Its
// reconciliation row consumes attempt zero so the first post after operator
// resolution creates attempt one rather than replaying the same paid Session.
func TestACompleteSessionRejectedAfterItsWebhookAdvancesGeneration(t *testing.T) {
	ctx := t.Context()
	const amount = int64(125000)
	number, orderID := order(t, amount)
	hold(t, orderID, 0, 60*time.Minute, "complete-race:"+number)

	completeSession := "cs_complete_race_" + uuid.NewString()[:12]
	replacementSession := "cs_complete_replacement_" + uuid.NewString()[:12]
	eventID := "evt_complete_race_" + uuid.NewString()[:12]
	webhookBody, webhookHeader := signed(t, typed(
		sessionEvent(eventID, completeSession, "paid", amount),
		"checkout.session.completed",
	))

	var state struct {
		mu           sync.Mutex
		keys         []string
		delivered    bool
		expireCalls  int
		retrieveOld  int
		retrieveNext int
	}
	var h *payment.Handler
	handlerReady := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
			state.mu.Lock()
			state.keys = append(state.keys, r.Header.Get("Idempotency-Key"))
			first := len(state.keys) == 1
			deliver := first && !state.delivered
			state.delivered = state.delivered || deliver
			state.mu.Unlock()

			if deliver {
				<-handlerReady
				req := httptest.NewRequestWithContext(r.Context(), http.MethodPost,
					"/webhooks/stripe", bytes.NewReader(webhookBody))
				req.Header.Set("Stripe-Signature", webhookHeader)
				res := httptest.NewRecorder()
				h.Webhook(res, req)
				if res.Code != http.StatusOK {
					http.Error(w, "concurrent webhook failed", http.StatusInternalServerError)
					return
				}
			}

			id := replacementSession
			status := "open"
			if first {
				id = completeSession
				status = "complete"
			}
			_, _ = fmt.Fprintf(w,
				`{"id":%q,"object":"checkout.session","status":%q,`+
					`"payment_status":"paid","url":"https://checkout.stripe.com/c/pay/%s"}`,
				id, status, id)

		case r.Method == http.MethodPost &&
			strings.HasSuffix(r.URL.Path, "/"+completeSession+"/expire"):
			state.mu.Lock()
			state.expireCalls++
			state.mu.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error",` +
				`"message":"only an open Checkout Session can be expired"}}`))

		case r.Method == http.MethodGet &&
			strings.HasSuffix(r.URL.Path, "/"+completeSession):
			state.mu.Lock()
			state.retrieveOld++
			state.mu.Unlock()
			_, _ = fmt.Fprintf(w,
				`{"id":%q,"object":"checkout.session","status":"complete",`+
					`"payment_status":"paid"}`, completeSession)

		case r.Method == http.MethodGet &&
			strings.HasSuffix(r.URL.Path, "/"+replacementSession):
			state.mu.Lock()
			state.retrieveNext++
			state.mu.Unlock()
			_, _ = fmt.Fprintf(w,
				`{"id":%q,"object":"checkout.session","status":"open",`+
					`"url":"https://checkout.stripe.com/c/pay/%s"}`,
				replacementSession, replacementSession)

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	original := stripe.GetBackend(stripe.APIBackend)
	noRetries := int64(0)
	backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL: stripe.String(srv.URL), MaxNetworkRetries: &noRetries,
	})
	stripe.SetBackend(stripe.APIBackend, backend)
	gateway, err := payment.NewGateway("sk_test_notreal", testWebhookSecret, "https://goen.example")
	stripe.SetBackend(stripe.APIBackend, original)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	h = payment.NewHandler(payment.NewStore(pool), gateway, alwaysPlacedHere{},
		slog.New(slog.DiscardHandler), false)
	close(handlerReady)

	post := func() *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/orders/"+number+"/pay", http.NoBody)
		req.SetPathValue("number", number)
		res := httptest.NewRecorder()
		h.Start(res, req)
		return res
	}

	if res := post(); res.Code != http.StatusConflict {
		t.Fatalf("Start after webhook won race = %d, want 409", res.Code)
	}

	var completeStatus string
	var unresolved bool
	if err := pool.QueryRow(ctx, `
		SELECT p.status,
		       e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
		FROM payments p
		JOIN payment_webhook_events e
		  ON e.provider = p.provider AND e.object_ref = p.provider_ref
		WHERE p.provider_ref = $1 AND e.event_id = $2`, completeSession, eventID).
		Scan(&completeStatus, &unresolved); err != nil {
		t.Fatalf("read complete-session recovery: %v", err)
	}
	if completeStatus != "requires_reconciliation" || !unresolved {
		t.Fatalf("complete-session recovery = %q/unresolved %v, want requires_reconciliation/true",
			completeStatus, unresolved)
	}

	state.mu.Lock()
	firstKeys := append([]string(nil), state.keys...)
	expireCalls, retrieveOld := state.expireCalls, state.retrieveOld
	state.mu.Unlock()
	if diff := cmp.Diff([]string{payment.SessionKey(number, amount, 0)}, firstKeys); diff != "" {
		t.Errorf("first create keys (-want +got):\n%s", diff)
	}
	if expireCalls != 1 || retrieveOld != 1 {
		t.Errorf("complete recovery called expire/retrieve %d/%d times, want 1/1",
			expireCalls, retrieveOld)
	}

	var reconciled bool
	if err := pool.QueryRow(ctx, `SELECT release_payment_event($1)`, eventID).
		Scan(&reconciled); err != nil || !reconciled {
		t.Fatalf("reconcile complete Session = %v, %v; want true", reconciled, err)
	}

	res := post()
	wantLocation := "https://checkout.stripe.com/c/pay/" + replacementSession
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != wantLocation {
		t.Fatalf("Start after reconciliation = %d %q, want 303 %q",
			res.Code, res.Header().Get("Location"), wantLocation)
	}

	state.mu.Lock()
	keys := append([]string(nil), state.keys...)
	retrieveNext := state.retrieveNext
	state.mu.Unlock()
	wantKeys := []string{
		payment.SessionKey(number, amount, 0),
		payment.SessionKey(number, amount, 1),
	}
	if diff := cmp.Diff(wantKeys, keys); diff != "" {
		t.Errorf("create generations (-want +got):\n%s", diff)
	}
	if retrieveNext != 1 {
		t.Errorf("replacement Session was retrieved %d times, want 1", retrieveNext)
	}

	var oldStatus, newStatus string
	if err := pool.QueryRow(ctx, `
		SELECT old.status, fresh.status
		FROM payments old, payments fresh
		WHERE old.provider_ref = $1 AND fresh.provider_ref = $2`,
		completeSession, replacementSession).Scan(&oldStatus, &newStatus); err != nil {
		t.Fatalf("read payment generations: %v", err)
	}
	if oldStatus != "reconciled" || newStatus != "requires_payment" {
		t.Errorf("payment generations = %q/%q, want reconciled/requires_payment",
			oldStatus, newStatus)
	}
}

// TestAnExpiredRejectedSessionConsumesItsIdempotencyGeneration covers the
// distributed boundary where Stripe creates a Session but open_payment refuses
// it. Even if persisting the first expiry fails, a retry must observe Stripe's
// current expired state, cancel the local row it just recovered, and advance to
// a new idempotency generation rather than redirecting to the dead Session.
func TestAnExpiredRejectedSessionConsumesItsIdempotencyGeneration(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	const amount = int64(130000)
	number, orderID := order(t, amount)
	hold(t, orderID, 0, 60*time.Minute, "generation:"+number)

	oldSession := "cs_generation_old_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, oldSession, amount); err != nil {
		t.Fatalf("open old payment: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT cancel_payment($1)`, oldSession); err != nil {
		t.Fatalf("cancel old payment: %v", err)
	}

	firstSession := "cs_generation_first_" + uuid.NewString()[:12]
	secondSession := "cs_generation_second_" + uuid.NewString()[:12]
	eventID := "evt_generation_" + uuid.NewString()[:12]

	// Fail exactly the first cancelled INSERT made by record_expired_payment.
	// This models a database error after Stripe accepted the expiration; the
	// second request must still recover from Stripe's cached Session response.
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_reject_expired_tombstone_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_reject_expired_tombstone_insert_" + suffix}.Sanitize()
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF NEW.provider_ref = '%s' AND NEW.status = 'cancelled' THEN
				RAISE EXCEPTION 'forced expired tombstone failure'
					USING ERRCODE = 'check_violation',
					      CONSTRAINT = 'test_expired_tombstone_failure';
			END IF;
			RETURN NEW;
		END
		$body$;
		CREATE TRIGGER %s BEFORE INSERT ON payments
		FOR EACH ROW EXECUTE FUNCTION %s()`,
		functionName, firstSession, triggerName, functionName)); err != nil {
		t.Fatalf("install tombstone failure: %v", err)
	}
	rejectorInstalled := true
	dropRejector := func() {
		t.Helper()
		if !rejectorInstalled {
			return
		}
		if _, err := pool.Exec(context.WithoutCancel(ctx), fmt.Sprintf(
			`DROP TRIGGER %s ON payments; DROP FUNCTION %s()`, triggerName, functionName)); err != nil {
			t.Fatalf("remove tombstone failure: %v", err)
		}
		rejectorInstalled = false
	}
	defer dropRejector()

	type remoteSession struct {
		id     string
		status string
	}
	var remote struct {
		mu          sync.Mutex
		byKey       map[string]*remoteSession
		createKeys  []string
		markerAdded bool
		markerErr   error
	}
	remote.byKey = make(map[string]*remoteSession)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
			key := r.Header.Get("Idempotency-Key")
			remote.mu.Lock()
			session := remote.byKey[key]
			if session == nil {
				id := firstSession
				if len(remote.byKey) > 0 {
					id = secondSession
				}
				session = &remoteSession{id: id, status: "open"}
				remote.byKey[key] = session
			}
			remote.createKeys = append(remote.createKeys, key)
			addMarker := !remote.markerAdded
			remote.markerAdded = true
			remote.mu.Unlock()

			if addMarker {
				_, markerErr := pool.Exec(r.Context(), `
					INSERT INTO payment_webhook_events
						(provider, event_id, type, object_ref, payload, processed_at, unreconciled)
					VALUES ('stripe', $1, 'checkout.session.completed', $2,
					        '{}'::jsonb, now(), 'unreadable_event: generation test')`,
					eventID, oldSession)
				remote.mu.Lock()
				remote.markerErr = markerErr
				remote.mu.Unlock()
				if markerErr != nil {
					http.Error(w, markerErr.Error(), http.StatusInternalServerError)
					return
				}
			}
			// Stripe replays the original create body for the same key, even
			// after the underlying resource has become expired.
			_, _ = fmt.Fprintf(w,
				`{"id":%q,"object":"checkout.session","status":"open",`+
					`"url":"https://checkout.stripe.com/c/pay/%s"}`,
				session.id, session.id)

		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/expire"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/checkout/sessions/"), "/expire")
			remote.mu.Lock()
			knownID := ""
			for _, session := range remote.byKey {
				if session.id == id {
					session.status = "expired"
					knownID = session.id
				}
			}
			remote.mu.Unlock()
			if knownID == "" {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id": knownID, "object": "checkout.session", "status": "expired",
			})

		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/checkout/sessions/"):
			id := strings.TrimPrefix(r.URL.Path, "/v1/checkout/sessions/")
			remote.mu.Lock()
			var status string
			knownID := ""
			for _, session := range remote.byKey {
				if session.id == id {
					status = session.status
					knownID = session.id
					break
				}
			}
			remote.mu.Unlock()
			if status == "" {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id": knownID, "object": "checkout.session", "status": status,
				"url": "https://checkout.stripe.com/c/pay/" + knownID,
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	original := stripe.GetBackend(stripe.APIBackend)
	noRetries := int64(0)
	backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL: stripe.String(srv.URL), MaxNetworkRetries: &noRetries,
	})
	stripe.SetBackend(stripe.APIBackend, backend)
	gateway, err := payment.NewGateway("sk_test_notreal", testWebhookSecret, "https://goen.example")
	stripe.SetBackend(stripe.APIBackend, original)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	h := payment.NewHandler(s, gateway, alwaysPlacedHere{}, slog.New(slog.DiscardHandler), false)
	post := func() *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/orders/"+number+"/pay", http.NoBody)
		req.SetPathValue("number", number)
		res := httptest.NewRecorder()
		h.Start(res, req)
		return res
	}

	if res := post(); res.Code != http.StatusInternalServerError {
		t.Fatalf("first Start status = %d, want 500 after tombstone persistence failed", res.Code)
	}
	remote.mu.Lock()
	markerErr := remote.markerErr
	firstKeys := append([]string(nil), remote.createKeys...)
	remote.mu.Unlock()
	if markerErr != nil {
		t.Fatalf("install concurrent reconciliation marker: %v", markerErr)
	}
	if diff := cmp.Diff([]string{payment.SessionKey(number, amount, 1)}, firstKeys); diff != "" {
		t.Errorf("first create keys (-want +got):\n%s", diff)
	}
	var firstRows int
	if countErr := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE provider_ref = $1`, firstSession).Scan(&firstRows); countErr != nil {
		t.Fatalf("count failed tombstone rows: %v", countErr)
	}
	if firstRows != 0 {
		t.Fatalf("failed tombstone left %d rows, want 0 for the recovery path", firstRows)
	}

	dropRejector()
	var reconciled bool
	if reconcileErr := pool.QueryRow(ctx, `SELECT release_payment_event($1)`, eventID).Scan(&reconciled); reconcileErr != nil || !reconciled {
		t.Fatalf("reconcile generation marker = %v, %v; want true", reconciled, reconcileErr)
	}

	if res := post(); res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/orders/"+number {
		t.Fatalf("recovered expired Start = %d %q, want 303 back to order",
			res.Code, res.Header().Get("Location"))
	}
	var recoveredStatus string
	if statusErr := pool.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, firstSession).Scan(&recoveredStatus); statusErr != nil {
		t.Fatalf("read recovered expired payment: %v", statusErr)
	}
	if recoveredStatus != "cancelled" {
		t.Fatalf("recovered expired payment status = %q, want cancelled", recoveredStatus)
	}

	if res := post(); res.Code != http.StatusSeeOther ||
		!strings.HasPrefix(res.Header().Get("Location"), "https://checkout.stripe.com/") {
		t.Fatalf("replacement Start = %d %q, want 303 to a fresh Stripe Session",
			res.Code, res.Header().Get("Location"))
	}
	remote.mu.Lock()
	keys := append([]string(nil), remote.createKeys...)
	remote.mu.Unlock()
	wantKeys := []string{
		payment.SessionKey(number, amount, 1),
		payment.SessionKey(number, amount, 1),
		payment.SessionKey(number, amount, 2),
	}
	if diff := cmp.Diff(wantKeys, keys); diff != "" {
		t.Errorf("create idempotency generations (-want +got):\n%s", diff)
	}

	var statuses []string
	rows, err := pool.Query(ctx,
		`SELECT status FROM payments WHERE order_id = $1 ORDER BY created_at, id`, orderID)
	if err != nil {
		t.Fatalf("read payment generations: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			t.Fatalf("scan payment generation: %v", err)
		}
		statuses = append(statuses, status)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("walk payment generations: %v", err)
	}
	if diff := cmp.Diff([]string{"cancelled", "cancelled", "requires_payment"}, statuses); diff != "" {
		t.Errorf("payment generations (-want +got):\n%s", diff)
	}
}

// TestObsoleteSessionCleanupConvergesAfterALocalWriteFailure proves that a
// successful provider expiry is not followed by an endless sequence of
// "already expired" errors when the first local cancellation fails. The retry
// retrieves current provider state, records cancelled, and only then opens the
// replacement amount.
func TestObsoleteSessionCleanupConvergesAfterALocalWriteFailure(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, orderID := order(t, 100000)
	hold(t, orderID, 0, 60*time.Minute, "obsolete-cleanup:"+number)
	oldSession := "cs_obsolete_cleanup_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, oldSession, 100000); err != nil {
		t.Fatalf("open old amount: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET shipping_cents = 30000 WHERE id = $1`, orderID); err != nil {
		t.Fatalf("change amount owed: %v", err)
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_reject_expired_update_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_reject_expired_update_once_" + suffix}.Sanitize()
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF OLD.provider_ref = '%s' AND NEW.status = 'cancelled' THEN
				RAISE EXCEPTION 'forced local cancellation failure'
					USING ERRCODE = 'check_violation',
					      CONSTRAINT = 'test_local_cancellation_failure';
			END IF;
			RETURN NEW;
		END
		$body$;
		CREATE TRIGGER %s BEFORE UPDATE ON payments
		FOR EACH ROW EXECUTE FUNCTION %s()`,
		functionName, oldSession, triggerName, functionName)); err != nil {
		t.Fatalf("install local cancellation failure: %v", err)
	}
	rejectorInstalled := true
	dropRejector := func() {
		t.Helper()
		if !rejectorInstalled {
			return
		}
		if _, err := pool.Exec(context.WithoutCancel(ctx), fmt.Sprintf(
			`DROP TRIGGER %s ON payments; DROP FUNCTION %s()`, triggerName, functionName)); err != nil {
			t.Fatalf("remove local cancellation failure: %v", err)
		}
		rejectorInstalled = false
	}
	defer dropRejector()

	replacement := "cs_obsolete_replacement_" + uuid.NewString()[:12]
	var remote struct {
		mu          sync.Mutex
		oldStatus   string
		expireCalls int
		createKeys  []string
	}
	remote.oldStatus = "open"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/"+oldSession:
			remote.mu.Lock()
			status := remote.oldStatus
			remote.mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"id":%q,"object":"checkout.session","status":%q,`+
				`"url":"https://checkout.stripe.com/c/pay/%s"}`, oldSession, status, oldSession)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions/"+oldSession+"/expire":
			remote.mu.Lock()
			remote.oldStatus = "expired"
			remote.expireCalls++
			remote.mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"id":%q,"object":"checkout.session","status":"expired"}`, oldSession)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
			remote.mu.Lock()
			remote.createKeys = append(remote.createKeys, r.Header.Get("Idempotency-Key"))
			remote.mu.Unlock()
			_, _ = fmt.Fprintf(w,
				`{"id":%q,"object":"checkout.session","status":"open",`+
					`"url":"https://checkout.stripe.com/c/pay/%s"}`, replacement, replacement)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/"+replacement:
			_, _ = fmt.Fprintf(w,
				`{"id":%q,"object":"checkout.session","status":"open",`+
					`"url":"https://checkout.stripe.com/c/pay/%s"}`, replacement, replacement)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	original := stripe.GetBackend(stripe.APIBackend)
	noRetries := int64(0)
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(
		stripe.APIBackend,
		&stripe.BackendConfig{URL: stripe.String(srv.URL), MaxNetworkRetries: &noRetries},
	))
	gateway, err := payment.NewGateway("sk_test_notreal", testWebhookSecret, "https://goen.example")
	stripe.SetBackend(stripe.APIBackend, original)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	h := payment.NewHandler(s, gateway, alwaysPlacedHere{}, slog.New(slog.DiscardHandler), false)
	post := func() *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/orders/"+number+"/pay", http.NoBody)
		req.SetPathValue("number", number)
		res := httptest.NewRecorder()
		h.Start(res, req)
		return res
	}

	if res := post(); res.Code != http.StatusInternalServerError {
		t.Fatalf("first obsolete cleanup status = %d, want 500", res.Code)
	}
	dropRejector()
	if res := post(); res.Code != http.StatusSeeOther ||
		!strings.HasPrefix(res.Header().Get("Location"), "https://checkout.stripe.com/") {
		t.Fatalf("retry after local cleanup failure = %d %q, want fresh checkout",
			res.Code, res.Header().Get("Location"))
	}

	remote.mu.Lock()
	expireCalls := remote.expireCalls
	createKeys := append([]string(nil), remote.createKeys...)
	remote.mu.Unlock()
	if expireCalls != 1 {
		t.Errorf("Stripe expire calls = %d, want 1; retry should consume known expired state", expireCalls)
	}
	if diff := cmp.Diff([]string{payment.SessionKey(number, 130000, 1)}, createKeys); diff != "" {
		t.Errorf("replacement create keys (-want +got):\n%s", diff)
	}

	var statuses []string
	rows, err := pool.Query(ctx,
		`SELECT status FROM payments WHERE order_id = $1 ORDER BY created_at, id`, orderID)
	if err != nil {
		t.Fatalf("read obsolete cleanup attempts: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			t.Fatalf("scan obsolete cleanup attempt: %v", err)
		}
		statuses = append(statuses, status)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("walk obsolete cleanup attempts: %v", err)
	}
	if diff := cmp.Diff([]string{"cancelled", "requires_payment"}, statuses); diff != "" {
		t.Errorf("obsolete cleanup statuses (-want +got):\n%s", diff)
	}
}

// TestObsoleteSessionCleanupSurvivesAClientDisconnect holds the local record of
// a retirement against a browser that leaves while Stripe is closing the
// session. Stripe has already expired it by then, so a cancelled local write
// leaves goen offering a checkout that can never take money.
func TestObsoleteSessionCleanupSurvivesAClientDisconnect(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, orderID := order(t, 100000)
	hold(t, orderID, 0, 60*time.Minute, "obsolete-disconnect:"+number)
	oldSession := "cs_obsolete_disconnect_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, oldSession, 100000); err != nil {
		t.Fatalf("open old amount: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET shipping_cents = 30000 WHERE id = $1`, orderID); err != nil {
		t.Fatalf("change amount owed: %v", err)
	}

	// The disconnect lands after Stripe has applied the expiry and before its
	// reply is written, which is the only window in which the two records can
	// disagree.
	atExpire := make(chan struct{})
	proceed := make(chan struct{})
	var remote struct {
		mu          sync.Mutex
		status      string
		expireCalls int
	}
	remote.status = "open"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/"+oldSession:
			remote.mu.Lock()
			status := remote.status
			remote.mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"id":%q,"object":"checkout.session","status":%q,`+
				`"url":"https://checkout.stripe.com/c/pay/%s"}`, oldSession, status, oldSession)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions/"+oldSession+"/expire":
			remote.mu.Lock()
			remote.status = "expired"
			remote.expireCalls++
			remote.mu.Unlock()
			atExpire <- struct{}{}
			<-proceed
			_, _ = fmt.Fprintf(w, `{"id":%q,"object":"checkout.session","status":"expired"}`, oldSession)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	original := stripe.GetBackend(stripe.APIBackend)
	noRetries := int64(0)
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(
		stripe.APIBackend,
		&stripe.BackendConfig{URL: stripe.String(srv.URL), MaxNetworkRetries: &noRetries},
	))
	gateway, err := payment.NewGateway("sk_test_notreal", testWebhookSecret, "https://goen.example")
	stripe.SetBackend(stripe.APIBackend, original)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}

	h := payment.NewHandler(s, gateway, alwaysPlacedHere{}, slog.New(slog.DiscardHandler), false)
	reqCtx, disconnect := context.WithCancel(ctx)
	defer disconnect()
	req := httptest.NewRequestWithContext(reqCtx, http.MethodPost,
		"/orders/"+number+"/pay", http.NoBody)
	req.SetPathValue("number", number)
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		h.Start(httptest.NewRecorder(), req)
	}()

	<-atExpire
	disconnect()
	close(proceed)
	<-returned

	remote.mu.Lock()
	expireCalls := remote.expireCalls
	remote.mu.Unlock()
	if expireCalls != 1 {
		t.Fatalf("Stripe expire calls = %d, want 1", expireCalls)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, oldSession).Scan(&status); err != nil {
		t.Fatalf("read the retired attempt: %v", err)
	}
	if status != "cancelled" {
		t.Errorf("retired attempt status = %q, want %q; Stripe has already expired the session",
			status, "cancelled")
	}
}

// TestTheWebhookRoutesEachEventToItsEffect is the HTTP-level lock on the routing
// switch in handler.go.
func TestTheWebhookRoutesEachEventToItsEffect(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
		slog.New(slog.DiscardHandler), false)

	tests := []struct {
		name             string
		eventType        string
		payStatus        string
		apiVersion       string
		wantStatus       string
		wantPaid         bool
		wantUnreconciled string
	}{
		{
			name:      "an older-version paid checkout captures from its payload shape",
			eventType: "checkout.session.completed", payStatus: "paid",
			apiVersion: legacyWebhookAPIVersion, wantStatus: "succeeded", wantPaid: true,
		},
		{
			name:      "a delayed method that cleared captures too",
			eventType: "checkout.session.async_payment_succeeded",
			payStatus: "paid", wantStatus: "succeeded", wantPaid: true,
		},
		{
			// Money is in flight: the row stays open and the event is the alarm.
			name:             "a delayed method still in flight does neither",
			eventType:        "checkout.session.completed",
			payStatus:        "unpaid",
			wantStatus:       "requires_payment",
			wantUnreconciled: "unsettled_session: ",
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
			if tt.apiVersion != "" {
				ev["api_version"] = tt.apiVersion
			}
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
			// Unsettled completion is understood and still needs a person; the
			// other understood or ignored rows must not look unreadable.
			var seen int
			var reason *string
			if err := pool.QueryRow(ctx,
				`SELECT count(*), max(unreconciled) FROM payment_webhook_events WHERE event_id = $1`,
				eventID).Scan(&seen, &reason); err != nil {
				t.Fatalf("read event: %v", err)
			}
			if seen != 1 {
				t.Errorf("the event was recorded %d times, want once", seen)
			}
			switch {
			case tt.wantUnreconciled == "" && reason != nil:
				t.Errorf("the understood/ignored event was marked unreconciled as %q", *reason)
			case tt.wantUnreconciled != "" && (reason == nil || !strings.HasPrefix(*reason, tt.wantUnreconciled)):
				t.Errorf("unreconciled = %v, want prefix %q", reason, tt.wantUnreconciled)
			}
		})
	}
}

// TestAnUnsettledCompletedSessionIsRecordedForAPerson holds the difference
// between classifying a delayed-method completion and leaving a person
// something to act on. The payment stays open so a later
// async_payment_succeeded can still capture through the existing door.
func TestAnUnsettledCompletedSessionIsRecordedForAPerson(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	var logs bytes.Buffer
	h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
		slog.New(slog.NewTextHandler(&logs, nil)), false)

	number, _ := order(t, 67000)
	session := "cs_unsettled_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, session, 67000); err != nil {
		t.Fatalf("open the known payment: %v", err)
	}
	eventID := "evt_" + uuid.NewString()[:12]
	body, header := signed(t, sessionEvent(eventID, session, "unpaid", 67000))

	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/webhooks/stripe", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", header)
	w := httptest.NewRecorder()
	h.Webhook(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Webhook() status = %d, want 200 — Stripe must not retry a session that cannot settle here", w.Code)
	}

	var processedAt *time.Time
	var reason *string
	var objectRef string
	if err := pool.QueryRow(ctx, `
		SELECT processed_at, unreconciled, coalesce(object_ref, '')
		FROM payment_webhook_events
		WHERE provider = 'stripe' AND event_id = $1`, eventID).
		Scan(&processedAt, &reason, &objectRef); err != nil {
		t.Fatalf("read unsettled event: %v", err)
	}
	if processedAt == nil {
		t.Error("the unsettled event was not marked processed, so Stripe will retry identical bytes")
	}
	if reason == nil || !strings.HasPrefix(*reason, "unsettled_session: ") {
		t.Fatalf("unreconciled = %v, want durable unsettled_session cause", reason)
	}
	if objectRef != session {
		t.Errorf("object_ref = %q, want the known session %q", objectRef, session)
	}

	backOffice := admin.NewStore(pool, admin.NewRefunder(""), nil, nil)
	health, err := backOffice.WorkerHealth(
		ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)),
	)
	if err != nil {
		t.Fatalf("read health: %v", err)
	}
	var listed bool
	for _, event := range health.UnreconciledEvents {
		if event.EventID == eventID && event.Ref == session {
			listed = true
			break
		}
	}
	if !listed {
		t.Fatalf("health does not list unsettled session %q", session)
	}

	var paymentStatus string
	var captured *int64
	if err := pool.QueryRow(ctx, `
		SELECT status, captured_amount_cents FROM payments WHERE provider_ref = $1`, session).
		Scan(&paymentStatus, &captured); err != nil {
		t.Fatalf("read open payment: %v", err)
	}
	if paymentStatus != "requires_payment" || captured != nil {
		t.Errorf("unsettled completion left payment=%q captured=%v, want requires_payment/NULL",
			paymentStatus, captured)
	}
	if output := logs.String(); !strings.Contains(output, "level=ERROR") ||
		!strings.Contains(output, "delayed payment method") {
		t.Errorf("unsettled event log = %q, want ERROR naming the delayed method", output)
	}

	clearedID := "evt_" + uuid.NewString()[:12]
	clearedBody, clearedHeader := signed(t, typed(sessionEvent(clearedID, session, "paid", 67000),
		"checkout.session.async_payment_succeeded"))
	clearedReq := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/webhooks/stripe", bytes.NewReader(clearedBody))
	clearedReq.Header.Set("Stripe-Signature", clearedHeader)
	cleared := httptest.NewRecorder()
	h.Webhook(cleared, clearedReq)
	if cleared.Code != http.StatusOK {
		t.Fatalf("async_payment_succeeded status = %d, want 200", cleared.Code)
	}
	var clearedStatus string
	var clearedCaptured *int64
	if err := pool.QueryRow(ctx, `
		SELECT status, captured_amount_cents FROM payments WHERE provider_ref = $1`, session).
		Scan(&clearedStatus, &clearedCaptured); err != nil {
		t.Fatalf("read captured payment: %v", err)
	}
	if clearedStatus != "succeeded" || clearedCaptured == nil || *clearedCaptured != 67000 {
		t.Errorf("after async_payment_succeeded payment=%q captured=%v, want succeeded/67000",
			clearedStatus, clearedCaptured)
	}
	var stillFlagged *string
	if err := pool.QueryRow(ctx,
		`SELECT unreconciled FROM payment_webhook_events WHERE event_id = $1`,
		eventID).Scan(&stillFlagged); err != nil {
		t.Fatalf("reread unsettled event: %v", err)
	}
	if stillFlagged == nil || *stillFlagged != *reason {
		t.Errorf("later capture rewrote the unsettled alarm from %v to %v", reason, stillFlagged)
	}
}

// TestAnUnreadableKnownEventIsRecordedForAPerson drives the signature verifier,
// handler switch and durable alarm together. The payment row is real and open,
// so this cannot pass by accidentally taking the unattributed-capture branch.
func TestAnUnreadableKnownEventIsRecordedForAPerson(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	var logs bytes.Buffer
	h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
		slog.New(slog.NewTextHandler(&logs, nil)), false)

	number, _ := order(t, 67000)
	session := "cs_unreadable_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, session, 67000); err != nil {
		t.Fatalf("open the known payment: %v", err)
	}
	eventID := "evt_" + uuid.NewString()[:12]
	event := sessionField(sessionEvent(eventID, session, "paid", 67000),
		"amount_total", "67000")
	// A version mismatch is accepted, but acceptance does not make a malformed
	// actionable object readable. It must still become durable operator work.
	event["api_version"] = legacyWebhookAPIVersion
	body, header := signed(t, event)

	post := func() *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/webhooks/stripe", bytes.NewReader(body))
		req.Header.Set("Stripe-Signature", header)
		w := httptest.NewRecorder()
		h.Webhook(w, req)
		return w
	}
	if w := post(); w.Code != http.StatusOK {
		t.Fatalf("Webhook() status = %d, want 200 — retrying the same unreadable bytes cannot repair them", w.Code)
	}

	var processedAt *time.Time
	var reason *string
	var objectRef, storedPayload string
	if err := pool.QueryRow(ctx, `
		SELECT processed_at, unreconciled, coalesce(object_ref, ''), payload::text
		FROM payment_webhook_events
		WHERE provider = 'stripe' AND event_id = $1`, eventID).
		Scan(&processedAt, &reason, &objectRef, &storedPayload); err != nil {
		t.Fatalf("read unreadable event: %v", err)
	}
	if processedAt == nil {
		t.Error("the unreadable event was not marked processed, so Stripe will retry identical bytes")
	}
	if reason == nil || !strings.HasPrefix(*reason, "unreadable_event: ") {
		t.Fatalf("unreconciled = %v, want durable unreadable_event cause", reason)
	}
	if objectRef != session {
		t.Errorf("object_ref = %q, want the known session %q", objectRef, session)
	}

	var actionableRows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM payment_webhook_events
		WHERE provider = 'stripe' AND event_id = $1
		  AND unreconciled IS NOT NULL AND reconciled_at IS NULL`, eventID).
		Scan(&actionableRows); err != nil {
		t.Fatalf("read /admin/health predicate: %v", err)
	}
	if actionableRows != 1 {
		t.Errorf("/admin/health predicate finds %d unreadable events, want 1", actionableRows)
	}

	var paymentStatus, orderStatus string
	var captured *int64
	if err := pool.QueryRow(ctx, `
		SELECT p.status, p.captured_amount_cents, o.fulfillment_status
		FROM payments p JOIN orders o ON o.id = p.order_id
		WHERE p.provider_ref = $1`, session).
		Scan(&paymentStatus, &captured, &orderStatus); err != nil {
		t.Fatalf("read untouched payment: %v", err)
	}
	if paymentStatus != "requires_payment" || captured != nil || orderStatus != "pending" {
		t.Errorf("unreadable event left payment=%q captured=%v order=%q, want requires_payment/NULL/pending",
			paymentStatus, captured, orderStatus)
	}
	if output := logs.String(); !strings.Contains(output, "level=ERROR") ||
		!strings.Contains(output, "could not be read") {
		t.Errorf("unreadable event log = %q, want ERROR telling the operator it could not be read", output)
	}

	// A replay is still 200 and changes none of the evidence: the claim, reason
	// and original payload are history after the first signed delivery.
	if w := post(); w.Code != http.StatusOK {
		t.Fatalf("replayed Webhook() status = %d, want 200", w.Code)
	}
	var replayProcessedAt *time.Time
	var replayReason *string
	var replayPayload string
	if err := pool.QueryRow(ctx, `
		SELECT processed_at, unreconciled, payload::text
		FROM payment_webhook_events
		WHERE provider = 'stripe' AND event_id = $1`, eventID).
		Scan(&replayProcessedAt, &replayReason, &replayPayload); err != nil {
		t.Fatalf("read replayed event: %v", err)
	}
	if processedAt == nil || replayProcessedAt == nil || !processedAt.Equal(*replayProcessedAt) {
		t.Errorf("replay changed processed_at from %v to %v", processedAt, replayProcessedAt)
	}
	if reason == nil || replayReason == nil || *reason != *replayReason {
		t.Errorf("replay changed unreconciled from %v to %v", reason, replayReason)
	}
	if replayPayload != storedPayload {
		t.Error("replay replaced the original signed payload")
	}
}

// TestTheWebhookFlagsMoneyItCannotAttribute proves that a paid Checkout Session
// with no local payment row is countable, stays unguessed, and is acknowledged
// with 200 because retrying cannot create the missing attribution. Resolving the
// operator alarm must not make that already-paid Stripe identity eligible to be
// attached to a newly active local payment later.
func TestTheWebhookFlagsMoneyItCannotAttribute(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	var logs bytes.Buffer
	h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
		slog.New(slog.NewTextHandler(&logs, nil)), false)

	number, _ := order(t, 88800)
	session := "cs_unattributable_" + uuid.NewString()[:12]
	eventID := "evt_" + uuid.NewString()[:12]
	body, header := signed(t, typed(sessionEvent(eventID, session, "paid", 88800),
		"checkout.session.completed"))
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/webhooks/stripe", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", header)
	w := httptest.NewRecorder()
	h.Webhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Webhook() status = %d, want 200 — an unattributable capture can never succeed on retry", w.Code)
	}
	var processedAt *time.Time
	var reason *string
	var objectRef string
	if err := pool.QueryRow(ctx, `
		SELECT processed_at, unreconciled, coalesce(object_ref, '')
		FROM payment_webhook_events
		WHERE provider = 'stripe' AND event_id = $1`, eventID).
		Scan(&processedAt, &reason, &objectRef); err != nil {
		t.Fatalf("read unattributed capture: %v", err)
	}
	if processedAt == nil {
		t.Error("the unattributed capture was not marked processed")
	}
	if reason == nil || !strings.HasPrefix(*reason, "unattributed_capture: ") {
		t.Fatalf("unreconciled = %v, want durable unattributed_capture cause", reason)
	}
	if objectRef != session {
		t.Errorf("object_ref = %q, want session %q for the Stripe search", objectRef, session)
	}

	var actionableRows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM payment_webhook_events
		WHERE provider = 'stripe' AND event_id = $1
		  AND unreconciled IS NOT NULL AND reconciled_at IS NULL`, eventID).
		Scan(&actionableRows); err != nil {
		t.Fatalf("read /admin/health predicate: %v", err)
	}
	if actionableRows != 1 {
		t.Errorf("/admin/health predicate finds %d unattributed captures, want 1", actionableRows)
	}
	var payments int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE provider_ref = $1`, session).Scan(&payments); err != nil {
		t.Fatalf("count invented payments: %v", err)
	}
	if payments != 0 {
		t.Errorf("the handler invented %d payment rows for an unattributable session, want 0", payments)
	}
	if output := logs.String(); !strings.Contains(output, "level=ERROR") ||
		!strings.Contains(output, "cannot attribute") {
		t.Errorf("unattributed capture log = %q, want an actionable ERROR", output)
	}

	// Exact late-link regression: the event has no payment row, then an operator
	// reconciles it. reconciled_at resolves the alarm and permits a NEW Stripe
	// identity; it never erases the fact that this particular Session was paid.
	var reconciled bool
	if err := pool.QueryRow(ctx, `SELECT release_payment_event($1)`, eventID).
		Scan(&reconciled); err != nil || !reconciled {
		t.Fatalf("reconcile unattributed capture = %v, %v; want true", reconciled, err)
	}
	openErr := s.OpenPayment(ctx, number, session, 88800)
	if !errors.Is(openErr, payment.ErrNotOpenable) {
		t.Fatalf("OpenPayment with reconciled captured reference = %v, want ErrNotOpenable", openErr)
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](openErr)
	if !ok || pgErr.ConstraintName != "payments_open_refuses_seen_provider_ref" {
		t.Fatalf("late-link refusal = %v, want payments_open_refuses_seen_provider_ref", openErr)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE provider_ref = $1`, session).Scan(&payments); err != nil {
		t.Fatalf("count payments after refused late link: %v", err)
	}
	if payments != 0 {
		t.Errorf("reconciled unattributed reference gained %d payment rows, want 0", payments)
	}
}

// TestTheWebhookPersistsCapturesLocalInvariantsRefuse proves that verified money
// is never left only in a retry loop. Neither an amount that differs from the
// session intent nor an order whose amount changed can become valid when Stripe
// redelivers the same event, so both must be durable unreconciled outcomes.
func TestTheWebhookPersistsCapturesLocalInvariantsRefuse(t *testing.T) {
	tests := []struct {
		name       string
		captured   int64
		wantDetail string
	}{
		{name: "capture differs from session intent", captured: 130000, wantDetail: "against an intent of 100000"},
		{name: "order now owes a different amount", captured: 100000, wantDetail: "payments_capture_matches_order"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			s := payment.NewStore(pool)
			var logs bytes.Buffer
			h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
				slog.New(slog.NewTextHandler(&logs, nil)), false)

			number, id := order(t, 100000)
			hold(t, id, 0, 45*time.Minute, "refused:"+number)
			session := "cs_refused_" + uuid.NewString()[:12]
			if err := s.OpenPayment(ctx, number, session, 100000); err != nil {
				t.Fatalf("open: %v", err)
			}
			if _, err := pool.Exec(ctx,
				`UPDATE orders SET shipping_cents = 30000 WHERE id = $1`, id); err != nil {
				t.Fatalf("change what the order owes: %v", err)
			}

			eventID := "evt_refused_" + uuid.NewString()[:12]
			body, header := signed(t, typed(sessionEvent(eventID, session, "paid", tt.captured),
				"checkout.session.completed"))
			req := httptest.NewRequestWithContext(ctx, http.MethodPost,
				"/webhooks/stripe", bytes.NewReader(body))
			req.Header.Set("Stripe-Signature", header)
			w := httptest.NewRecorder()
			h.Webhook(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("Webhook() status = %d, want 200 — identical retries cannot repair this capture", w.Code)
			}
			var processed bool
			var reason *string
			if err := pool.QueryRow(ctx, `
				SELECT processed_at IS NOT NULL, unreconciled
				FROM payment_webhook_events WHERE event_id = $1`, eventID).
				Scan(&processed, &reason); err != nil {
				t.Fatalf("read webhook outcome: %v", err)
			}
			if !processed {
				t.Error("the refused capture was not marked processed")
			}
			if reason == nil || !strings.HasPrefix(*reason, "refused_capture: ") ||
				!strings.Contains(*reason, tt.wantDetail) {
				t.Errorf("unreconciled = %v, want refused_capture naming %q", reason, tt.wantDetail)
			}

			var status string
			var captured *int64
			if err := pool.QueryRow(ctx,
				`SELECT status, captured_amount_cents FROM payments WHERE provider_ref = $1`,
				session).Scan(&status, &captured); err != nil {
				t.Fatalf("read refused payment: %v", err)
			}
			if status != "requires_payment" || captured != nil {
				t.Errorf("refused payment = %q/%v, want requires_payment/NULL", status, captured)
			}
			if output := logs.String(); !strings.Contains(output, "level=ERROR") ||
				!strings.Contains(output, "reconcile or refund") {
				t.Errorf("refused capture log = %q, want an actionable ERROR", output)
			}

			attempt, err := s.PaymentAttempt(ctx, number, 130000)
			if err != nil {
				t.Fatalf("PaymentAttempt after refused capture: %v", err)
			}
			if !attempt.NeedsReconciliation {
				t.Fatal("verified money awaiting reconciliation did not block another checkout")
			}
			if openErr := s.OpenPayment(ctx, number, "cs_must_not_open_"+uuid.NewString()[:12], 130000); !errors.Is(openErr, payment.ErrNotOpenable) {
				t.Fatalf("OpenPayment during reconciliation = %v, want ErrNotOpenable", openErr)
			}

			newSession := "cs_after_reconcile_" + uuid.NewString()[:12]
			gateway, stripeCalls := gatewayRecordingCalls(t, newSession)
			start := payment.NewHandler(s, gateway, alwaysPlacedHere{},
				slog.New(slog.DiscardHandler), false)
			post := func() *httptest.ResponseRecorder {
				req := httptest.NewRequestWithContext(ctx, http.MethodPost,
					"/orders/"+number+"/pay", http.NoBody)
				req.SetPathValue("number", number)
				res := httptest.NewRecorder()
				start.Start(res, req)
				return res
			}
			if res := post(); res.Code != http.StatusConflict {
				t.Fatalf("Start during reconciliation status = %d, want 409", res.Code)
			}
			if *stripeCalls != 0 {
				t.Fatalf("Start called Stripe %d times while old captured money was unresolved, want 0",
					*stripeCalls)
			}

			// This represents the operator's refund/investigation acknowledgement.
			// It atomically terminates the old attempt before lifting the gate.
			var reconciled bool
			if reconcileErr := pool.QueryRow(ctx, `SELECT release_payment_event($1)`, eventID).
				Scan(&reconciled); reconcileErr != nil || !reconciled {
				t.Fatalf("release_payment_event = %v, %v; want true", reconciled, reconcileErr)
			}
			if res := post(); res.Code != http.StatusSeeOther {
				t.Fatalf("Start after reconciliation status = %d, want 303", res.Code)
			}
			if *stripeCalls != 1 {
				t.Fatalf("Start after reconciliation called Stripe %d times, want 1", *stripeCalls)
			}

			var statuses []string
			rows, err := pool.Query(ctx,
				`SELECT status FROM payments WHERE order_id = $1 ORDER BY created_at, id`, id)
			if err != nil {
				t.Fatalf("read attempts after reconciliation: %v", err)
			}
			for rows.Next() {
				var status string
				if err := rows.Scan(&status); err != nil {
					rows.Close()
					t.Fatalf("scan attempt status: %v", err)
				}
				statuses = append(statuses, status)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				t.Fatalf("walk attempt statuses: %v", err)
			}
			rows.Close()
			if diff := cmp.Diff([]string{"cancelled", "requires_payment"}, statuses); diff != "" {
				t.Errorf("payment statuses after safe replacement (-want +got):\n%s", diff)
			}
		})
	}
}

// TestReachableCaptureConstraintsBecomeDurable binds the classifier to the
// actual PostgreSQL names. A rename or a constraint removed from the durable
// set would turn verified money back into a permanent 500/retry loop.
func TestReachableCaptureConstraintsBecomeDurable(t *testing.T) {
	tests := []struct {
		name       string
		constraint string
		prepare    func(*testing.T, context.Context, *payment.Store, string, uuid.UUID, string)
		cardLast4  string
		wantStatus string
	}{
		{
			name: "the order now owes a different amount", constraint: "payments_capture_matches_order",
			prepare: func(t *testing.T, ctx context.Context, _ *payment.Store, _ string, id uuid.UUID, _ string) {
				t.Helper()
				if _, err := pool.Exec(ctx,
					`UPDATE orders SET shipping_cents = shipping_cents + 1 WHERE id = $1`, id); err != nil {
					t.Fatalf("change amount owed after opening payment: %v", err)
				}
			},
		},
		{
			name: "an incomplete order", constraint: "payments_require_complete_order",
			prepare: func(t *testing.T, ctx context.Context, _ *payment.Store, _ string, id uuid.UUID, _ string) {
				t.Helper()
				if _, err := pool.Exec(ctx, `DELETE FROM order_private_data WHERE order_id = $1`, id); err != nil {
					t.Fatalf("remove required delivery data: %v", err)
				}
			},
		},
		{
			name: "malformed card evidence", constraint: "payments_last4_format",
			cardLast4: "not-four-digits",
			prepare:   func(*testing.T, context.Context, *payment.Store, string, uuid.UUID, string) {},
		},
		{
			name: "a paid event follows a terminal local session", constraint: "payments_no_regression",
			wantStatus: "cancelled",
			prepare: func(t *testing.T, ctx context.Context, _ *payment.Store, _ string, _ uuid.UUID, session string) {
				t.Helper()
				if _, err := pool.Exec(ctx, `SELECT cancel_payment($1)`, session); err != nil {
					t.Fatalf("cancel payment before its late capture: %v", err)
				}
			},
		},
		{
			name: "another capture already funds the order", constraint: "payments_one_capture_per_order",
			prepare: func(t *testing.T, ctx context.Context, _ *payment.Store, _ string, id uuid.UUID, _ string) {
				t.Helper()
				if _, err := pool.Exec(ctx, `
					INSERT INTO payments
					    (order_id, provider_ref, status, intended_amount_cents,
					     captured_amount_cents, paid_at)
					VALUES ($1, $2, 'succeeded', 100000, 100000, now())`,
					id, "cs_existing_capture_"+uuid.NewString()[:12]); err != nil {
					t.Fatalf("record the order's existing capture: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			s := payment.NewStore(pool)
			number, id := order(t, 100000)
			session := "cs_constraint_" + uuid.NewString()[:12]

			if err := s.OpenPayment(ctx, number, session, 100000); err != nil {
				t.Fatalf("open payment: %v", err)
			}
			tt.prepare(t, ctx, s, number, id, session)

			eventID := "evt_constraint_" + uuid.NewString()[:12]
			event := sessionEvent(eventID, session, "paid", 100000)
			if tt.cardLast4 != "" {
				object := event["data"].(map[string]any)["object"].(map[string]any)
				object["payment_intent"] = map[string]any{
					"id": "pi_" + uuid.NewString()[:12],
					"latest_charge": map[string]any{
						"id": "ch_" + uuid.NewString()[:12],
						"payment_method_details": map[string]any{
							"card": map[string]any{"brand": "visa", "last4": tt.cardLast4},
						},
					},
				}
			}
			body, header := signed(t, typed(event, "checkout.session.completed"))
			req := httptest.NewRequestWithContext(ctx, http.MethodPost,
				"/webhooks/stripe", bytes.NewReader(body))
			req.Header.Set("Stripe-Signature", header)
			res := httptest.NewRecorder()
			payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
				slog.New(slog.DiscardHandler), false).Webhook(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("Webhook() status = %d, want 200", res.Code)
			}

			var processed bool
			var reason *string
			if err := pool.QueryRow(ctx,
				`SELECT processed_at IS NOT NULL, unreconciled
				 FROM payment_webhook_events WHERE event_id = $1`, eventID).
				Scan(&processed, &reason); err != nil {
				t.Fatalf("read durable refusal: %v", err)
			}
			if !processed {
				t.Error("the durable refusal was not marked processed")
			}
			if reason == nil || !strings.HasPrefix(*reason, "refused_capture: ") ||
				!strings.Contains(*reason, tt.constraint) {
				t.Errorf("unreconciled = %v, want refused_capture naming %s", reason, tt.constraint)
			}

			wantStatus := tt.wantStatus
			if wantStatus == "" {
				wantStatus = "requires_payment"
			}
			var status string
			var captured *int64
			if err := pool.QueryRow(ctx, `
				SELECT status, captured_amount_cents FROM payments WHERE provider_ref = $1`, session).
				Scan(&status, &captured); err != nil {
				t.Fatalf("read payment after durable refusal: %v", err)
			}
			if status != wantStatus || captured != nil {
				t.Errorf("refused payment = %q/%v, want %q/NULL", status, captured, wantStatus)
			}
		})
	}
}

// TestTheWebhookItselfFlagsMoneyForACancelledOrder drives the real handler. The
// test below drives a callback of its own, so it can only show that the store
// records the outcome, never that handler.go's switch asks it to.
func TestTheWebhookItselfFlagsMoneyForACancelledOrder(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	var logs bytes.Buffer
	h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
		slog.New(slog.NewTextHandler(&logs, nil)), false)

	number, id := order(t, 88800)
	session := "cs_handler_unrec_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, session, 88800); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE id = $1`, id); err != nil {
		t.Fatalf("cancel the order: %v", err)
	}

	eventID := "evt_" + uuid.NewString()[:12]
	body, header := signed(t, typed(sessionEvent(eventID, session, "paid", 88800),
		"checkout.session.completed"))
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/webhooks/stripe", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", header)
	w := httptest.NewRecorder()
	h.Webhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Webhook() status = %d, want 200 — a 5xx gets the endpoint disabled", w.Code)
	}
	var reason *string
	if err := pool.QueryRow(ctx,
		`SELECT unreconciled FROM payment_webhook_events WHERE event_id = $1`,
		eventID).Scan(&reason); err != nil {
		t.Fatalf("read the event: %v", err)
	}
	if reason == nil {
		t.Fatal("the webhook took money for a cancelled order and left nothing " +
			"for /admin/health to count — the shop finds out when the customer asks")
	}
	if !strings.HasPrefix(*reason, "cancelled_order_capture: ") {
		t.Errorf("unreconciled = %q, want durable cancelled_order_capture cause", *reason)
	}
	if output := logs.String(); !strings.Contains(output, "level=ERROR") ||
		!strings.Contains(output, "cancelled order") {
		t.Errorf("cancelled-order capture log = %q, want an actionable ERROR", output)
	}
}

// TestMoneyForACancelledOrderLeavesSomethingToActOn holds the difference
// between a log line and a record.
//
// A slow webhook races a cancel: the capture is refused by
// payments_refuse_cancelled_order, and the event is still marked processed —
// retrying changes nothing, and rolling the claim back would lose the only
// trace that money arrived. The money IS at Stripe against goods already back
// on the shelf, so the event is marked UNRECONCILED in the same transaction as
// the claim: an event may not be recorded as seen unless what it needs is
// recorded with it.
func TestMoneyForACancelledOrderLeavesSomethingToActOn(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, id := order(t, 77700)
	session := "cs_unreconciled_" + number

	if err := s.OpenPayment(ctx, number, session, 77700); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE id = $1`, id); err != nil {
		t.Fatalf("cancel the order: %v", err)
	}

	const eventID = "evt_unreconciled_probe"
	claimed, err := s.ProcessWebhook(ctx, &payment.WebhookEvent{
		ID: eventID, Type: "checkout.session.completed", ObjectRef: session,
		Payload: []byte(`{"probe":true}`),
	}, func(ctx context.Context, tx *payment.WebhookTx) error {
		_, captureErr := tx.Capture(ctx, payment.Capture{SessionID: session, AmountRecv: 77700})
		if !errors.Is(captureErr, payment.ErrOrderCancelled) {
			return captureErr
		}
		return tx.Unreconciled(ctx,
			"cancelled_order_capture: money arrived for an order that was already cancelled")
	})
	if err != nil {
		t.Fatalf("process the webhook: %v", err)
	}
	if !claimed {
		t.Fatal("the event was not claimed, so this proved nothing")
	}

	var processed bool
	var reason *string
	if err := pool.QueryRow(ctx, `
		SELECT processed_at IS NOT NULL, unreconciled
		FROM payment_webhook_events WHERE provider = 'stripe' AND event_id = $1`,
		eventID).Scan(&processed, &reason); err != nil {
		t.Fatalf("read the event: %v", err)
	}
	if !processed {
		t.Error("the event was not marked processed, so Stripe will retry something " +
			"that can never succeed")
	}
	if reason == nil {
		t.Fatal("money arrived for a cancelled order and the event records nothing " +
			"about it — a log line is not something /admin/health can count, and " +
			"nobody will refund it")
	}
	if !strings.Contains(*reason, "cancelled") {
		t.Errorf("the reason is %q, which does not say what happened", *reason)
	}
}
