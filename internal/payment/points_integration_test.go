//go:build integration

package payment_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/payment"
)

// TestWebhookTx_CaptureTierMultiplier: Points are multiplied by the customer's
// tier. It holds the rate an order earns at: the multiplier comes from the
// spend the customer had BEFORE this order.
func TestWebhookTx_CaptureTierMultiplier(t *testing.T) {
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

// TestCapturePaymentTierAwards: Concurrent captures serialize tier awards. It
// holds the per-member lock between two different paid orders. Without it, both
// award statements can read the same pre-payment spend and mint at the base
// rate even though one payment must precede the other.
func TestCapturePaymentTierAwards(t *testing.T) {
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

// TestWebhookTx_CaptureZeroAward: An order that earns nothing is still
// captured. It is the customer-order sibling of loyalty's guest-order
// precedent: neither kind of zero award may abort money that Stripe has already
// captured.
func TestWebhookTx_CaptureZeroAward(t *testing.T) {
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

// TestWebhookTx_CaptureWholeHundreds: The awarded points use whole hundreds. It
// binds every integer boundary to the database's single production definition.
func TestWebhookTx_CaptureWholeHundreds(t *testing.T) {
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
