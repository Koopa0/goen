//go:build integration

package loyalty_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/loyalty"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	p, stop, err := dbtest.Start(ctx)
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
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
	stop()
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
			INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on)
			VALUES ($1, 'award', $2, 'seed', 'seed:' || gen_random_uuid(), shop_today() + 365)`,
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

// TestAnInfrastructureFailureIsNotReportedAsAnEmptyBalance locks the category
// boundary: a timeout while writing store credit says nothing about the points
// the customer has. The fixture deliberately has more than the request, or the
// preflight balance check would make this test pass without reaching the write.
func TestAnInfrastructureFailureIsNotReportedAsAnEmptyBalance(t *testing.T) {
	ctx := t.Context()
	userID, _ := customer(t, 500)

	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse timeout pool config: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "1500"
	timeoutPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open timeout pool: %v", err)
	}
	t.Cleanup(timeoutPool.Close)

	blocker, err := pgx.Connect(ctx, pool.Config().ConnString())
	if err != nil {
		t.Fatalf("open blocker: %v", err)
	}
	t.Cleanup(func() { _ = blocker.Close(context.Background()) }) //nolint:usetesting // cleanup runs after t.Context is canceled
	tx, err := blocker.Begin(ctx)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, lockErr := tx.Exec(ctx, `LOCK TABLE store_credit_entries IN ACCESS EXCLUSIVE MODE`); lockErr != nil {
		t.Fatalf("lock store-credit ledger: %v", lockErr)
	}

	_, err = loyalty.NewStore(timeoutPool).Redeem(ctx, userID, 100)
	if errors.Is(err, loyalty.ErrNotEnough) {
		t.Fatalf("a statement timeout is not a statement about the customer's balance: %v", err)
	}
	if errors.Is(err, loyalty.ErrTooSmall) || errors.Is(err, loyalty.ErrNoAccount) {
		t.Fatalf("the timeout acquired another loyalty category: %v", err)
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != "57014" {
		t.Fatalf("timeout cause = %v, want preserved PgError 57014", err)
	}
}

// TestARedemptionPostsPointsAndCreditTogether proves the customer never spends
// points and receives no credit.
func TestARedemptionPostsPointsAndCreditTogether(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 0)
	lots := []struct {
		id     uuid.UUID
		points int64
	}{{uuid.New(), 200}, {uuid.New(), 300}}
	for i, lot := range lots {
		if _, err := pool.Exec(ctx, `
			INSERT INTO loyalty_entries
			    (id, account_id, kind, points, reason, idempotency_key, expires_on, created_at)
			VALUES ($1::uuid, $2, 'award', $3, 'seed', 'atomic:' || ($1::uuid)::text,
			        shop_today() + 365, now() + make_interval(secs => $4))`,
			lot.id, accountID, lot.points, i); err != nil {
			t.Fatalf("seed award lot %d: %v", i, err)
		}
	}
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

	for _, lot := range lots {
		var rows int
		var spent int64
		if err := pool.QueryRow(ctx, `
			SELECT count(*), coalesce(sum(-points), 0)::bigint
			FROM loyalty_entries
			WHERE account_id = $1 AND kind = 'spend' AND lot_id = $2`,
			accountID, lot.id).Scan(&rows, &spent); err != nil {
			t.Fatalf("read spend for lot %s: %v", lot.id, err)
		}
		if rows != 1 || spent != lot.points {
			t.Errorf("lot %s has %d spend rows totalling %d, want one totalling %d",
				lot.id, rows, spent, lot.points)
		}
	}

	var credit int64
	var creditRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*), coalesce(sum(amount_cents), 0) FROM store_credit_entries
		 WHERE account_id = $1 AND reason = 'points'`, accountID).Scan(&creditRows, &credit); err != nil {
		t.Fatalf("read credit: %v", err)
	}
	if creditRows != 1 || credit != 5000 {
		t.Errorf("%d credit rows posted %d cents, want one row and 5000", creditRows, credit)
	}
}

// TestExpiredPointsAreNotSpendable proves expiry is applied ON READ, so it never
// waits for a job that has not run.
func TestExpiredPointsAreNotSpendable(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 0)

	// 300 expired yesterday, 200 good for another year.
	if _, err := pool.Exec(ctx, `
		INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on)
		VALUES ($1, 'award', 300, 'old', 'old:' || gen_random_uuid(), shop_today() - 1),
		       ($1, 'award', 200, 'new', 'new:' || gen_random_uuid(), shop_today() + 365)`,
		accountID); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if got := balance(t, accountID); got != 200 {
		t.Errorf("the balance is %d, want 200 — expired points are still spendable", got)
	}
	s := loyalty.NewStore(pool)
	if _, err := s.Redeem(ctx, userID, 500); !errors.Is(err, loyalty.ErrNotEnough) {
		t.Errorf("spending expired points gave %v, want ErrNotEnough", err)
	}
	if _, err := s.Redeem(ctx, userID, 200); err != nil {
		t.Errorf("spending the unexpired points was refused: %v", err)
	}
}

// TestASpentAwardLeavesTheBalanceWithIt crosses the expiry boundary the old
// model could not represent. If both lots expired a year from now, every
// assertion would pass with an unlotted spend and the test would prove nothing.
func TestASpentAwardLeavesTheBalanceWithIt(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 0)
	orderA := orderFor(t, userID, 1000000)
	orderB := orderFor(t, userID, 1000000)

	var lotA, lotB uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT award_loyalty_points($1, 100, shop_today())`, orderA).Scan(new(int64)); err != nil {
		t.Fatalf("award lot A: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM loyalty_entries WHERE order_id = $1 AND kind = 'award'`, orderA).Scan(&lotA); err != nil {
		t.Fatalf("read lot A: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT award_loyalty_points($1, 100, shop_today() + 365)`, orderB).Scan(new(int64)); err != nil {
		t.Fatalf("award lot B: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM loyalty_entries WHERE order_id = $1 AND kind = 'award'`, orderB).Scan(&lotB); err != nil {
		t.Fatalf("read lot B: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`SELECT redeem_loyalty_points($1, 150, 1500, 'expiry-boundary')`, accountID); err != nil {
		t.Fatalf("redeem across lots: %v", err)
	}
	if got := balance(t, accountID); got != 50 {
		t.Errorf("today's balance = %d, want 50", got)
	}

	for _, tc := range []struct {
		lot  uuid.UUID
		want int64
	}{{lotA, -100}, {lotB, -50}} {
		var got int64
		if err := pool.QueryRow(ctx,
			`SELECT coalesce(sum(points), 0)::bigint FROM loyalty_entries
			 WHERE lot_id = $1 AND kind = 'spend'`, tc.lot).Scan(&got); err != nil {
			t.Fatalf("read allocation for %s: %v", tc.lot, err)
		}
		if got != tc.want {
			t.Errorf("lot %s spend = %d, want %d", tc.lot, got, tc.want)
		}
	}

	var everyDateNonnegative bool
	if err := pool.QueryRow(ctx, `
		SELECT bool_and(net >= 0) FROM (
			SELECT expires_on, sum(points) AS net
			FROM loyalty_entries WHERE account_id = $1 GROUP BY expires_on
		) per_expiry`, accountID).Scan(&everyDateNonnegative); err != nil {
		t.Fatalf("read per-expiry invariant: %v", err)
	}
	if !everyDateNonnegative {
		t.Error("an expiry group is negative, so the account can drift below zero with time")
	}

	var tomorrow int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(points), 0)::bigint FROM loyalty_entries
		WHERE account_id = $1 AND expires_on >= shop_today() + 1`, accountID).Scan(&tomorrow); err != nil {
		t.Fatalf("read tomorrow's balance: %v", err)
	}
	if tomorrow != 50 {
		t.Errorf("tomorrow's balance = %d, want 50", tomorrow)
	}

	history, err := loyalty.NewStore(pool).History(ctx, userID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	spends := 0
	for _, entry := range history.Entries {
		if entry.Kind == "spend" {
			spends++
			if entry.Points != -150 {
				t.Errorf("grouped redemption = %d, want -150", entry.Points)
			}
		}
	}
	if spends != 1 {
		t.Errorf("one redemption spanning two lots rendered as %d history rows", spends)
	}
}

// TestSameDayLotsHaveADeterministicFIFOOrder locks both tie-breaks after the
// expiry date: created_at first, then id. The clawback of either order depends
// on which same-day lot the redemption consumed.
func TestSameDayLotsHaveADeterministicFIFOOrder(t *testing.T) {
	for _, tc := range []struct {
		name        string
		firstID     uuid.UUID
		secondID    uuid.UUID
		firstAtSQL  string
		secondAtSQL string
	}{
		{
			name: "created_at breaks an expiry tie",
			// Deliberately reverse UUID order: deleting created_at must choose the
			// wrong lot rather than accidentally passing on the final id tie-break.
			firstID:     uuid.MustParse("a2000001-0000-4000-8000-000000000002"),
			secondID:    uuid.MustParse("a2000001-0000-4000-8000-000000000001"),
			firstAtSQL:  "now() - interval '2 hours'",
			secondAtSQL: "now() - interval '1 hour'",
		},
		{
			name:        "id breaks a created_at tie",
			firstID:     uuid.MustParse("a2000002-0000-4000-8000-000000000001"),
			secondID:    uuid.MustParse("a2000002-0000-4000-8000-000000000002"),
			firstAtSQL:  "now()",
			secondAtSQL: "now()",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, accountID := customer(t, 0)
			stmt := fmt.Sprintf(`
				INSERT INTO loyalty_entries
				    (id, account_id, kind, points, reason, idempotency_key, expires_on, created_at)
				VALUES ($1::uuid, $3, 'award', 60, 'seed', 'fifo:' || ($1::uuid)::text, shop_today() + 365, %s),
				       ($2::uuid, $3, 'award', 60, 'seed', 'fifo:' || ($2::uuid)::text, shop_today() + 365, %s)`,
				tc.firstAtSQL, tc.secondAtSQL)
			if _, err := pool.Exec(t.Context(), stmt, tc.firstID, tc.secondID, accountID); err != nil {
				t.Fatalf("seed tied lots: %v", err)
			}
			if _, err := pool.Exec(t.Context(),
				`SELECT redeem_loyalty_points($1, 80, 800, 'fifo-redeem:' || $2::text)`,
				accountID, tc.firstID); err != nil {
				t.Fatalf("redeem: %v", err)
			}
			for _, want := range []struct {
				lot    uuid.UUID
				points int64
			}{{tc.firstID, -60}, {tc.secondID, -20}} {
				var got int64
				if err := pool.QueryRow(t.Context(),
					`SELECT coalesce(sum(points), 0)::bigint FROM loyalty_entries WHERE lot_id = $1`,
					want.lot).Scan(&got); err != nil {
					t.Fatalf("read allocation: %v", err)
				}
				if got != want.points {
					t.Errorf("lot %s received %d, want %d", want.lot, got, want.points)
				}
			}
		})
	}
}

// TestAZeroClawbackSurvivesHistoryToRenderedHTML crosses every layer that can
// accidentally turn a clawback back into "zero points": database query,
// loyalty view model, locale lookup, and the actual page component.
func TestAZeroClawbackSurvivesHistoryToRenderedHTML(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 0)
	orderID := orderFor(t, userID, 750000)
	lotID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO loyalty_entries
		    (id, account_id, kind, points, reason, idempotency_key, order_id, expires_on)
		VALUES ($1::uuid, $2, 'award', 75, 'order', 'render-award:' || ($1::uuid)::text,
		        $3, shop_today() + 365)`, lotID, accountID, orderID); err != nil {
		t.Fatalf("seed award: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO loyalty_entries
		    (account_id, kind, points, reason, idempotency_key, expires_on, lot_id)
		VALUES ($1, 'spend', -75, 'redeem', 'render-spend:' || ($2::uuid)::text,
		        shop_today() + 365, $2::uuid)`, accountID, lotID); err != nil {
		t.Fatalf("consume award: %v", err)
	}
	var returnID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason)
		VALUES ($1, '不合用') RETURNING id`, orderID).Scan(&returnID); err != nil {
		t.Fatalf("create return: %v", err)
	}
	var reversed int64
	if err := pool.QueryRow(ctx,
		`SELECT reverse_order_points($1, $2, 75)`, orderID, returnID).Scan(&reversed); err != nil {
		t.Fatalf("post zero clawback: %v", err)
	}
	if reversed != 0 {
		t.Fatalf("wholly consumed lot reversed %d, want zero", reversed)
	}

	view, err := loyalty.NewStore(pool).History(ctx, userID)
	if err != nil {
		t.Fatalf("read points history: %v", err)
	}
	var clawback *pages.PointsEntry
	for i := range view.Entries {
		if view.Entries[i].Kind == "clawback" {
			clawback = &view.Entries[i]
			break
		}
	}
	if clawback == nil {
		t.Fatal("database-created zero clawback disappeared from PointsHistory")
	}
	if clawback.Points != 0 || clawback.RequestedPoints != 75 || clawback.ShortfallPoints != 75 {
		t.Fatalf("history clawback = points %d requested %d shortfall %d, want 0/75/75",
			clawback.Points, clawback.RequestedPoints, clawback.ShortfallPoints)
	}

	for _, tc := range []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, "應扣回 75 點；實際扣回 0 點；未扣回 75 點"},
		{i18n.En, "Requested 75 points; reversed 0; shortfall 75"},
	} {
		t.Run(tc.locale.Tag(), func(t *testing.T) {
			localized := i18n.WithLocale(ctx, tc.locale)
			var rendered bytes.Buffer
			if err := pages.Points(layouts.Page{}, view).Render(localized, &rendered); err != nil {
				t.Fatalf("render history: %v", err)
			}
			if !strings.Contains(rendered.String(), tc.want) {
				t.Errorf("rendered history omits %q", tc.want)
			}
		})
	}
}

func TestPointsCannotBeSpentFromALapsedLot(t *testing.T) {
	ctx := t.Context()
	_, accountID := customer(t, 0)
	var lotID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on)
		VALUES ($1, 'award', 100, 'old', 'lapsed:' || gen_random_uuid(), shop_today() - 1)
		RETURNING id`, accountID).Scan(&lotID); err != nil {
		t.Fatalf("seed lapsed lot: %v", err)
	}

	_, err := pool.Exec(ctx,
		`SELECT redeem_loyalty_points($1, 10, 100, 'lapsed-through-door')`, accountID)
	if why := constraintOf(err); why != "loyalty_entries_within_balance" {
		t.Fatalf("posting door was refused by %q, want loyalty_entries_within_balance", why)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on, lot_id)
		VALUES ($1, 'spend', -10, 'late', 'lapsed-direct', shop_today() - 1, $2)`, accountID, lotID)
	if why := constraintOf(err); why != "loyalty_entries_lot_not_expired" {
		t.Fatalf("direct spend was refused by %q, want loyalty_entries_lot_not_expired", why)
	}
}

func TestAClawbackReversesOnlyTheLotsUnconsumedRemainder(t *testing.T) {
	// Mutation note: deleting reverse_order_points' explicit FOR UPDATE remains
	// GREEN because redeem and the lot trigger take the same account lock. This
	// test locks the clamp and zero-row audit fact, not an unobservable ordering.
	for _, tc := range []struct {
		name       string
		spent      int64
		wantActual int64
	}{
		{"partly consumed", 60, 40},
		{"wholly consumed records a zero row", 100, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			userID, accountID := customer(t, 0)
			orderID := orderFor(t, userID, 1000000)
			if err := pool.QueryRow(ctx,
				`SELECT award_loyalty_points($1, 100, shop_today() + 365)`, orderID).
				Scan(new(int64)); err != nil {
				t.Fatalf("award: %v", err)
			}
			if _, err := pool.Exec(ctx,
				`SELECT redeem_loyalty_points($1, $2, $3, 'clawback-spend:' || $4::text)`,
				accountID, tc.spent, loyalty.CreditFor(tc.spent), orderID); err != nil {
				t.Fatalf("redeem: %v", err)
			}
			var returnID uuid.UUID
			if err := pool.QueryRow(ctx, `
				INSERT INTO return_requests (order_id, requested_by_user_id, reason)
				VALUES ($1, $2, 'clawback fixture') RETURNING id`,
				orderID, uuid.MustParse(userID)).Scan(&returnID); err != nil {
				t.Fatalf("create return: %v", err)
			}
			var actual int64
			if err := pool.QueryRow(ctx,
				`SELECT reverse_order_points($1, $2, 100)`, orderID, returnID).Scan(&actual); err != nil {
				if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
					t.Fatalf("clawback refused by %q: %v", pgErr.ConstraintName, err)
				}
				t.Fatalf("clawback: %v", err)
			}
			if actual != tc.wantActual {
				t.Errorf("actual reversal = %d, want %d", actual, tc.wantActual)
			}
			var points, requested int64
			if err := pool.QueryRow(ctx, `
				SELECT points, requested_points FROM loyalty_entries
				WHERE return_request_id = $1 AND kind = 'clawback'`, returnID).
				Scan(&points, &requested); err != nil {
				t.Fatalf("read clawback row: %v", err)
			}
			if points != -tc.wantActual || requested != 100 {
				t.Errorf("clawback row = points %d requested %d, want %d/100",
					points, requested, -tc.wantActual)
			}
			if got := balance(t, accountID); got != 0 {
				t.Errorf("balance = %d, want 0", got)
			}
		})
	}
}

// TestAnOrderIsAwardedOnce proves at-least-once delivery awards exactly once,
// keyed on the order in the database rather than checked in Go.
func TestAnOrderIsAwardedOnce(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 0)
	orderID := orderFor(t, userID, 250000) // NT$2,500 → 25 points

	for i := range 3 {
		var awarded int64
		if err := pool.QueryRow(ctx,
			`SELECT award_loyalty_points($1, 25, shop_today() + 365)`, orderID).Scan(&awarded); err != nil {
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
// failed by the award, which would make Stripe retry a capture that succeeded.
func TestAGuestOrderEarnsNothingAndDoesNotError(t *testing.T) {
	ctx := t.Context()
	orderID := orderFor(t, "", 250000)

	var awarded int64
	if err := pool.QueryRow(ctx,
		`SELECT award_loyalty_points($1, 25, shop_today() + 365)`, orderID).Scan(&awarded); err != nil {
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

// TestTheLoyaltyAllocatorHoldsOnItsOwn proves the account lock is what
// serialises spends, not PostgreSQL's deadlock detector. With lot-linked spends
// the allocator discovers the shortfall under that lock.
func TestTheLoyaltyAllocatorHoldsOnItsOwn(t *testing.T) {
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
			// `store` and `admin` hold no INSERT on the ledger.
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

	// Every loser must have been refused by THIS guard.
	for i, why := range refusals {
		if won[i] {
			continue
		}
		if why != "loyalty_entries_within_balance" {
			t.Errorf("racer %d was refused by %q, not by the guard — the lock is "+
				"not what is serialising these", i, why)
		}
	}
}

// constraintOf names what refused a statement.
func constraintOf(err error) string {
	if err == nil {
		return ""
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return err.Error()
	}
	if pgErr.ConstraintName != "" {
		return pgErr.ConstraintName
	}
	return pgErr.Code
}
