//go:build integration

package payment_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/payment"
)

// TestStore_OpenPaymentRepeated: Opening the same payment twice is one row.
func TestStore_OpenPaymentRepeated(t *testing.T) {
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

// TestStore_AttemptReusesSession: A second payment attempt reuses the open
// session. It is the double-charge defect and the read that closes it:
// open_payment dedupes on (order_id, provider_ref) and every session brings its
// own id, so two tabs are two real charges.
func TestStore_AttemptReusesSession(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, id := order(t, 149900)

	first, err := s.Attempt(ctx, number, 149900)
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

	second, err := s.Attempt(ctx, number, 149900)
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
	stale, err := s.Attempt(ctx, number, 119900)
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
	dead, err := s.Attempt(ctx, number, 149900)
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

// TestStore_OpenPaymentCancelledOrder: Opening a payment is refused on a
// cancelled order.
func TestStore_OpenPaymentCancelledOrder(t *testing.T) {
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

// TestOpenPaymentRefusals: Payment posting functions name every admission
// refusal. It binds the error names that Store maps to ErrNotOpenable. A
// generic refusal is insufficient here: each branch protects a different
// double-charge or identity invariant.
func TestOpenPaymentRefusals(t *testing.T) {
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
