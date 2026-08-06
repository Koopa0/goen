//go:build integration

package loyalty_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/koopa0/goen/internal/loyalty"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("goen"),
		postgres.WithUsername("goen"),
		postgres.WithPassword("goen"),
		postgres.BasicWaitStrategies(),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		panic(err)
	}
	dsn, dsnErr := container.ConnectionString(ctx, "sslmode=disable")
	if dsnErr != nil {
		panic(dsnErr)
	}
	schema, err := os.ReadFile("../../migrations/001_initial_schema.up.sql")
	if err != nil {
		panic(err)
	}
	pool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		panic(err)
	}
	if _, execErr := pool.Exec(ctx, string(schema)); execErr != nil {
		panic(execErr)
	}
	// The seed, for shipping_method_versions: an order needs a shipping version
	// and the migration creates none.
	seed, seedReadErr := os.ReadFile("../../seed/dev_catalog.sql")
	if seedReadErr != nil {
		panic(seedReadErr)
	}
	if _, seedErr := pool.Exec(ctx, string(seed)); seedErr != nil {
		panic(seedErr)
	}
	code := m.Run()
	pool.Close()
	_ = testcontainers.TerminateContainer(container)
	os.Exit(code)
}

// customer creates a user with an account holding `points`.
func customer(t *testing.T, points int64) (userID string, accountID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	var uid uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('pt-' || gen_random_uuid() || '@goen.invalid', 'customer', '點數測試')
		RETURNING id`).Scan(&uid); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO store_credit_accounts (user_id) VALUES ($1) RETURNING id`,
		uid).Scan(&accountID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	if points > 0 {
		if _, err := pool.Exec(ctx, `
			INSERT INTO loyalty_entries (account_id, points, reason, idempotency_key, expires_on)
			VALUES ($1, $2, 'seed', 'seed:' || gen_random_uuid(), current_date + 365)`,
			accountID, points); err != nil {
			t.Fatalf("seed points: %v", err)
		}
	}
	return uid.String(), accountID
}

// balance reads the account's spendable points.
func balance(t *testing.T, accountID uuid.UUID) int64 {
	t.Helper()
	var points int64
	if err := pool.QueryRow(t.Context(),
		`SELECT points FROM loyalty_balances WHERE account_id = $1`, accountID).Scan(&points); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return points
}

// TestPointsCannotGoNegativeEvenConcurrently proves a balance cannot be spent
// twice.
//
// Two redemptions racing each read a balance the other is about to invalidate.
// loyalty_never_negative locks the account FIRST, exactly as
// store_credit_never_negative does — without the lock both pass and the
// customer spends points twice.
func TestPointsCannotGoNegativeEvenConcurrently(t *testing.T) {
	ctx := t.Context()
	_, accountID := customer(t, 1000)

	const racers = 8
	start := make(chan struct{})
	won := make([]bool, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			<-start
			// The key is built in Go, not concatenated in SQL: passing an int
			// where the statement casts to text made every racer fail to
			// ENCODE, and the case then read as "the guard let nobody
			// through" — a fixture bug wearing the shape of a passing lock.
			_, err := pool.Exec(ctx, `
				SELECT redeem_loyalty_points($1, 1000, 10000, $2)`,
				accountID, fmt.Sprintf("race:%d", i))
			won[i] = err == nil
		})
	}
	close(start)
	wg.Wait()

	wins := 0
	for _, ok := range won {
		if ok {
			wins++
		}
	}
	if wins != 1 {
		t.Errorf("%d of %d concurrent redemptions of the whole balance succeeded, "+
			"want 1", wins, racers)
	}
	if left := balance(t, accountID); left != 0 {
		t.Errorf("the balance is %d after spending it all", left)
	}
}

// TestARedemptionPostsPointsAndCreditTogether proves the customer never pays
// and receives nothing.
//
// Both or neither. Two statements would let the points go and the credit not
// arrive — the customer pays and receives nothing, which is the worst available
// failure here.
func TestARedemptionPostsPointsAndCreditTogether(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 500)
	s := loyalty.NewStore(pool)

	cents, err := s.Redeem(ctx, userID, 500)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if cents != 5000 {
		t.Errorf("500 points became %d cents, want 5000", cents)
	}
	if left := balance(t, accountID); left != 0 {
		t.Errorf("%d points left, want 0", left)
	}

	var credit int64
	if err := pool.QueryRow(ctx,
		`SELECT coalesce(sum(amount_cents), 0) FROM store_credit_entries
		 WHERE account_id = $1 AND reason = 'points'`, accountID).Scan(&credit); err != nil {
		t.Fatalf("read credit: %v", err)
	}
	if credit != 5000 {
		t.Errorf("%d cents of credit posted, want 5000", credit)
	}
}

// TestExpiredPointsAreNotSpendable proves expiry does not wait for a job.
//
// Expiry is applied ON READ rather than by a job that writes expiry rows. A job
// that has not run yet would leave expired points spendable, and a customer
// spending points the shop believes are gone is the failure this must not have.
func TestExpiredPointsAreNotSpendable(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 0)

	// 300 expired yesterday, 200 good for another year.
	if _, err := pool.Exec(ctx, `
		INSERT INTO loyalty_entries (account_id, points, reason, idempotency_key, expires_on)
		VALUES ($1, 300, 'old', 'old:' || gen_random_uuid(), current_date - 1),
		       ($1, 200, 'new', 'new:' || gen_random_uuid(), current_date + 365)`,
		accountID); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if got := balance(t, accountID); got != 200 {
		t.Errorf("the balance is %d, want 200 — expired points are still spendable", got)
	}
	// And the database refuses to spend them even if something asks.
	s := loyalty.NewStore(pool)
	if _, err := s.Redeem(ctx, userID, 500); !errors.Is(err, loyalty.ErrNotEnough) {
		t.Errorf("spending expired points gave %v, want ErrNotEnough", err)
	}
	if _, err := s.Redeem(ctx, userID, 200); err != nil {
		t.Errorf("spending the unexpired points was refused: %v", err)
	}
}

// TestAnOrderIsAwardedOnce proves at-least-once delivery awards exactly once.
//
// Stripe delivers at least once and the capture tolerates that; the award has
// to as well. Idempotency is the database's, keyed on the order — a check in Go
// would be one two concurrent deliveries both pass.
func TestAnOrderIsAwardedOnce(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 0)
	orderID := orderFor(t, userID, 250000) // NT$2,500 → 25 points

	for i := range 3 {
		var awarded int64
		if err := pool.QueryRow(ctx,
			`SELECT award_loyalty_points($1, 25, current_date + 365)`, orderID).Scan(&awarded); err != nil {
			t.Fatalf("award %d: %v", i, err)
		}
		if i == 0 && awarded != 25 {
			t.Errorf("the first award returned %d, want 25", awarded)
		}
		if i > 0 && awarded != 0 {
			t.Errorf("award %d returned %d, want 0 — it is not idempotent", i, awarded)
		}
	}
	if got := balance(t, accountID); got != 25 {
		t.Errorf("the balance is %d after three deliveries of one order, want 25", got)
	}
}

// TestAGuestOrderEarnsNothingAndDoesNotError proves a guest capture is not
// failed by the award.
//
// Guest checkout is supported and points are a membership benefit. Refusing the
// award would fail the webhook, which would make Stripe retry a capture that
// already succeeded.
func TestAGuestOrderEarnsNothingAndDoesNotError(t *testing.T) {
	ctx := t.Context()
	orderID := orderFor(t, "", 250000)

	var awarded int64
	if err := pool.QueryRow(ctx,
		`SELECT award_loyalty_points($1, 25, current_date + 365)`, orderID).Scan(&awarded); err != nil {
		t.Fatalf("a guest order errored: %v", err)
	}
	if awarded != 0 {
		t.Errorf("a guest order was awarded %d points", awarded)
	}
}

// TestTheLedgerIsAppendOnly proves a posted entry cannot be edited away.
func TestTheLedgerIsAppendOnly(t *testing.T) {
	ctx := t.Context()
	_, accountID := customer(t, 100)

	for _, stmt := range []string{
		`UPDATE loyalty_entries SET points = 99999 WHERE account_id = $1`,
		`DELETE FROM loyalty_entries WHERE account_id = $1`,
	} {
		if _, err := pool.Exec(ctx, stmt, accountID); err == nil {
			t.Errorf("%q succeeded against an append-only ledger", stmt)
		}
	}
}

// orderFor writes a committed-shaped order for `userID` ("" for a guest).
func orderFor(t *testing.T, userID string, cents int64) uuid.UUID {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var owner uuid.NullUUID
	if userID != "" {
		owner = uuid.NullUUID{UUID: uuid.MustParse(userID), Valid: true}
	}
	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id`, owner).Scan(&orderID); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'p@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("delivery details: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'PT-SKU', '點數測試', $2, 1)`, orderID, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return orderID
}

// TestTheLoyaltyGuardHoldsOnItsOwn proves the account lock is what serialises
// spends, not PostgreSQL's deadlock detector.
//
// Three things had to be got right before this case said anything:
//
//  1. The case above races REDEMPTIONS, and a redemption also posts store
//     credit — whose own guard locks the account. It passes with the loyalty
//     guard's lock removed, because the other lock covers it.
//  2. Racing the ledger directly was not enough either: without the lock the
//     losers came back with DEADLOCK DETECTED, so exactly one still won and the
//     case read as a passing lock. PostgreSQL was doing the work, by accident,
//     and a deadlock is not a guard — it is a coin toss that happens to have
//     one survivor.
//  3. So the assertion is bound to the CONSTRAINT NAME. A refusal that is not
//     loyalty_never_negative is not this guard working.
func TestTheLoyaltyGuardHoldsOnItsOwn(t *testing.T) {
	ctx := t.Context()
	_, accountID := customer(t, 1000)

	const racers = 8
	start := make(chan struct{})
	won := make([]bool, racers)
	refusals := make([]string, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			<-start
			// Through the posting FUNCTION, which is the only path goen has:
			// `store` and `admin` hold no INSERT on the ledger. A raw insert
			// would race without the function's own lock and deadlock — which
			// is exactly what this case was written to stop being mistaken for
			// a guard.
			_, err := pool.Exec(ctx, `
				SELECT redeem_loyalty_points($1, 1000, 10000, $2)`,
				accountID, fmt.Sprintf("solo:%d", i))
			won[i] = err == nil
			if err != nil {
				refusals[i] = constraintOf(err)
			}
		})
	}
	close(start)
	wg.Wait()

	wins := 0
	for _, ok := range won {
		if ok {
			wins++
		}
	}
	if wins != 1 {
		t.Errorf("%d of %d concurrent spends of the whole balance succeeded, want 1 — "+
			"the guard is reading the balance without holding the account",
			wins, racers)
	}
	if left := balance(t, accountID); left < 0 {
		t.Errorf("the balance is %d", left)
	}

	// Every loser must have been refused by THIS guard. A deadlock also leaves
	// one survivor, so counting winners cannot tell the two apart — and a
	// deadlock is not a guard.
	for i, why := range refusals {
		if won[i] {
			continue
		}
		if why != "loyalty_never_negative" {
			t.Errorf("racer %d was refused by %q, not by the guard — the lock is "+
				"not what is serialising these", i, why)
		}
	}
}

// constraintOf names what refused a statement.
func constraintOf(err error) string {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return err.Error()
	}
	if pgErr.ConstraintName != "" {
		return pgErr.ConstraintName
	}
	return pgErr.Code
}
