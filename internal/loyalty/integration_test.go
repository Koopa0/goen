//go:build integration

package loyalty_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
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
	// The seed supplies shipping_method_versions: an order needs one and the
	// migration creates none.
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

func redemptionOperation(t *testing.T, userID string) uuid.UUID {
	t.Helper()
	if _, err := uuid.Parse(userID); err != nil {
		t.Fatalf("invalid redemption owner fixture: %v", err)
	}
	return uuid.New()
}

// A timeout while writing store credit says nothing about the points the
// customer has. The fixture deliberately holds more than the request, or the
// preflight balance check would pass this test without reaching the write.
func TestAnInfrastructureFailureIsNotReportedAsAnEmptyBalance(t *testing.T) {
	ctx := t.Context()
	userID, _ := customer(t, 500)
	operationID := redemptionOperation(t, userID)

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

	_, err = loyalty.NewStore(timeoutPool).Redeem(ctx, userID, 100, operationID)
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
	operationID := redemptionOperation(t, userID)

	cents, err := s.Redeem(ctx, userID, 500, operationID)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if cents != 5000 {
		t.Errorf("500 points became %d cents, want 5000", cents)
	}
	if replayed, replayErr := s.Redeem(ctx, userID, 500, operationID); replayErr != nil || replayed != cents {
		t.Fatalf("exact replay = %d, %v; want %d and nil", replayed, replayErr, cents)
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

func TestAnOversizedRedemptionIsRejectedBeforeItClaimsAnOperation(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 100)
	operationID := redemptionOperation(t, userID)

	_, err := loyalty.NewStore(pool).Redeem(
		ctx, userID, loyalty.MaxRedemptionPoints+10, operationID,
	)
	if !errors.Is(err, loyalty.ErrTooSmall) {
		t.Fatalf("oversized redemption error = %v, want ErrTooSmall", err)
	}

	var operations, spends, credits int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM loyalty_redemption_operations WHERE id=$1),
			(SELECT count(*) FROM loyalty_entries WHERE account_id=$2 AND kind='spend'),
			(SELECT count(*) FROM store_credit_entries
			 WHERE account_id=$2 AND reason='points')`, operationID, accountID).
		Scan(&operations, &spends, &credits); err != nil {
		t.Fatalf("read rejected redemption effects: %v", err)
	}
	if operations != 0 || spends != 0 || credits != 0 {
		t.Fatalf("oversized redemption left operations/spends/credits = %d/%d/%d, want 0/0/0",
			operations, spends, credits)
	}
}

func TestAnOversizedRedemptionPOSTRedirectsWithoutDatabaseEffects(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 100)
	operationID := redemptionOperation(t, userID)
	form := url.Values{
		"points":       {strconv.FormatInt(loyalty.MaxRedemptionPoints+10, 10)},
		"operation_id": {operationID.String()},
	}
	req := httptest.NewRequestWithContext(
		account.WithUser(ctx, account.User{ID: userID, Role: "customer"}),
		http.MethodPost, "/account/points", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	loyalty.NewHandler(loyalty.NewStore(pool), slog.New(slog.DiscardHandler)).Redeem(res, req)

	if res.Code != http.StatusSeeOther {
		t.Fatalf("oversized POST status = %d, want 303", res.Code)
	}
	if location := res.Header().Get("Location"); location != "/account/points?small=1" {
		t.Fatalf("oversized POST location = %q, want small notice", location)
	}
	var effects int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM loyalty_redemption_operations WHERE id=$1)
			+ (SELECT count(*) FROM loyalty_entries
			   WHERE account_id=$2 AND kind='spend')
			+ (SELECT count(*) FROM store_credit_entries
			   WHERE account_id=$2 AND reason='points')`, operationID, accountID).Scan(&effects); err != nil {
		t.Fatalf("read oversized POST effects: %v", err)
	}
	if effects != 0 {
		t.Fatalf("oversized POST left %d economic rows, want 0", effects)
	}
}

func TestErasingARedeemerRemovesTheOperationButKeepsAnonymousLedgers(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 100)
	owner := uuid.MustParse(userID)
	operationID := redemptionOperation(t, userID)
	if _, err := loyalty.NewStore(pool).Redeem(ctx, userID, 100, operationID); err != nil {
		t.Fatalf("redeem before erasure: %v", err)
	}

	if _, err := pool.Exec(ctx, `SELECT erase_user($1)`, owner); err != nil {
		t.Fatalf("erase redeemer: %v", err)
	}

	var users, operations, loyaltyRows, creditRows int
	var accountOwner uuid.NullUUID
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM users WHERE id=$1),
			(SELECT count(*) FROM loyalty_redemption_operations WHERE id=$2),
			(SELECT count(*) FROM loyalty_entries WHERE account_id=$3),
			(SELECT count(*) FROM store_credit_entries WHERE account_id=$3),
			(SELECT user_id FROM store_credit_accounts WHERE id=$3)`,
		owner, operationID, accountID).
		Scan(&users, &operations, &loyaltyRows, &creditRows, &accountOwner); err != nil {
		t.Fatalf("read erased redemption state: %v", err)
	}
	if users != 0 || operations != 0 || accountOwner.Valid {
		t.Fatalf("erasure left users/operations/account owner = %d/%d/%v, want 0/0/null",
			users, operations, accountOwner)
	}
	if loyaltyRows != 2 || creditRows != 1 {
		t.Fatalf("anonymous ledgers have loyalty/credit rows = %d/%d, want 2/1",
			loyaltyRows, creditRows)
	}
}

// Expiry is applied on read, never by a job.
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
	if _, err := s.Redeem(ctx, userID, 500, redemptionOperation(t, userID)); !errors.Is(err, loyalty.ErrNotEnough) {
		t.Errorf("spending expired points gave %v, want ErrNotEnough", err)
	}
	if _, err := s.Redeem(ctx, userID, 200, redemptionOperation(t, userID)); err != nil {
		t.Errorf("spending the unexpired points was refused: %v", err)
	}
}

// The two lots must expire on different days: if both expired a year from now,
// every assertion would pass with an unlotted spend and prove nothing.
func TestASpentAwardLeavesTheBalanceWithIt(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 0)
	orderA := orderFor(t, userID, 1000000)
	orderB := orderFor(t, userID, 1000000)

	var lotA, lotB uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO loyalty_entries
		    (account_id, kind, points, reason, idempotency_key, order_id, expires_on)
		VALUES ($1, 'award', 100, 'boundary', 'boundary:' || ($2::uuid)::text,
		        $2::uuid, shop_today())
		RETURNING id`, accountID, orderA).Scan(&lotA); err != nil {
		t.Fatalf("award lot A: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO loyalty_entries
		    (account_id, kind, points, reason, idempotency_key, order_id, expires_on)
		VALUES ($1, 'award', 100, 'boundary', 'boundary:' || ($2::uuid)::text,
		        $2::uuid, shop_today() + 365)
		RETURNING id`, accountID, orderB).Scan(&lotB); err != nil {
		t.Fatalf("award lot B: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`SELECT redeem_loyalty_points($1, 150, $2)`, uuid.MustParse(userID), redemptionOperation(t, userID)); err != nil {
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

// The tie-breaks after the expiry date: created_at first, then id.
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
			// Deliberately reverse UUID order, or dropping the created_at
			// tie-break would still pass on the id one.
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
			userID, accountID := customer(t, 0)
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
				`SELECT redeem_loyalty_points($1, 100, $2)`,
				uuid.MustParse(userID), redemptionOperation(t, userID)); err != nil {
				t.Fatalf("redeem: %v", err)
			}
			for _, want := range []struct {
				lot    uuid.UUID
				points int64
			}{{tc.firstID, -60}, {tc.secondID, -40}} {
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

// The zero must survive every layer that could turn it back into "no points":
// query, view model, locale lookup, page component.
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
	returnID := paidReturnFor(t, orderID, userID, 750000)
	var reversed int64
	if err := pool.QueryRow(ctx,
		`SELECT reverse_return_points($1)`, returnID).Scan(&reversed); err != nil {
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
	userID, accountID := customer(t, 0)
	var lotID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on)
		VALUES ($1, 'award', 100, 'old', 'lapsed:' || gen_random_uuid(), shop_today() - 1)
		RETURNING id`, accountID).Scan(&lotID); err != nil {
		t.Fatalf("seed lapsed lot: %v", err)
	}

	_, err := pool.Exec(ctx,
		`SELECT redeem_loyalty_points($1, 100, $2)`, uuid.MustParse(userID), redemptionOperation(t, userID))
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
	for _, tc := range []struct {
		name       string
		spent      int64
		wantActual int64
	}{
		{"partly consumed", 100, 100},
		{"wholly consumed records a zero row", 200, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			userID, accountID := customer(t, 0)
			orderID := orderFor(t, userID, 2000000)
			if err := pool.QueryRow(ctx,
				`SELECT award_loyalty_points($1)`, orderID).
				Scan(new(int64)); err != nil {
				t.Fatalf("award: %v", err)
			}
			if _, err := pool.Exec(ctx,
				`SELECT redeem_loyalty_points($1, $2, $3)`,
				uuid.MustParse(userID), tc.spent, redemptionOperation(t, userID)); err != nil {
				t.Fatalf("redeem: %v", err)
			}
			returnID := paidReturnFor(t, orderID, userID, 2000000)
			var actual int64
			if err := pool.QueryRow(ctx,
				`SELECT reverse_return_points($1)`, returnID).Scan(&actual); err != nil {
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
			if points != -tc.wantActual || requested != 200 {
				t.Errorf("clawback row = points %d requested %d, want %d/200",
					points, requested, -tc.wantActual)
			}
			if got := balance(t, accountID); got != 0 {
				t.Errorf("balance = %d, want 0", got)
			}
		})
	}
}

func TestReturnPointSlicesFollowAccountLockedPostingOrderDespiteInvertedTimestamps(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 0)
	orderID, firstReturn, secondReturn := splitReturnOrderFor(t, userID)
	if _, err := pool.Exec(ctx, `
		INSERT INTO loyalty_entries
		    (account_id, kind, points, reason, idempotency_key, order_id, expires_on)
		VALUES ($1, 'award', 3, 'rounding fixture', 'rounding:'||($2::uuid)::text,
		        $2::uuid, shop_today()+365)`, accountID, orderID); err != nil {
		t.Fatalf("seed three-point award: %v", err)
	}

	firstKey := pendingReturnRefund(t, orderID, firstReturn, 50)
	secondKey := pendingReturnRefund(t, orderID, secondReturn, 50)

	// Begin A early, then block it on its refund row. B settles, commits and posts
	// its clawback first. A eventually commits with an OLDER transaction now().
	// Timestamp ordering would then move A in front of immutable B and lose one
	// of the order's three points; the account-locked posting delta cannot.
	blocker, err := pgx.Connect(ctx, pool.Config().ConnString())
	if err != nil {
		t.Fatalf("open settlement blocker: %v", err)
	}
	cleanupCtx := context.WithoutCancel(t.Context())
	t.Cleanup(func() { _ = blocker.Close(cleanupCtx) })
	blockTx, err := blocker.Begin(ctx)
	if err != nil {
		t.Fatalf("begin settlement blocker: %v", err)
	}
	if _, execErr := blockTx.Exec(ctx,
		`SELECT id FROM refunds WHERE request_key=$1 FOR UPDATE`, firstKey); execErr != nil {
		t.Fatalf("lock first refund: %v", execErr)
	}

	cfg, err := pgx.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse early settlement config: %v", err)
	}
	const appName = "loyalty-inverted-settlement"
	cfg.RuntimeParams["application_name"] = appName
	early, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open early settlement connection: %v", err)
	}
	t.Cleanup(func() { _ = early.Close(cleanupCtx) })
	earlyTx, err := early.Begin(ctx)
	if err != nil {
		t.Fatalf("begin early settlement: %v", err)
	}
	if _, err := earlyTx.Exec(ctx, `SELECT now()`); err != nil {
		t.Fatalf("establish early transaction time: %v", err)
	}
	earlyDone := make(chan error, 1)
	go func() {
		tag, execErr := earlyTx.Exec(ctx, `
			UPDATE refunds
			SET provider_ref=$2, status='succeeded', succeeded_at=now()
			WHERE request_key=$1 AND status='pending'`,
			firstKey, "rf_"+firstReturn.String())
		if execErr != nil {
			earlyDone <- execErr
			return
		}
		if tag.RowsAffected() != 1 {
			earlyDone <- fmt.Errorf("updated %d first refund rows, want 1", tag.RowsAffected())
			return
		}
		earlyDone <- earlyTx.Commit(ctx)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE application_name=$1 AND wait_event_type='Lock'
			)`, appName).Scan(&waiting); err != nil {
			t.Fatalf("observe blocked settlement: %v", err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("early settlement never blocked on its refund row")
		}
		time.Sleep(time.Millisecond)
	}

	if tag, err := pool.Exec(ctx, `
		UPDATE refunds
		SET provider_ref=$2, status='succeeded', succeeded_at=now()
		WHERE request_key=$1 AND status='pending'`,
		secondKey, "rf_"+secondReturn.String()); err != nil {
		t.Fatalf("settle second return first: %v", err)
	} else if tag.RowsAffected() != 1 {
		t.Fatalf("settle second return first updated %d rows, want 1", tag.RowsAffected())
	}
	var secondActual int64
	if err := pool.QueryRow(ctx,
		`SELECT reverse_return_points($1)`, secondReturn).Scan(&secondActual); err != nil {
		t.Fatalf("reverse second return first: %v", err)
	}
	if secondActual != 1 {
		t.Fatalf("first 50%% payout reversed %d points, want 1", secondActual)
	}
	if err := blockTx.Commit(ctx); err != nil {
		t.Fatalf("release first settlement: %v", err)
	}
	if err := <-earlyDone; err != nil {
		t.Fatalf("complete early-started settlement: %v", err)
	}

	var firstAt, secondAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT succeeded_at FROM refunds WHERE request_key=$1),
			(SELECT succeeded_at FROM refunds WHERE request_key=$2)`,
		firstKey, secondKey).Scan(&firstAt, &secondAt); err != nil {
		t.Fatalf("read inverted settlement times: %v", err)
	}
	if !firstAt.Before(secondAt) {
		t.Fatalf("fixture did not invert transaction time: first=%s second=%s", firstAt, secondAt)
	}
	var allocation, firstActual int64
	if err := pool.QueryRow(ctx,
		`SELECT return_loyalty_points_allocation($1)`, firstReturn).Scan(&allocation); err != nil {
		t.Fatalf("read remaining allocation: %v", err)
	}
	if allocation != 2 {
		t.Fatalf("remaining allocation = %d, want 2", allocation)
	}
	if err := pool.QueryRow(ctx,
		`SELECT reverse_return_points($1)`, firstReturn).Scan(&firstActual); err != nil {
		t.Fatalf("reverse late-committing first return: %v", err)
	}
	if firstActual != 2 {
		t.Fatalf("late-committing return reversed %d points, want 2", firstActual)
	}
	var replay int64
	if err := pool.QueryRow(ctx,
		`SELECT reverse_return_points($1)`, firstReturn).Scan(&replay); err != nil || replay != 0 {
		t.Fatalf("exact clawback replay = %d, %v; want 0, nil", replay, err)
	}

	var requested, reversed int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(requested_points),0)::bigint,
		       coalesce(sum(-points),0)::bigint
		FROM loyalty_entries
		WHERE order_id=$1 AND kind='clawback'`, orderID).Scan(&requested, &reversed); err != nil {
		t.Fatalf("read order clawbacks: %v", err)
	}
	if requested != 3 || reversed != 3 {
		t.Fatalf("requested/reversed points = %d/%d, want 3/3", requested, reversed)
	}
}

// At-least-once delivery awards exactly once, keyed on the order in the
// database rather than checked in Go.
func TestAnOrderIsAwardedOnce(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 0)
	orderID := orderFor(t, userID, 250000) // NT$2,500 → 25 points

	for i := range 3 {
		var awarded int64
		if err := pool.QueryRow(ctx,
			`SELECT award_loyalty_points($1)`, orderID).Scan(&awarded); err != nil {
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
		`SELECT award_loyalty_points($1)`, orderID).Scan(&awarded); err != nil {
		t.Fatalf("a guest order errored: %v", err)
	}
	if awarded != 0 {
		t.Errorf("a guest order was awarded %d points", awarded)
	}
}

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
	if _, err := tx.Exec(ctx, `
		INSERT INTO payments
		    (order_id, provider_ref, status, intended_amount_cents,
		     captured_amount_cents, paid_at)
		VALUES ($1::uuid, 'loyalty:' || $1::uuid::text, 'succeeded', $2, $2, now())`,
		orderID, cents); err != nil {
		t.Fatalf("commit order: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return orderID
}

// paidReturnFor creates one realistic approved return whose card payout has
// durably succeeded. reverse_return_points deliberately refuses a bare return
// UUID: order, amount and eligibility must all come from this history.
func paidReturnFor(t *testing.T, orderID uuid.UUID, userID string, cents int64) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	returnID, paymentID := approvedReturnFor(t, orderID, userID)
	key := "points-return:" + returnID.String()
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds
		    (payment_id, return_request_id, request_key, amount_cents, reason,
		     provider_ref, status, succeeded_at)
		VALUES ($1,$2,$3,$4,'points return',$5,'succeeded',now())`,
		paymentID, returnID, key, cents, "rf_"+returnID.String()); err != nil {
		t.Fatalf("create paid return refund: %v", err)
	}
	return returnID
}

func approvedReturnFor(
	t *testing.T, orderID uuid.UUID, userID string,
) (returnID, paymentID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	var lineID, shipmentID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM order_lines WHERE order_id=$1 ORDER BY position,id LIMIT 1`, orderID).
		Scan(&lineID); err != nil {
		t.Fatalf("read return line: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM payments WHERE order_id=$1 AND status='succeeded' LIMIT 1`, orderID).
		Scan(&paymentID); err != nil {
		t.Fatalf("read return payment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE orders SET fulfillment_status='picking'
		WHERE id=$1 AND fulfillment_status='pending'`, orderID); err != nil {
		t.Fatalf("pick return order: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'loyalty-test', 'LOY-'||gen_random_uuid()) RETURNING id`, orderID).
		Scan(&shipmentID); err != nil {
		t.Fatalf("create return shipment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1,$2,$3,1)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("ship return line: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE orders SET fulfillment_status='shipped'
		WHERE id=$1 AND fulfillment_status='picking'`, orderID); err != nil {
		t.Fatalf("mark return order shipped: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, requested_by_user_id, reason)
		VALUES ($1,$2,'points return') RETURNING id`, orderID, uuid.MustParse(userID)).
		Scan(&returnID); err != nil {
		t.Fatalf("create return: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1,$2,$3,1)`, orderID, returnID, lineID); err != nil {
		t.Fatalf("create return line: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests SET status='approved', decided_at=now() WHERE id=$1`, returnID); err != nil {
		t.Fatalf("approve return: %v", err)
	}
	return returnID, paymentID
}

func splitReturnOrderFor(
	t *testing.T, userID string,
) (orderID, firstReturnID, secondReturnID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	owner := uuid.MustParse(userID)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin split-return order: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var lineID, shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id=v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id`, owner).Scan(&orderID); err != nil {
		t.Fatalf("create split-return order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data
		    (order_id,email,recipient_name,phone,postal_code,city,district,street)
		VALUES ($1,'split@example.com','收件','0912345678','110','台北市','信義區','路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create split-return delivery: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines
		    (order_id,sku,product_name,unit_price_cents,quantity)
		VALUES ($1,'SPLIT-POINTS','點數分攤',50,2) RETURNING id`, orderID).Scan(&lineID); err != nil {
		t.Fatalf("create split-return line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payments
		    (order_id,provider_ref,status,intended_amount_cents,captured_amount_cents,paid_at)
		VALUES ($1::uuid,'split:'||$1::uuid::text,'succeeded',100,100,now())`, orderID); err != nil {
		t.Fatalf("pay split-return order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE orders SET fulfillment_status='picking'
		WHERE id=$1 AND fulfillment_status='pending'`, orderID); err != nil {
		t.Fatalf("pick split-return order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id,carrier,tracking_number)
		VALUES ($1,'loyalty-test','SPLIT-'||gen_random_uuid()) RETURNING id`, orderID).
		Scan(&shipmentID); err != nil {
		t.Fatalf("create split-return shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id,shipment_id,order_line_id,quantity)
		VALUES ($1,$2,$3,2)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("ship split-return line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE orders SET fulfillment_status='shipped'
		WHERE id=$1 AND fulfillment_status='picking'`, orderID); err != nil {
		t.Fatalf("mark split-return order shipped: %v", err)
	}

	returns := make([]uuid.UUID, 2)
	for i := range returns {
		if err := tx.QueryRow(ctx, `
			INSERT INTO return_requests (order_id,requested_by_user_id,reason)
			VALUES ($1,$2,'split points') RETURNING id`, orderID, owner).Scan(&returns[i]); err != nil {
			t.Fatalf("create split return %d: %v", i, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO return_request_lines
			    (order_id,return_request_id,order_line_id,quantity)
			VALUES ($1,$2,$3,1)`, orderID, returns[i], lineID); err != nil {
			t.Fatalf("create split return line %d: %v", i, err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE return_requests SET status='approved',decided_at=now() WHERE id=$1`,
			returns[i]); err != nil {
			t.Fatalf("approve split return %d: %v", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit split-return order: %v", err)
	}
	return orderID, returns[0], returns[1]
}

func pendingReturnRefund(t *testing.T, orderID, returnID uuid.UUID, cents int64) string {
	t.Helper()
	ctx := t.Context()
	var paymentID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM payments WHERE order_id=$1 AND status='succeeded'`, orderID).
		Scan(&paymentID); err != nil {
		t.Fatalf("read split-return payment: %v", err)
	}
	key := "split-return:" + returnID.String()
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds
		    (payment_id, return_request_id, request_key, amount_cents, reason, status)
		VALUES ($1,$2,$3,$4,'split points','pending')`,
		paymentID, returnID, key, cents); err != nil {
		t.Fatalf("create pending split refund: %v", err)
	}
	return key
}

func openOrderFor(t *testing.T, userID string, cents int64) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	owner := uuid.MustParse(userID)
	var orderID, lineID uuid.UUID
	if err := pool.QueryRow(ctx, `
		WITH o AS (
			INSERT INTO orders (order_number, user_id, shipping_version_id,
			                    shipping_method_code, shipping_method_name, shipping_cents)
			SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
			FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
			ORDER BY v.effective_at LIMIT 1 RETURNING id
		), p AS (
			INSERT INTO order_private_data (order_id, email, recipient_name, phone,
			                                postal_code, city, district, street)
			SELECT id, 'role@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號'
			FROM o
		)
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		SELECT id, 'ROLE-SKU', '權限測試', $2, 1 FROM o
		RETURNING order_id, id`, owner, cents).Scan(&orderID, &lineID); err != nil {
		t.Fatalf("create open role-test order: %v", err)
	}
	return orderID
}

func roleConn(t *testing.T, role string) *pgxpool.Conn {
	t.Helper()
	if role != "store" && role != "admin" {
		t.Fatalf("unsupported test role %q", role)
	}
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatalf("acquire %s connection: %v", role, err)
	}
	if _, err := conn.Exec(t.Context(), "SET ROLE "+role); err != nil {
		conn.Release()
		t.Fatalf("set role %s: %v", role, err)
	}
	cleanupCtx := context.WithoutCancel(t.Context())
	t.Cleanup(func() {
		_, _ = conn.Exec(cleanupCtx, `RESET ROLE`)
		conn.Release()
	})
	return conn
}

func requirePermissionDenied(t *testing.T, err error) {
	t.Helper()
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != "42501" {
		t.Fatalf("call failed with %v, want permission_denied (42501)", err)
	}
}

func TestMoneyPostingDoorsAreRoleNarrowAndDataAuthoritative(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 0)
	owner := uuid.MustParse(userID)
	var actor uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role) VALUES ('door-staff-'||gen_random_uuid()||'@goen.invalid', 'staff')
		RETURNING id`).Scan(&actor); err != nil {
		t.Fatalf("create staff actor: %v", err)
	}

	for _, tc := range []struct {
		role, signature string
		want            bool
	}{
		{"store", "post_store_credit(uuid,bigint,text,uuid,text,uuid)", false},
		{"admin", "post_store_credit(uuid,bigint,text,uuid,text,uuid)", false},
		{"store", "spend_store_credit(uuid,bigint)", true},
		{"admin", "spend_store_credit(uuid,bigint)", false},
		{"store", "grant_store_credit(uuid,bigint,text,uuid,uuid)", false},
		{"admin", "grant_store_credit(uuid,bigint,text,uuid,uuid)", true},
		{"store", "compensate_return_with_credit(uuid,bigint,uuid)", false},
		{"admin", "compensate_return_with_credit(uuid,bigint,uuid)", true},
		{"store", "redeem_loyalty_points(uuid,bigint,uuid)", true},
		{"admin", "redeem_loyalty_points(uuid,bigint,uuid)", false},
		{"store", "award_loyalty_points(uuid)", true},
		{"admin", "award_loyalty_points(uuid)", false},
		{"store", "reverse_return_points(uuid)", false},
		{"admin", "reverse_return_points(uuid)", true},
		{"store", "hold_inventory(uuid,uuid,integer,interval,text)", true},
		{"admin", "hold_inventory(uuid,uuid,integer,interval,text)", false},
	} {
		var got bool
		if err := pool.QueryRow(ctx,
			`SELECT has_function_privilege($1, $2, 'EXECUTE')`, tc.role, tc.signature).Scan(&got); err != nil {
			t.Fatalf("read %s privilege on %s: %v", tc.role, tc.signature, err)
		}
		if got != tc.want {
			t.Errorf("%s execute %s = %t, want %t", tc.role, tc.signature, got, tc.want)
		}
	}

	store := roleConn(t, "store")
	adminRole := roleConn(t, "admin")
	_, err := store.Exec(ctx, `SELECT post_store_credit($1, 1, 'mint', NULL, 'mint', NULL)`, owner)
	requirePermissionDenied(t, err)
	_, err = adminRole.Exec(ctx, `SELECT post_store_credit($1, -1, 'debit', NULL, 'debit', $2)`, owner, actor)
	requirePermissionDenied(t, err)
	_, err = store.Exec(ctx, `SELECT grant_store_credit($1, 100, 'mint', $2, $3)`, owner, actor, uuid.New())
	requirePermissionDenied(t, err)

	// Positive admin control: a bounded grant is attributed to a durable staff user.
	grantOperation := uuid.New()
	if _, execErr := adminRole.Exec(ctx,
		`SELECT grant_store_credit($1, 2000, 'door fixture', $2, $3)`, owner, actor, grantOperation); execErr != nil {
		t.Fatalf("legal admin grant: %v", execErr)
	}
	_, err = adminRole.Exec(ctx,
		`SELECT grant_store_credit($1, -1, 'debit', $2, $3)`, owner, actor, uuid.New())
	if why := constraintOf(err); why != "store_credit_grant_amount" {
		t.Fatalf("negative admin grant refused by %q, want store_credit_grant_amount", why)
	}
	_, err = adminRole.Exec(ctx,
		`SELECT grant_store_credit($1, 100, 'forged actor', $1, $2)`, owner, uuid.New())
	if why := constraintOf(err); why != "store_credit_grant_actor" {
		t.Fatalf("customer actor refused by %q, want store_credit_grant_actor", why)
	}
	_, err = adminRole.Exec(ctx,
		`SELECT grant_store_credit($1, 100, E'\t', $2, $3)`, owner, actor, uuid.New())
	if why := constraintOf(err); why != "store_credit_grant_reason" {
		t.Fatalf("blank grant reason refused by %q, want store_credit_grant_reason", why)
	}
	_, err = adminRole.Exec(ctx,
		`SELECT grant_store_credit($1, 100, 'operation', $2, $3)`, owner, actor, uuid.Nil)
	if why := constraintOf(err); why != "store_credit_grant_operation" {
		t.Fatalf("blank grant operation refused by %q, want store_credit_grant_operation", why)
	}
	_, err = adminRole.Exec(ctx,
		`SELECT grant_store_credit($1, 2001, 'door fixture', $2, $3)`, owner, actor, grantOperation)
	if why := constraintOf(err); why != "store_credit_idempotency_attribution" {
		t.Fatalf("grant operation collision refused by %q, want store_credit_idempotency_attribution", why)
	}

	spendOrder := openOrderFor(t, userID, 1000)
	_, err = adminRole.Exec(ctx, `SELECT spend_store_credit($1, -100)`, spendOrder)
	requirePermissionDenied(t, err)
	_, err = store.Exec(ctx, `SELECT spend_store_credit($1, -100)`, uuid.New())
	if why := constraintOf(err); why != "store_credit_checkout_owner" {
		t.Fatalf("unknown checkout order refused by %q, want store_credit_checkout_owner", why)
	}
	_, err = store.Exec(ctx, `SELECT spend_store_credit($1, 100)`, spendOrder)
	if why := constraintOf(err); why != "store_credit_checkout_debit" {
		t.Fatalf("positive checkout posting refused by %q, want store_credit_checkout_debit", why)
	}
	_, err = store.Exec(ctx, `SELECT spend_store_credit($1, -1001)`, spendOrder)
	if why := constraintOf(err); why != "store_credit_checkout_amount" {
		t.Fatalf("oversized debit refused by %q, want store_credit_checkout_amount", why)
	}
	if _, execErr := store.Exec(ctx, `SELECT spend_store_credit($1, -400)`, spendOrder); execErr != nil {
		t.Fatalf("legal partial checkout spend: %v", execErr)
	}
	attributionOrder := openOrderFor(t, userID, 1000)
	if _, execErr := pool.Exec(ctx, `
		SELECT post_store_credit($1, -1, 'collision fixture', NULL,
		                         'order:'||$2::text, NULL)`, owner, attributionOrder); execErr != nil {
		t.Fatalf("seed mismatched checkout attribution: %v", execErr)
	}
	_, err = store.Exec(ctx, `SELECT spend_store_credit($1, -100)`, attributionOrder)
	if why := constraintOf(err); why != "store_credit_checkout_attribution" {
		t.Fatalf("mismatched checkout key refused by %q, want store_credit_checkout_attribution", why)
	}
	_, err = store.Exec(ctx, `SELECT reverse_order_credit($1)`, spendOrder)
	if why := constraintOf(err); why != "store_credit_posting_matches_order" {
		t.Fatalf("cross-object open-order reversal refused by %q, want store_credit_posting_matches_order", why)
	}

	precommit := openOrderFor(t, userID, 250000)
	_, err = adminRole.Exec(ctx, `SELECT award_loyalty_points($1)`, precommit)
	requirePermissionDenied(t, err)
	_, err = adminRole.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, interval '1 minute', 'admin-hold')`, precommit, uuid.New())
	requirePermissionDenied(t, err)
	_, err = store.Exec(ctx, `SELECT award_loyalty_points($1)`, precommit)
	if why := constraintOf(err); why != "loyalty_award_committed_order" {
		t.Fatalf("precommit award refused by %q, want loyalty_award_committed_order", why)
	}
	paid := orderFor(t, userID, 250000)
	var awarded int64
	if queryErr := store.QueryRow(ctx, `SELECT award_loyalty_points($1)`, paid).Scan(&awarded); queryErr != nil {
		t.Fatalf("legal derived award: %v", queryErr)
	}
	if awarded != 25 {
		t.Fatalf("derived award = %d, want 25", awarded)
	}

	if _, execErr := pool.Exec(ctx, `
		INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on)
		VALUES ($1, 'award', 100, 'door seed', 'door-points:'||gen_random_uuid(), shop_today()+365)`,
		accountID); execErr != nil {
		t.Fatalf("seed redeemable lot: %v", execErr)
	}
	_, err = adminRole.Exec(ctx,
		`SELECT redeem_loyalty_points($1, 100, $2)`, owner, uuid.New())
	requirePermissionDenied(t, err)
	_, err = store.Exec(ctx,
		`SELECT redeem_loyalty_points($1, 100, $2)`, uuid.New(), uuid.New())
	if why := constraintOf(err); why != "loyalty_redemption_owner" {
		t.Fatalf("unknown redemption owner refused by %q, want loyalty_redemption_owner", why)
	}
	var cents int64
	redeemOperation := uuid.New()
	if queryErr := store.QueryRow(ctx,
		`SELECT redeem_loyalty_points($1, 100, $2)`, owner, redeemOperation).Scan(&cents); queryErr != nil {
		t.Fatalf("legal redemption: %v", queryErr)
	}
	if cents != 1000 {
		t.Fatalf("100 points produced %d cents, want database-derived 1000", cents)
	}
	if queryErr := store.QueryRow(ctx,
		`SELECT redeem_loyalty_points($1, 100, $2)`, owner, redeemOperation).Scan(&cents); queryErr != nil {
		t.Fatalf("exact redemption replay: %v", queryErr)
	}
	otherUserID, _ := customer(t, 100)
	_, err = store.Exec(ctx,
		`SELECT redeem_loyalty_points($1, 100, $2)`, uuid.MustParse(otherUserID), redeemOperation)
	if why := constraintOf(err); why != "loyalty_redemption_operation_owner" {
		t.Fatalf("cross-owner redemption replay refused by %q, want loyalty_redemption_operation_owner", why)
	}
	_, err = store.Exec(ctx,
		`SELECT redeem_loyalty_points($1, 200, $2)`, owner, redeemOperation)
	if why := constraintOf(err); why != "loyalty_redemption_attribution" {
		t.Fatalf("changed redemption replay refused by %q, want loyalty_redemption_attribution", why)
	}

	pointsOrder := orderFor(t, userID, 1000000)
	if _, execErr := store.Exec(ctx, `SELECT award_loyalty_points($1)`, pointsOrder); execErr != nil {
		t.Fatalf("award return fixture: %v", execErr)
	}
	unpaidReturn, paymentID := approvedReturnFor(t, pointsOrder, userID)
	_, err = store.Exec(ctx, `SELECT reverse_return_points($1)`, unpaidReturn)
	requirePermissionDenied(t, err)
	_, err = adminRole.Exec(ctx, `SELECT reverse_return_points($1)`, uuid.New())
	if why := constraintOf(err); why != "loyalty_return_approved" {
		t.Fatalf("unknown return clawback refused by %q, want loyalty_return_approved", why)
	}
	_, err = adminRole.Exec(ctx, `SELECT reverse_return_points($1)`, unpaidReturn)
	if why := constraintOf(err); why != "loyalty_return_paid" {
		t.Fatalf("unpaid return clawback refused by %q, want loyalty_return_paid", why)
	}
	returnKey := "door-return:" + unpaidReturn.String()
	if _, execErr := pool.Exec(ctx, `
		INSERT INTO refunds
		    (payment_id, return_request_id, request_key, amount_cents, reason,
		     provider_ref, status, succeeded_at)
		VALUES ($1,$2,$3,1000000,'door return',$4,'succeeded',now())`,
		paymentID, unpaidReturn, returnKey, "rf_"+unpaidReturn.String()); execErr != nil {
		t.Fatalf("create paid return payout: %v", execErr)
	}
	var reversed int64
	if queryErr := adminRole.QueryRow(ctx,
		`SELECT reverse_return_points($1)`, unpaidReturn).Scan(&reversed); queryErr != nil {
		t.Fatalf("legal return clawback: %v", queryErr)
	}
	if reversed != 100 {
		t.Fatalf("derived return clawback = %d, want 100", reversed)
	}

	if _, execErr := pool.Exec(ctx, `
		INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents,
		                      captured_amount_cents, paid_at)
		VALUES ($1::uuid, 'door-card:'||($1::uuid)::text,
		        'succeeded', 600, 600, now())`, spendOrder); execErr != nil {
		t.Fatalf("capture card remainder: %v", execErr)
	}
	returnID, _ := approvedReturnFor(t, spendOrder, userID)
	_, err = store.Exec(ctx,
		`SELECT compensate_return_with_credit($1, 400, $2)`, returnID, actor)
	requirePermissionDenied(t, err)
	_, err = adminRole.Exec(ctx,
		`SELECT compensate_return_with_credit($1, -1, $2)`, returnID, actor)
	if why := constraintOf(err); why != "store_credit_return_amount" {
		t.Fatalf("negative return credit refused by %q, want store_credit_return_amount", why)
	}
	_, err = adminRole.Exec(ctx,
		`SELECT compensate_return_with_credit($1, 400, $2)`, returnID, owner)
	if why := constraintOf(err); why != "store_credit_return_actor" {
		t.Fatalf("customer return actor refused by %q, want store_credit_return_actor", why)
	}
	if _, execErr := adminRole.Exec(ctx,
		`SELECT compensate_return_with_credit($1, 400, $2)`, returnID, actor); execErr != nil {
		t.Fatalf("legal durable return compensation: %v", execErr)
	}
	_, err = adminRole.Exec(ctx,
		`SELECT compensate_return_with_credit($1, 1, $2)`, uuid.New(), actor)
	if why := constraintOf(err); why != "store_credit_return_owner" {
		t.Fatalf("unknown return refused by %q, want store_credit_return_owner", why)
	}
}

// TestTheLoyaltyAllocatorHoldsOnItsOwn proves the account lock is what
// serialises spends, not PostgreSQL's deadlock detector. With lot-linked spends
// the allocator discovers the shortfall under that lock.
func TestTheLoyaltyAllocatorHoldsOnItsOwn(t *testing.T) {
	ctx := t.Context()
	userID, accountID := customer(t, 1000)

	const racers = 8
	start := make(chan struct{})
	won := make([]bool, racers)
	refusals := make([]string, racers)
	operations := make([]uuid.UUID, racers)
	for i := range operations {
		operations[i] = redemptionOperation(t, userID)
	}
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			<-start
			// Through the posting FUNCTION, which is the only path goen has:
			// `store` and `admin` hold no INSERT on the ledger.
			_, err := pool.Exec(ctx, `
				SELECT redeem_loyalty_points($1, 1000, $2)`,
				uuid.MustParse(userID), operations[i])
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
