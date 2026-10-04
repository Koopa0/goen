//go:build integration

package admin_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/campaigns"
	contentdesk "github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/admin/customers"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/admin/loyalty"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/admin/refundstate"
	returndesk "github.com/koopa0/goen/internal/admin/returns"
	"github.com/koopa0/goen/internal/admin/shipping"
	roster "github.com/koopa0/goen/internal/admin/staff"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/site"
	"github.com/koopa0/goen/internal/ui/icons"
	adminpages "github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/warranty"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	if err := admintest.LoadCatalogue(context.Background(), pool); err != nil {
		slog.Error("load seed", "error", err)
		os.Exit(1)
	}

	code := m.Run()
	stop()
	os.Exit(code)
}

var backOffice = admintest.BackOffice

func healthHandler(p *pgxpool.Pool) *health.Handler {
	log := slog.New(slog.DiscardHandler)
	return health.NewHandler(health.NewStore(p), outbox.NewStore(p, log), nil, log)
}

// returnDeskOver builds the returns desk and the refunds it pays through over
// one pool and one refunder, as cmd/goen does.
func returnDeskOver(p *pgxpool.Pool, refunder refunds.Refunder) *returndesk.Store {
	return returndesk.NewStore(p, refunds.NewStore(p, refunder, nil))
}

func TestRetiringTheLastDiscountedVariantIsRefused(t *testing.T) {
	ctx := t.Context()

	var sku string
	if err := pool.QueryRow(ctx, `
		WITH one AS (
			SELECT pv.id, pv.sku, pv.product_id FROM product_variants pv
			WHERE pv.is_active AND pv.compare_at_price_cents IS NOT NULL
			ORDER BY pv.position LIMIT 1
		)
		SELECT sku FROM one`).Scan(&sku); err != nil {
		t.Skipf("the seed has no discounted variant to test with: %v", err)
	}

	var productID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT product_id FROM product_variants WHERE sku = $1`, sku).Scan(&productID); err != nil {
		t.Fatalf("read product: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE product_variants SET compare_at_price_cents = NULL
		WHERE product_id = $1 AND sku <> $2`, productID, sku); err != nil {
		t.Fatalf("clear siblings: %v", err)
	}

	var campaignID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO sale_campaigns (slug, title, ends_at)
		VALUES ($1, '測試活動', now() + interval '7 days') RETURNING id`,
		"admin-test-"+uuid.NewString()[:8]).Scan(&campaignID); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO sale_campaign_products (campaign_id, product_id) VALUES ($1, $2)`,
		campaignID, productID); err != nil {
		t.Fatalf("feature product: %v", err)
	}
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM sale_campaigns WHERE id = $1`, campaignID)
	})

	if err := stock.NewStore(pool).SetActive(ctx, sku, false); !errors.Is(err, stock.ErrRefused) {
		t.Errorf("retiring the last discounted variant of a featured product gave %v, "+
			"want ErrRefused — the campaign would point at nothing marked down", err)
	}
}

// TestAdminCancelClawsBackLoyaltyPoints holds that a paid order the back office
// cancels — by refunding it before shipment — claws back its award lot,
// including when the lot was partly or wholly spent before cancellation.
func TestAdminCancelClawsBackLoyaltyPoints(t *testing.T) {
	ctx, _ := staffContext(t)
	s := refunds.NewStore(pool, admintest.Refunder{}, nil)

	t.Run("untouched lot", func(t *testing.T) {
		userID := cancelPointsCustomer(t)
		number, orderID := paidPickingOrderForUser(t, userID, 1200000)
		cancelPaidOrder(t, s, ctx, number)
		assertCancelClawback(t, orderID, -120, 120)
	})

	for _, tc := range []struct {
		name       string
		spent      int64
		wantPoints int64
	}{
		{"partly consumed", 100, -20},
		{"wholly consumed records a zero row", 120, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID := cancelPointsCustomer(t)
			number, orderID := paidPickingOrderForUser(t, userID, 1200000)
			if _, err := pool.Exec(ctx,
				`SELECT redeem_loyalty_points($1, $2, $3)`,
				userID, tc.spent, uuid.New()); err != nil {
				t.Fatalf("redeem: %v", err)
			}
			cancelPaidOrder(t, s, ctx, number)
			assertCancelClawback(t, orderID, tc.wantPoints, 120)
		})
	}

	t.Run("refuses before cancelled", func(t *testing.T) {
		userID := cancelPointsCustomer(t)
		_, pickingID := paidPickingOrderForUser(t, userID, 500000)
		var replay int64
		earlyErr := pool.QueryRow(ctx, `SELECT reverse_order_points($1)`, pickingID).Scan(&replay)
		if earlyErr == nil {
			t.Fatal("loyalty was reversed before the order was cancelled")
		}
		if admintest.ConstraintName(earlyErr) != "loyalty_clawback_cancelled_order" {
			t.Fatalf("refused by %q, want loyalty_clawback_cancelled_order: %v", admintest.ConstraintName(earlyErr), earlyErr)
		}
	})
}

func cancelPointsCustomer(t *testing.T) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('cancel-points-' || gen_random_uuid() || '@goen.invalid', 'customer', '取消點數')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return userID
}

func cancelPaidOrder(t *testing.T, s *refunds.Store, ctx context.Context, number string) {
	t.Helper()
	if _, err := s.RefundBeforeShipment(ctx, number, "顧客取消"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

func assertCancelClawback(t *testing.T, orderID uuid.UUID, wantPoints, wantRequested int64) {
	t.Helper()
	ctx := t.Context()
	var points, requested int64
	var key string
	var returnID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT e.points, e.requested_points, e.idempotency_key, r.id
		FROM loyalty_entries e
		JOIN return_requests r ON r.order_id = e.order_id AND r.before_shipment
		WHERE e.order_id = $1 AND e.kind = 'clawback'`, orderID).Scan(&points, &requested, &key, &returnID); err != nil {
		t.Fatalf("read clawback: %v", err)
	}
	if points != wantPoints || requested != wantRequested {
		t.Errorf("clawback points/requested = %d/%d, want %d/%d",
			points, requested, wantPoints, wantRequested)
	}
	if want := "return:" + returnID.String(); key != want {
		t.Errorf("clawback key = %q, want %q", key, want)
	}
	var rows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM loyalty_entries
		WHERE order_id = $1 AND kind = 'clawback'`, orderID).Scan(&rows); err != nil {
		t.Fatalf("count clawbacks: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d cancel clawbacks, want 1", rows)
	}
	var replay int64
	if err := pool.QueryRow(ctx,
		`SELECT reverse_return_points($1)`, returnID).Scan(&replay); err != nil || replay != 0 {
		t.Fatalf("cancel clawback replay = %d, %v; want 0, nil", replay, err)
	}
}

func paidPickingOrderForUser(t *testing.T, userID uuid.UUID, cents int64) (number string, orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

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
		VALUES ($1, 'CANCEL-POINTS', '點數取消', $2, 1)`, orderID, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'cancel-points@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`,
		orderID, "cs_cancel_pts_"+number, cents); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`,
		"cs_cancel_pts_"+number, cents); err != nil {
		t.Fatalf("capture: %v", err)
	}
	var awarded int64
	if err := tx.QueryRow(ctx, `SELECT award_loyalty_points($1)`, orderID).Scan(&awarded); err != nil {
		t.Fatalf("award: %v", err)
	}
	if awarded != cents/10000 {
		t.Fatalf("awarded = %d, want %d", awarded, cents/10000)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("to picking: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, orderID
}

// TestPointClawbackAndErasureShareOrderBeforeAccount forces the former ABBA
// window. The clawback pauses at its ledger insert; erasure has already begun.
// Both must finish in order, with neither PostgreSQL transaction chosen as a
// deadlock victim.
func TestPointClawbackAndErasureShareOrderBeforeAccount(t *testing.T) {
	ctx := t.Context()
	requestID, orderID, userID := admintest.LoyaltyReturn(t, pool, []int64{1200000}, 0)
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests
		SET status = 'approved', decided_at = now(), resolution = 'lock order'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve lock-order return: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, reason,
		                     return_request_id, status, provider_ref, succeeded_at)
		SELECT p.id, 'return:' || ($1::uuid)::text, 1200000, 'lock order', $1::uuid,
		       'succeeded', 're_lock_order_' || ($1::uuid)::text, now()
		FROM payments p
		WHERE p.order_id = $2 AND p.status = 'succeeded'`, requestID, orderID); err != nil {
		t.Fatalf("settle lock-order return: %v", err)
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_pause_return_clawback_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_pause_return_clawback_" + suffix}.Sanitize()
	const barrierKey int64 = 8_812_233_445_566_781
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF NEW.return_request_id = '%s'::uuid THEN
				PERFORM pg_advisory_xact_lock(%d);
			END IF;
			RETURN NEW;
		END
		$body$;
		CREATE TRIGGER %s BEFORE INSERT ON loyalty_entries
		FOR EACH ROW EXECUTE FUNCTION %s()`,
		functionName, requestID, barrierKey, triggerName, functionName)); err != nil {
		t.Fatalf("install clawback barrier: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON loyalty_entries; DROP FUNCTION IF EXISTS %s()",
			triggerName, functionName))
	})

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin clawback barrier: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, lockErr := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, barrierKey); lockErr != nil {
		t.Fatalf("hold clawback barrier: %v", lockErr)
	}
	reverseConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire clawback connection: %v", err)
	}
	defer reverseConn.Release()
	eraseConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire erasure connection: %v", err)
	}
	defer eraseConn.Release()
	var reversePID, erasePID int
	if err := reverseConn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&reversePID); err != nil {
		t.Fatalf("read clawback backend: %v", err)
	}
	if err := eraseConn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&erasePID); err != nil {
		t.Fatalf("read erasure backend: %v", err)
	}

	type reverseResult struct {
		points int64
		err    error
	}
	reverseResultCh := make(chan reverseResult, 1)
	reverseDone := make(chan struct{})
	go func() {
		defer close(reverseDone)
		var points int64
		err := reverseConn.QueryRow(context.WithoutCancel(ctx),
			`SELECT reverse_return_points($1)`, requestID).Scan(&points)
		reverseResultCh <- reverseResult{points: points, err: err}
	}()
	waitForBackendLock(t, reversePID, reverseDone)

	eraseResultCh := make(chan error, 1)
	eraseDone := make(chan struct{})
	go func() {
		defer close(eraseDone)
		_, err := eraseConn.Exec(context.WithoutCancel(ctx), `SELECT erase_user($1)`, userID)
		eraseResultCh <- err
	}()
	waitForBackendLock(t, erasePID, eraseDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release clawback barrier: %v", err)
	}
	select {
	case result := <-reverseResultCh:
		if result.err != nil || result.points != 120 {
			t.Fatalf("concurrent clawback = %d, %v; want 120 and no deadlock", result.points, result.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("clawback did not finish after its barrier was released")
	}
	select {
	case err := <-eraseResultCh:
		if err != nil {
			t.Fatalf("concurrent erasure: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("erasure did not finish after clawback committed")
	}

	var users, clawbacks int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM users WHERE id = $1),
		       (SELECT count(*) FROM loyalty_entries
		        WHERE return_request_id = $2 AND kind = 'clawback')`, userID, requestID).
		Scan(&users, &clawbacks); err != nil {
		t.Fatalf("read lock-order result: %v", err)
	}
	if users != 0 || clawbacks != 1 {
		t.Errorf("lock-order survivors users/clawbacks = %d/%d, want 0/1", users, clawbacks)
	}
}

func waitForBackendLock(t *testing.T, pid int, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatalf("backend %d finished before reaching the forced lock boundary", pid)
		default:
		}
		var waiting bool
		if err := pool.QueryRow(t.Context(), `
			SELECT coalesce(wait_event_type = 'Lock', false)
			FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&waiting); err != nil {
			t.Fatalf("observe backend %d: %v", pid, err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("backend %d never reached a lock wait", pid)
}

// TestAProviderRefusalGetsANewDurableAttempt holds the distinction between
// retrying ambiguity and retrying a known terminal outcome. Ambiguity reuses one
// provider key; failed/cancelled is immutable evidence and gets a linked next
// generation with a fresh DB-derived key.
func TestAProviderRefusalGetsANewDurableAttempt(t *testing.T) {
	ctx, _ := staffContext(t)
	s := returnDeskOver(pool, admintest.Refunder{State: refundstate.Failed})
	requestID, _ := admintest.ReturnedOrder(t, pool, 1)

	if err := s.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err == nil {
		t.Fatal("a refund Stripe refused was reported as success")
	}

	var returnStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
		t.Fatalf("read return: %v", err)
	}
	if returnStatus != "approved" {
		t.Errorf("return is %q, want approved — the decision is taken before any "+
			"money moves, which is what stops two staff members both paying",
			returnStatus)
	}

	// On record, so nothing is lost while it is outstanding.
	var refundStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM refunds WHERE return_request_id = $1`, requestID).Scan(&refundStatus); err != nil {
		t.Fatalf("no refund row survives a refusal, so nothing can reconcile it: %v", err)
	}
	if refundStatus != "failed" {
		t.Errorf("refund is %q, want failed — Stripe was asked and said no", refundStatus)
	}

	healthy := returnDeskOver(pool, admintest.Refunder{})
	if err := healthy.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("retry after a known provider refusal: %v", err)
	}

	type attempt struct {
		id       uuid.UUID
		previous uuid.NullUUID
		number   int32
		key      string
		status   string
	}
	rows, err := pool.Query(ctx, `
		SELECT id, previous_refund_id, attempt_no, request_key, status
		FROM refunds WHERE return_request_id = $1 ORDER BY attempt_no`, requestID)
	if err != nil {
		t.Fatalf("read provider attempts: %v", err)
	}
	defer rows.Close()
	var attempts []attempt
	for rows.Next() {
		var got attempt
		if scanErr := rows.Scan(&got.id, &got.previous, &got.number, &got.key, &got.status); scanErr != nil {
			t.Fatalf("scan provider attempt: %v", scanErr)
		}
		attempts = append(attempts, got)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("iterate provider attempts: %v", rowsErr)
	}
	base := "return:" + requestID.String()
	if len(attempts) != 2 {
		t.Fatalf("provider attempts = %#v, want failed evidence plus one successor", attempts)
	}
	if attempts[0].number != 1 || attempts[0].key != base || attempts[0].status != "failed" ||
		attempts[0].previous.Valid {
		t.Errorf("first provider attempt = %#v, want immutable failed generation 1", attempts[0])
	}
	if attempts[1].number != 2 || attempts[1].key != base+":attempt:2" ||
		attempts[1].status != "succeeded" || !attempts[1].previous.Valid ||
		attempts[1].previous.UUID != attempts[0].id {
		t.Errorf("second provider attempt = %#v, want succeeded generation 2 linked to %s",
			attempts[1], attempts[0].id)
	}
	page, err := health.NewStore(pool).WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("read health after successful successor: %v", err)
	}
	for i := range page.OpenRefunds {
		if strings.HasPrefix(page.OpenRefunds[i].Key, base) {
			t.Errorf("historical failed attempt still appears as current health work: %#v",
				page.OpenRefunds[i])
		}
	}
}

func TestTheHealthPageNamesARefundThatDidNotLand(t *testing.T) {
	ctx, _ := staffContext(t)
	s := returnDeskOver(pool, admintest.Refunder{
		RefundErr: errors.New("read tcp 1.2.3.4:443: i/o timeout"),
	})

	requestID, orderNumber := admintest.ReturnedOrder(t, pool, 1)

	if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err == nil {
		t.Fatal("a refund that timed out was reported as success")
	}

	view, err := health.NewStore(pool).WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if view.RefundsHealthy() {
		t.Error("a refund that never left reads as healthy")
	}

	// Found by identity, never by position: the suite is shuffled and other tests leave refunds behind.
	var found *adminpages.OpenRefund
	for i := range view.OpenRefunds {
		if view.OpenRefunds[i].Key == "return:"+requestID.String() {
			found = &view.OpenRefunds[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("the stalled refund for return %s is on no page — the row exists "+
			"only for reconciliation and nothing can read it", requestID)
	}
	want := adminpages.OpenRefund{
		OrderNumber: orderNumber,
		Key:         "return:" + requestID.String(),
		Status:      "pending",
		AmountCents: 100000,
		ProviderRef: "",
		Since:       found.Since,
	}
	if diff := cmp.Diff(want, *found); diff != "" {
		t.Errorf("WorkerHealth() open refund mismatch (-want +got):\n%s", diff)
	}
}

func TestRefundHealthCountExceedsItsBoundedDiagnosticSample(t *testing.T) {
	ctx, _ := staffContext(t)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))
	before, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("read refund count before sample crowd: %v", err)
	}
	_, orderNumber := admintest.ReturnedOrder(t, pool, 1)
	prefix := "health-count-" + uuid.NewString() + "-"
	if _, insertErr := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents)
		SELECT p.id, $2 || g::text, 1
		FROM payments p
		JOIN orders o ON o.id = p.order_id
		CROSS JOIN generate_series(1, 25) g
		WHERE o.order_number = $1 AND p.status = 'succeeded'`, orderNumber, prefix); insertErr != nil {
		t.Fatalf("create open-refund sample crowd: %v", insertErr)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, cleanupErr := pool.Exec(cleanupCtx,
			`DELETE FROM refunds WHERE request_key LIKE $1 || '%'`, prefix); cleanupErr != nil {
			t.Errorf("remove open-refund sample crowd: %v", cleanupErr)
		}
	})

	after, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("read refund count after sample crowd: %v", err)
	}
	if after.OpenRefundCount != before.OpenRefundCount+25 {
		t.Errorf("exact open refund count moved %d -> %d, want +25",
			before.OpenRefundCount, after.OpenRefundCount)
	}
	if len(after.OpenRefunds) != health.OpenRefundListLimit {
		t.Errorf("diagnostic sample has %d rows, want bounded %d",
			len(after.OpenRefunds), health.OpenRefundListLimit)
	}
	enCtx := i18n.WithLocale(ctx, i18n.En)
	wantText := fmt.Sprintf(i18n.T(enCtx, i18n.KeyHealthRefundsStuck), after.OpenRefundCount)
	if got := after.RefundsText(enCtx); got != wantText {
		t.Errorf("refund health text = %q, want exact count %q", got, wantText)
	}
}

func TestTheBackOfficeIsInvisibleToEveryoneButStaff(t *testing.T) {
	ctx := t.Context()

	var customerID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role) VALUES ('shopper-'||gen_random_uuid()||'@example.com', 'customer')
		RETURNING id`).Scan(&customerID); err != nil {
		t.Fatalf("create customer: %v", err)
	}

	guarded := backOffice.RequireStaff(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("the back office"))
	})

	tests := []struct {
		name     string
		signedIn bool
		user     account.User
	}{
		{name: "signed out"},
		{name: "a signed-in customer", signedIn: true,
			user: account.User{ID: customerID.String(), Role: "customer"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin", nil)
			if tt.signedIn {
				req = req.WithContext(account.WithUser(req.Context(), tt.user))
			}
			w := httptest.NewRecorder()
			guarded(w, req)

			if w.Code != http.StatusNotFound {
				t.Errorf("status is %d, want 404 — anything else says /admin is a "+
					"real place", w.Code)
			}
			if loc := w.Header().Get("Location"); loc != "" {
				t.Errorf("redirected to %q; a redirect confirms the page exists and "+
					"puts its path in the visitor's history", loc)
			}
			if strings.Contains(w.Body.String(), "the back office") {
				t.Error("the handler ran")
			}
		})
	}

	// BOTH back-office roles, and 'staff' is the one that matters: a colleague
	// hired as staff must not meet a 404 on the whole back office.
	staffView, err := roster.NewStore(pool).Staff(ctx)
	if err != nil {
		t.Fatalf("read roles offered by /admin/staff: %v", err)
	}
	for _, offered := range staffView.Roles {
		role := string(offered)
		t.Run(role+" reaches the back office", func(t *testing.T) {
			var id uuid.UUID
			if err := pool.QueryRow(ctx, `
				INSERT INTO users (email, role) VALUES ('bo-'||gen_random_uuid()||'@example.com', $1)
				RETURNING id`, role).Scan(&id); err != nil {
				t.Fatalf("create %s: %v", role, err)
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin", nil)
			req = req.WithContext(account.WithUser(req.Context(),
				account.User{ID: id.String(), Role: account.Role(role)}))
			w := httptest.NewRecorder()
			guarded(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("%s got %d, want 200 — /admin/staff offers this role, so a "+
					"colleague hired into it can do no work at all", role, w.Code)
			}
		})
	}
}

func TestOnlyAnAdminReachesTheStaffPage(t *testing.T) {
	ctx := t.Context()

	guarded := backOffice.RequireAdmin(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("who works here"))
	})

	newUser := func(role string) account.User {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (email, role)
			VALUES ('`+role+`-'||gen_random_uuid()||'@example.com', $1)
			RETURNING id`, role).Scan(&id); err != nil {
			t.Fatalf("create %s: %v", role, err)
		}
		return account.User{ID: id.String(), Role: account.Role(role)}
	}

	for _, tt := range []struct {
		name     string
		signedIn bool
		user     account.User
		want     int
	}{
		{name: "signed out", want: http.StatusNotFound},
		{name: "a customer", signedIn: true, user: newUser("customer"), want: http.StatusNotFound},
		{name: "a staff member", signedIn: true, user: newUser("staff"), want: http.StatusNotFound},
		{name: "an admin", signedIn: true, user: newUser("admin"), want: http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/staff", nil)
			if tt.signedIn {
				req = req.WithContext(account.WithUser(req.Context(), tt.user))
			}
			w := httptest.NewRecorder()
			guarded(w, req)

			if w.Code != tt.want {
				t.Errorf("status is %d, want %d", w.Code, tt.want)
			}
			ran := strings.Contains(w.Body.String(), "who works here")
			if want := tt.want == http.StatusOK; ran != want {
				t.Errorf("the handler ran = %v, want %v", ran, want)
			}
		})
	}
}

func staffContext(t *testing.T) (context.Context, uuid.UUID) {
	t.Helper()
	return staffContextOn(t, pool)
}

func staffContextOn(t *testing.T, p *pgxpool.Pool) (context.Context, uuid.UUID) {
	t.Helper()
	return admintest.StaffContext(t, p)
}

func auditRows(t *testing.T, action audit.Action) int {
	t.Helper()
	return admintest.AuditRows(t, pool, action)
}

func TestEveryBackOfficeWriteLeavesATrail(t *testing.T) {
	ctx, actor := staffContext(t)
	s := products.NewStore(pool)

	sku := anyVariantSKU(t)
	slug := admintest.AnyProductSlug(t, pool)

	tests := []struct {
		name   string
		action audit.Action
		run    func() error
	}{
		{"adjust stock", audit.ActionAdjustStock, func() error {
			return stock.NewStore(pool).Adjust(ctx, sku, 3, actor.String(), uuid.NewString())
		}},
		{"reprice", audit.ActionRepriceVariant, func() error {
			return stock.NewStore(pool).SetPrice(ctx, sku, 123400, 0)
		}},
		{"publish", audit.ActionPublishProduct, func() error {
			return s.SetStatus(ctx, slug, "draft")
		}},
		{"grant credit", audit.ActionGrantCredit, func() error {
			_, grantErr := loyalty.NewStore(pool).GrantCredit(ctx, actor, 500,
				"測試", uuid.New())
			return grantErr
		}},
		{"create campaign", audit.ActionCreateCampaign, func() error {
			_, err := campaigns.NewStore(pool).Create(ctx, &campaigns.Form{
				Slug: "trail-" + uuid.NewString()[:8], Title: "紀錄", Days: 7,
			})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := auditRows(t, tt.action)
			if err := tt.run(); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if after := auditRows(t, tt.action); after != before+1 {
				t.Errorf("%s left %d audit rows, want one more than %d — the action "+
					"happened and nobody can say who did it", tt.name, after, before)
			}
		})
	}
}

func TestTheTrailCannotBeRewritten(t *testing.T) {
	ctx, _ := staffContext(t)
	if err := products.NewStore(pool).
		SetStatus(ctx, admintest.AnyProductSlug(t, pool), "draft"); err != nil {
		t.Fatalf("seed a row: %v", err)
	}

	tests := []struct {
		name string
		stmt string
	}{
		{"update", `UPDATE audit_events SET action = 'rewritten'`},
		{"delete", `DELETE FROM audit_events`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tt.stmt); err == nil {
				t.Fatalf("%s succeeded against an append-only table", tt.name)
			} else if name := admintest.ConstraintName(err); name != "audit_events_append_only" {
				t.Errorf("refused by %q, want audit_events_append_only: %v", name, err)
			}
		})
	}

	if err := admintest.AsAdmin(ctx, t, pool, `INSERT INTO audit_events (action, entity_table)
		VALUES ('forged', 'x')`); err == nil {
		t.Error("admin inserted an audit row directly, bypassing record_audit_event")
	}

	if err := admintest.AsAdmin(ctx, t, pool, `SELECT record_audit_event(
		(SELECT id FROM users LIMIT 1), 'test.control', 'products', NULL)`); err != nil {
		t.Errorf("admin cannot call record_audit_event: %v", err)
	}
}

func anyVariantSKU(t *testing.T) string {
	t.Helper()
	var sku string
	if err := pool.QueryRow(t.Context(),
		`SELECT v.sku FROM product_variants v
		   WHERE NOT EXISTS (SELECT 1 FROM sale_campaign_products cp WHERE cp.product_id = v.product_id)
		   ORDER BY v.sku LIMIT 1`).Scan(&sku); err != nil {
		t.Fatalf("find variant: %v", err)
	}
	return sku
}

// TestAnAcknowledgedPaymentLeavesTheAlarm is the other half of flagging one.
// The refund is at Stripe and nothing here can see it land, so the alarm has an
// off switch or /admin/health is unhealthy forever after the first arrival —
// and an alarm that is always on is one nobody reads.
func TestAnAcknowledgedPaymentLeavesTheAlarm(t *testing.T) {
	ctx, _ := staffContext(t)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))
	baseline, healthErr := health.NewStore(pool).WorkerHealth(ctx, worker)
	if healthErr != nil {
		t.Fatalf("health before fixture: %v", healthErr)
	}

	number := admintest.PlaceUnpaidOrder(t, pool)
	var orderID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE order_number = $1`, number).
		Scan(&orderID); err != nil {
		t.Fatalf("read order: %v", err)
	}
	sessionID := "cs_ack_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents)
		VALUES ($1, $2, 'requires_payment', 500000)`, orderID, sessionID); err != nil {
		t.Fatalf("open payment: %v", err)
	}

	eventID := "evt_ack_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events
			(provider, event_id, type, object_ref, payload, unreconciled)
		VALUES ('stripe', $1, 'checkout.session.completed', $2, '{}'::jsonb,
		        'refused_capture: operator must refund or post the verified money')`,
		eventID, sessionID); err != nil {
		t.Fatalf("flag the event: %v", err)
	}

	flagged, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if flagged.UnreconciledPayments != baseline.UnreconciledPayments+1 {
		t.Fatalf("flagging this event changed the alarm count from %d to %d, want one more",
			baseline.UnreconciledPayments, flagged.UnreconciledPayments)
	}
	var eventFlagged bool
	for _, event := range flagged.UnreconciledEvents {
		if event.EventID == eventID {
			eventFlagged = true
			break
		}
	}
	if !eventFlagged {
		t.Fatalf("payment alarm does not name flagged event %q", eventID)
	}

	// Merely naming the event would cancel the linked attempt and allow another
	// checkout without saying what happened to the provider money, so the HTTP
	// boundary must refuse a form that omits the safe-release conclusion.
	form := url.Values{"event": {eventID}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/health/reconcile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	healthHandler(pool).Reconcile(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/health?notflagged=1" {
		t.Fatalf("event-only HTTP resolution = %d %q, want refusal redirect",
			res.Code, res.Header().Get("Location"))
	}
	var stillOpen, stillFlagged bool
	if err := pool.QueryRow(ctx, `
		SELECT p.status = 'requires_payment', e.reconciled_at IS NULL
		FROM payments p JOIN payment_webhook_events e
		  ON e.provider = p.provider AND e.object_ref = p.provider_ref
		WHERE e.event_id = $1`, eventID).Scan(&stillOpen, &stillFlagged); err != nil {
		t.Fatalf("read refused event-only resolution: %v", err)
	}
	if !stillOpen || !stillFlagged {
		t.Fatalf("event-only resolution changed payment/event = open %v, flagged %v",
			stillOpen, stillFlagged)
	}

	beforeAudit := auditRows(t, audit.ActionReconcilePayment)
	if err := health.NewStore(pool).ReleasePaymentEventAfterRefundOrAccounting(ctx, eventID); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var paymentStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM payments WHERE provider_ref = $1`, sessionID).
		Scan(&paymentStatus); err != nil {
		t.Fatalf("read reconciled payment: %v", err)
	}
	if paymentStatus != "cancelled" {
		t.Errorf("linked payment status = %q, want cancelled so a completed session cannot be resumed",
			paymentStatus)
	}

	settled, settledErr := health.NewStore(pool).WorkerHealth(ctx, worker)
	if settledErr != nil {
		t.Fatalf("health: %v", settledErr)
	}
	if settled.UnreconciledPayments != baseline.UnreconciledPayments {
		t.Errorf("reconciling this event left the alarm count at %d, want baseline %d",
			settled.UnreconciledPayments, baseline.UnreconciledPayments)
	}
	for _, event := range settled.UnreconciledEvents {
		if event.EventID == eventID {
			t.Errorf("acknowledged event %q remains on the payment alarm", eventID)
		}
	}
	if afterAudit := auditRows(t, audit.ActionReconcilePayment); afterAudit != beforeAudit+1 {
		t.Errorf("saying the money went back by hand added %d audit rows, want 1",
			afterAudit-beforeAudit)
	}

	// A second press changes nothing: the row count is where the question is
	// asked, so there is no read-then-write for two staff members to both pass.
	if err := health.NewStore(pool).ReleasePaymentEventAfterRefundOrAccounting(
		ctx, eventID,
	); !errors.Is(err, health.ErrNotFound) {
		t.Errorf("acknowledging it twice = %v, want ErrNotFound", err)
	}
	// And an event nobody flagged is not acknowledgeable at all.
	unflagged := "evt_plain_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events (provider, event_id, type, payload)
		VALUES ('stripe', $1, 'payment_intent.processing', '{}'::jsonb)`, unflagged); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := health.NewStore(pool).ReleasePaymentEventAfterRefundOrAccounting(
		ctx, unflagged,
	); !errors.Is(err, health.ErrNotFound) {
		t.Errorf("acknowledging an event that was never flagged = %v, want ErrNotFound", err)
	}
}

// TestACompletePaymentWithoutAFlaggedEventHasAResolutionDoor covers provider
// completion before a capture webhook and the understood complete/unpaid event:
// neither has an unreconciled event row, so the payment state itself must make
// health unhealthy and give staff an audited, typed way to resolve it.
func TestACompletePaymentWithoutAFlaggedEventHasAResolutionDoor(t *testing.T) {
	ctx, _ := staffContext(t)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))

	number := admintest.PlaceUnpaidOrder(t, pool)
	var orderID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE order_number = $1`, number).
		Scan(&orderID); err != nil {
		t.Fatalf("read order: %v", err)
	}
	providerRef := "cs_complete_health_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents)
		VALUES ($1, $2, 'requires_reconciliation', 500000)`, orderID, providerRef); err != nil {
		t.Fatalf("record complete payment: %v", err)
	}
	// This is the complete+unpaid shape: the webhook was understood and clean,
	// so it cannot be resolved through release_payment_event.
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events
			(provider, event_id, type, object_ref, payload, processed_at)
		VALUES ('stripe', $1, 'checkout.session.completed', $2, '{}'::jsonb, now())`,
		"evt_complete_unpaid_"+uuid.NewString()[:12], providerRef); err != nil {
		t.Fatalf("record understood complete event: %v", err)
	}

	flagged, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("health before resolution: %v", err)
	}
	if flagged.PaymentsReconciled() {
		t.Fatal("requires_reconciliation payment with no flagged event reads healthy")
	}
	found := false
	for _, issue := range flagged.UnreconciledCompletePayments {
		if issue.ProviderRef == providerRef {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("health did not name complete provider reference %q", providerRef)
	}

	beforeAudit := auditRows(t, audit.ActionReconcilePayment)
	if reconcileErr := health.NewStore(pool).ReconcileCompletePayment(ctx, providerRef,
		health.CompletePaymentUnpaidOrRefunded); reconcileErr != nil {
		t.Fatalf("reconcile complete payment: %v", reconcileErr)
	}
	var status string
	if queryErr := pool.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, providerRef).Scan(&status); queryErr != nil {
		t.Fatalf("read resolved payment: %v", queryErr)
	}
	if status != "reconciled" {
		t.Errorf("resolved complete payment status = %q, want reconciled", status)
	}

	settled, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("health after resolution: %v", err)
	}
	for _, issue := range settled.UnreconciledCompletePayments {
		if issue.ProviderRef == providerRef {
			t.Errorf("resolved provider reference %q remains on health", providerRef)
		}
	}
	if got := auditRows(t, audit.ActionReconcilePayment); got != beforeAudit+1 {
		t.Errorf("reconciling complete payment added %d audit rows, want 1", got-beforeAudit)
	}
	if err := health.NewStore(pool).ReconcileCompletePayment(ctx, providerRef,
		health.CompletePaymentUnpaidOrRefunded); !errors.Is(err, health.ErrNotFound) {
		t.Errorf("reconciling complete payment twice = %v, want ErrNotFound", err)
	}
}

// TestReleasedStockPaidAttributionReturnsARefundInstruction proves the admin
// boundary does not turn the capture fence into a generic 500. Health removes
// the impossible paid action, and a stale form submitted across a concurrent
// sweep returns an actionable refund notice while leaving reconciliation open.
func TestReleasedStockPaidAttributionReturnsARefundInstruction(t *testing.T) {
	ctx, _ := staffContext(t)
	number, orderID, _ := admintest.PendingOrderHoldingStock(t, pool)
	providerRef := "cs_admin_released_stock_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx,
		`SELECT open_payment($1, $2, 100000)`, orderID, providerRef); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	var reservationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		UPDATE inventory_reservations
		SET created_at = now() - interval '2 hours',
		    expires_at = now() - interval '1 hour'
		WHERE order_id = $1 AND state = 'held'
		RETURNING id`, orderID).Scan(&reservationID); err != nil {
		t.Fatalf("expire hold: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT release_reservation($1)`, reservationID); err != nil {
		t.Fatalf("release hold before recovery: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT record_complete_payment($1, $2, 100000)`, orderID, providerRef); err != nil {
		t.Fatalf("record delayed complete session: %v", err)
	}

	view, err := health.NewStore(pool).WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	refundOnly := false
	for _, issue := range view.UnreconciledCompletePayments {
		if issue.ProviderRef == providerRef && issue.OrderNumber == number &&
			!issue.PaidAttributionAllowed {
			refundOnly = true
		}
	}
	if !refundOnly {
		t.Fatal("released-stock complete payment still offered paid attribution")
	}

	form := url.Values{
		"payment":    {providerRef},
		"resolution": {"paid"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/health/reconcile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	healthHandler(pool).Reconcile(res, req)
	if res.Code != http.StatusSeeOther ||
		res.Header().Get("Location") != "/admin/health?mustrefund=1" {
		t.Fatalf("stale paid form = %d %q, want actionable refund redirect",
			res.Code, res.Header().Get("Location"))
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, providerRef).Scan(&status); err != nil {
		t.Fatalf("read refused payment: %v", err)
	}
	if status != "requires_reconciliation" {
		t.Fatalf("refused paid form changed payment to %q, want requires_reconciliation", status)
	}
}

const (
	invoiceFAQQuestion = "發票怎麼開立？"
	invoiceFAQZh       = "結帳時可以選擇會員載具、手機條碼載具或公司統編，付款完成時系統會依你的選擇自動開立電子發票。這份部署若尚未設定綠界加值中心則不會開立，後台會說明原因。"
	invoiceFAQEn       = "At checkout you can choose a member carrier, a mobile barcode carrier, or a company tax ID, and the electronic invoice is issued automatically against that choice when your payment completes. Without ECPay credentials this deployment files nothing, and the back office says so."
	staleInvoiceFAQZh  = "結帳時可以選擇會員載具、手機條碼載具或公司統編,系統會記錄您的選擇。電子發票的實際開立需要串接加值中心,這部分尚未完成。"
	staleInvoiceFAQEn  = "At checkout you can choose a member carrier, a mobile barcode carrier, or a company tax ID, and we record your choice. Actually issuing the electronic invoice needs an integration with a certified provider, which is not built yet."
)

// TestSeededInvoiceFAQMatchesTheWiredIssuer holds the published invoice FAQ
// to Gateway.Issue: a fresh seed, the shipped repair file on a kept stale
// row, and the back-office form all say the same deployment-gated thing,
// in both locales.
func TestSeededInvoiceFAQMatchesTheWiredIssuer(t *testing.T) {
	ctx, _ := staffContext(t)
	siteStore := site.NewStore(pool)

	if _, err := invoice.NewGateway("", "", "", ""); err != nil {
		t.Fatalf("an empty configuration must be legal: %v", err)
	}

	assertInvoiceFAQLocales(t, siteStore, ctx, invoiceFAQZh, invoiceFAQEn)
	assertStatutoryReturnFAQUntouched(t, siteStore, ctx)

	catalog, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatalf("read catalogue seed: %v", err)
	}
	repair, err := os.ReadFile("../../seed/repair_invoice_faq.sql")
	if err != nil {
		t.Fatalf("read shipped invoice FAQ repair: %v", err)
	}

	isolated := dbtest.Pool(t)
	if _, loadErr := isolated.Exec(ctx, string(catalog)); loadErr != nil {
		t.Fatalf("seed isolated catalogue: %v", loadErr)
	}
	plantStaleInvoiceFAQ(t, ctx, isolated)
	isolatedContent := site.NewStore(isolated)
	zh, en := invoiceFAQAnswers(t, isolatedContent, ctx)
	if !strings.Contains(zh, "尚未完成") || !strings.Contains(en, "not built yet") {
		t.Fatalf("the planted stale answers did not reach /faq: zh=%q en=%q", zh, en)
	}
	conn, err := isolated.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire isolated connection: %v", err)
	}
	if _, seedErr := conn.Exec(ctx, string(catalog)); seedErr == nil {
		conn.Release()
		t.Fatal("the catalogue seed succeeded against a kept database; " +
			"db-seed cannot be the repair path")
	}
	if _, rollErr := conn.Exec(ctx, "ROLLBACK"); rollErr != nil {
		conn.Release()
		t.Fatalf("rollback failed catalogue re-seed: %v", rollErr)
	}
	conn.Release()
	zh, en = invoiceFAQAnswers(t, isolatedContent, ctx)
	if !strings.Contains(zh, "尚未完成") || !strings.Contains(en, "not built yet") {
		t.Fatal("the failed catalogue re-seed changed the kept invoice FAQ")
	}
	if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
		t.Fatalf("apply shipped invoice FAQ repair: %v", repairErr)
	}
	assertInvoiceFAQLocales(t, isolatedContent, ctx, invoiceFAQZh, invoiceFAQEn)
	assertStatutoryReturnFAQUntouched(t, isolatedContent, ctx)

	t.Cleanup(func() {
		if _, restoreErr := pool.Exec(context.WithoutCancel(ctx), string(repair)); restoreErr != nil {
			t.Errorf("restore invoice FAQ: %v", restoreErr)
		}
	})
	plantStaleInvoiceFAQ(t, ctx, pool)
	var entry adminpages.FAQEntry
	view, err := contentdesk.NewStore(pool).FAQ(ctx)
	if err != nil {
		t.Fatalf("FAQ: %v", err)
	}
	for i := range view.Rows {
		if view.Rows[i].Question == invoiceFAQQuestion {
			entry = view.Rows[i]
			break
		}
	}
	if entry.ID == "" {
		t.Fatal("the invoice FAQ row is not in the back office")
	}
	if errs, updErr := contentdesk.NewStore(pool).UpdateFAQEntry(ctx, &contentdesk.FAQForm{
		ID: entry.ID, Category: entry.Category,
		Question: invoiceFAQQuestion, Answer: invoiceFAQZh,
		CategoryEn: entry.CategoryEn, QuestionEn: entry.QuestionEn,
		AnswerEn: invoiceFAQEn,
	}); updErr != nil || len(errs) > 0 {
		t.Fatalf("UpdateFAQEntry: %v %v", updErr, errs)
	}
	assertInvoiceFAQLocales(t, siteStore, ctx, invoiceFAQZh, invoiceFAQEn)
	assertStatutoryReturnFAQUntouched(t, siteStore, ctx)
}

// TestRepairInvoiceFAQPreservesEditedLocales runs the shipped repair file against
// kept rows and rewrites only a locale that still exactly matches the stale copy.
func TestRepairInvoiceFAQPreservesEditedLocales(t *testing.T) {
	ctx := t.Context()
	catalog, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatalf("read catalogue seed: %v", err)
	}
	repair, err := os.ReadFile("../../seed/repair_invoice_faq.sql")
	if err != nil {
		t.Fatalf("read shipped invoice FAQ repair: %v", err)
	}

	cases := []struct {
		name           string
		zh, en         string
		wantZh, wantEn string
	}{
		{
			name: "both stale",
			zh:   staleInvoiceFAQZh, en: staleInvoiceFAQEn,
			wantZh: invoiceFAQZh, wantEn: invoiceFAQEn,
		},
		{
			name: "custom Chinese stale English",
			zh:   "店家自訂發票說明", en: staleInvoiceFAQEn,
			wantZh: "店家自訂發票說明", wantEn: invoiceFAQEn,
		},
		{
			name: "stale Chinese custom English",
			zh:   staleInvoiceFAQZh, en: "Merchant invoice instructions",
			wantZh: invoiceFAQZh, wantEn: "Merchant invoice instructions",
		},
		{
			name: "both custom",
			zh:   "店家自訂發票說明", en: "Merchant invoice instructions",
			wantZh: "店家自訂發票說明", wantEn: "Merchant invoice instructions",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolated := dbtest.Pool(t)
			if _, loadErr := isolated.Exec(ctx, string(catalog)); loadErr != nil {
				t.Fatalf("seed catalogue: %v", loadErr)
			}
			if _, plantErr := isolated.Exec(ctx, `
				UPDATE faq_entries SET answer = $1, answer_en = $2
				WHERE question = $3`, tc.zh, tc.en, invoiceFAQQuestion); plantErr != nil {
				t.Fatalf("plant invoice FAQ: %v", plantErr)
			}
			if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
				t.Fatalf("apply shipped invoice FAQ repair: %v", repairErr)
			}
			content := site.NewStore(isolated)
			gotZh, gotEn := invoiceFAQAnswers(t, content, ctx)
			if gotZh != tc.wantZh {
				t.Errorf("Chinese = %q, want %q", gotZh, tc.wantZh)
			}
			if gotEn != tc.wantEn {
				t.Errorf("English = %q, want %q", gotEn, tc.wantEn)
			}
			assertStatutoryReturnFAQUntouched(t, content, ctx)
		})
	}

	t.Run("absent row", func(t *testing.T) {
		isolated := dbtest.Pool(t)
		if _, loadErr := isolated.Exec(ctx, string(catalog)); loadErr != nil {
			t.Fatalf("seed catalogue: %v", loadErr)
		}
		if _, delErr := isolated.Exec(ctx,
			`DELETE FROM faq_entries WHERE question = $1`, invoiceFAQQuestion); delErr != nil {
			t.Fatalf("delete invoice FAQ: %v", delErr)
		}
		if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
			t.Fatalf("apply shipped invoice FAQ repair: %v", repairErr)
		}
		var count int
		if err := isolated.QueryRow(ctx,
			`SELECT count(*) FROM faq_entries WHERE question = $1`, invoiceFAQQuestion).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 0 {
			t.Error("the repair recreated the invoice FAQ row")
		}
		content := site.NewStore(isolated)
		assertStatutoryReturnFAQUntouched(t, content, ctx)
	})
}

func plantStaleInvoiceFAQ(t *testing.T, ctx context.Context, p *pgxpool.Pool) {
	t.Helper()
	if _, err := p.Exec(ctx, `
		UPDATE faq_entries
		SET answer = $1, answer_en = $2
		WHERE question = $3`,
		staleInvoiceFAQZh, staleInvoiceFAQEn, invoiceFAQQuestion); err != nil {
		t.Fatalf("plant stale invoice FAQ: %v", err)
	}
}

func invoiceFAQAnswers(t *testing.T, content *site.Store, ctx context.Context) (zh, en string) {
	t.Helper()
	return faqAnswer(t, content, ctx, i18n.ZhHant, invoiceFAQQuestion),
		faqAnswer(t, content, ctx, i18n.En, "How is my invoice issued?")
}

func faqAnswer(t *testing.T, content *site.Store, ctx context.Context, loc i18n.Locale, question string) string {
	t.Helper()
	rows, err := content.FAQEntries(i18n.WithLocale(ctx, loc))
	if err != nil {
		t.Fatalf("FAQEntries(%s): %v", loc, err)
	}
	for i := range rows {
		if rows[i].Question == question {
			return rows[i].Answer
		}
	}
	t.Fatalf("/faq has no %q in %s", question, loc)
	return ""
}

func assertInvoiceFAQLocales(t *testing.T, content *site.Store, ctx context.Context, wantZh, wantEn string) {
	t.Helper()
	zh, en := invoiceFAQAnswers(t, content, ctx)
	if zh != wantZh {
		t.Errorf("Chinese invoice FAQ = %q, want %q", zh, wantZh)
	}
	if en != wantEn {
		t.Errorf("English invoice FAQ = %q, want %q", en, wantEn)
	}
	if strings.Contains(zh, "尚未完成") || strings.Contains(en, "not built yet") {
		t.Errorf("published invoice FAQ still calls the issuer unfinished: zh=%q en=%q", zh, en)
	}
	if strings.Contains(zh, "已成功開立") || strings.Contains(en, "successfully issued") {
		t.Errorf("published invoice FAQ invents a successful filing: zh=%q en=%q", zh, en)
	}
	if !strings.Contains(zh, "綠界") || !strings.Contains(en, "ECPay") {
		t.Errorf("published invoice FAQ does not name the issuer: zh=%q en=%q", zh, en)
	}
}

func assertStatutoryReturnFAQUntouched(t *testing.T, content *site.Store, ctx context.Context) {
	t.Helper()
	zh := faqAnswer(t, content, ctx, i18n.ZhHant, "退貨要付運費嗎？")
	en := faqAnswer(t, content, ctx, i18n.En, "Who pays return postage?")
	if !strings.Contains(zh, "退貨運費由 goen 負擔") {
		t.Errorf("statutory return FAQ was edited: %q", zh)
	}
	if !strings.Contains(en, "Rescinding within seven days") {
		t.Errorf("statutory English return FAQ was edited: %q", en)
	}
}

const (
	refundFAQQuestion = "退款什麼時候會收到？"
	refundFAQZh       = "退貨經審核同意後，系統依原付款組成退回：卡款立刻向 Stripe 發出退款，店儲退回購物金。卡款入帳時間依發卡銀行而定，通常是數個工作天；額度退回後可立刻使用。"
	refundFAQEn       = "As soon as a return is approved we pay it back the way you paid: the card share through Stripe, store credit back to your balance. When a card refund lands depends on your card issuer, usually a few working days; credit is available again at once."
	staleRefundFAQZh  = "退貨經審核同意後,系統會立即向 Stripe 發出退款。實際入帳時間依發卡銀行而定,通常是數個工作天。"
	staleRefundFAQEn  = "As soon as a return is approved we ask Stripe to refund. When it lands depends on your card issuer, usually a few working days."
	customRefundFAQZh = "店家自訂退款說明"
	customRefundFAQEn = "Shop-edited refund note"
)

// TestAShippedRefundFAQRepairRewritesOnlyStaleLocales holds the published
// refund FAQ to the original payment composition: a fresh seed, the shipped
// repair file on a kept stale row, and each locale matched on its own
// Stripe-only sentence so a shop-edited answer stays.
func TestAShippedRefundFAQRepairRewritesOnlyStaleLocales(t *testing.T) {
	ctx := t.Context()
	content := site.NewStore(pool)
	assertRefundFAQLocales(t, content, ctx, refundFAQZh, refundFAQEn)

	catalog, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatalf("read catalogue seed: %v", err)
	}
	repair, err := os.ReadFile("../../seed/repair_refund_faq.sql")
	if err != nil {
		t.Fatalf("read shipped refund FAQ repair: %v", err)
	}

	isolated := dbtest.Pool(t)
	if _, loadErr := isolated.Exec(ctx, string(catalog)); loadErr != nil {
		t.Fatalf("seed isolated catalogue: %v", loadErr)
	}
	plantRefundFAQ(t, ctx, isolated, staleRefundFAQZh, staleRefundFAQEn)
	isolatedContent := site.NewStore(isolated)
	zh, en := refundFAQAnswers(t, isolatedContent, ctx)
	if zh != staleRefundFAQZh || en != staleRefundFAQEn {
		t.Fatalf("the planted stale answers did not reach /faq: zh=%q en=%q", zh, en)
	}

	conn, err := isolated.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire isolated connection: %v", err)
	}
	if _, seedErr := conn.Exec(ctx, string(catalog)); seedErr == nil {
		conn.Release()
		t.Fatal("the catalogue seed succeeded against a kept database; " +
			"db-seed cannot be the repair path")
	}
	if _, rollErr := conn.Exec(ctx, "ROLLBACK"); rollErr != nil {
		conn.Release()
		t.Fatalf("rollback failed catalogue re-seed: %v", rollErr)
	}
	conn.Release()
	zh, en = refundFAQAnswers(t, isolatedContent, ctx)
	if zh != staleRefundFAQZh || en != staleRefundFAQEn {
		t.Fatal("the failed catalogue re-seed changed the kept refund FAQ")
	}

	if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
		t.Fatalf("apply shipped refund FAQ repair: %v", repairErr)
	}
	assertRefundFAQLocales(t, isolatedContent, ctx, refundFAQZh, refundFAQEn)

	plantRefundFAQ(t, ctx, isolated, customRefundFAQZh, staleRefundFAQEn)
	if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
		t.Fatalf("apply shipped refund FAQ repair after a Chinese edit: %v", repairErr)
	}
	assertRefundFAQLocales(t, isolatedContent, ctx, customRefundFAQZh, refundFAQEn)

	plantRefundFAQ(t, ctx, isolated, staleRefundFAQZh, customRefundFAQEn)
	if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
		t.Fatalf("apply shipped refund FAQ repair after an English edit: %v", repairErr)
	}
	assertRefundFAQLocales(t, isolatedContent, ctx, refundFAQZh, customRefundFAQEn)
}

func plantRefundFAQ(t *testing.T, ctx context.Context, p *pgxpool.Pool, zh, en string) {
	t.Helper()
	if _, err := p.Exec(ctx, `
		UPDATE faq_entries
		SET answer = $1, answer_en = $2
		WHERE question = $3`, zh, en, refundFAQQuestion); err != nil {
		t.Fatalf("plant refund FAQ: %v", err)
	}
}

func refundFAQAnswers(t *testing.T, content *site.Store, ctx context.Context) (zh, en string) {
	t.Helper()
	return faqAnswer(t, content, ctx, i18n.ZhHant, refundFAQQuestion),
		faqAnswer(t, content, ctx, i18n.En, "When will I get my refund?")
}

func assertRefundFAQLocales(t *testing.T, content *site.Store, ctx context.Context, wantZh, wantEn string) {
	t.Helper()
	zh, en := refundFAQAnswers(t, content, ctx)
	if zh != wantZh {
		t.Errorf("Chinese refund FAQ = %q, want %q", zh, wantZh)
	}
	if en != wantEn {
		t.Errorf("English refund FAQ = %q, want %q", en, wantEn)
	}
}

func discountedProductSlugs(t *testing.T, n int) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT DISTINCT p.slug FROM products p JOIN product_variants pv ON pv.product_id = p.id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.compare_at_price_cents > pv.price_cents
		ORDER BY p.slug
		LIMIT $1`, n)
	if err != nil {
		t.Fatalf("find discounted products: %v", err)
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var slug string
		if scanErr := rows.Scan(&slug); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		slugs = append(slugs, slug)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate discounted products: %v", err)
	}
	if len(slugs) < n {
		t.Fatalf("found %d discounted products, need %d", len(slugs), n)
	}
	return slugs
}

// TestTwoConcurrentCampaignFeaturesTakeDistinctPositions proves the advisory lock
// is in the statement: without it two staff members featuring at once both read
// the same max(position) and sale_campaign_products_position_key refuses one.
func TestTwoConcurrentCampaignFeaturesTakeDistinctPositions(t *testing.T) {
	ctx, _ := staffContext(t)
	slug := admintest.CampaignSlug(t)
	if _, err := campaigns.NewStore(pool).Create(ctx, &campaigns.Form{
		Slug: slug, Title: "併發活動", Days: 7,
	}); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM sale_campaigns WHERE slug = $1`, slug)
	})
	slugs := discountedProductSlugs(t, 2)

	poolA := admintest.NamedPool(t, pool, "campaign-append-a-"+uuid.NewString()[:8])
	poolB := admintest.NamedPool(t, pool, "campaign-append-b-"+uuid.NewString()[:8])
	storeA := campaigns.NewStore(poolA)
	storeB := campaigns.NewStore(poolB)

	start := make(chan struct{})
	type result struct {
		product string
		err     error
	}
	done := make(chan result, 2)
	go func() {
		<-start
		done <- result{product: slugs[0], err: storeA.FeatureProduct(ctx, slug, slugs[0])}
	}()
	go func() {
		<-start
		done <- result{product: slugs[1], err: storeB.FeatureProduct(ctx, slug, slugs[1])}
	}()
	close(start)

	featured := make([]string, 0, 2)
	for range 2 {
		got := <-done
		if got.err != nil {
			t.Fatalf("FeatureProduct(%s): %v", got.product, got.err)
		}
		featured = append(featured, got.product)
	}

	var positions []int32
	rows, err := pool.Query(ctx, `
		SELECT cp.position
		FROM sale_campaign_products cp
		JOIN sale_campaigns c ON c.id = cp.campaign_id
		JOIN products p ON p.id = cp.product_id
		WHERE c.slug = $1 AND p.slug = ANY($2)
		ORDER BY cp.position`, slug, featured)
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p int32
		if scanErr := rows.Scan(&p); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		positions = append(positions, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate campaign positions: %v", err)
	}
	if len(positions) != 2 || positions[0] == positions[1] {
		t.Errorf("positions = %v, want two distinct values — the lock is not in "+
			"the statement, so two concurrent features collided on max(position)+1",
			positions)
	}
}

// TestAReleaseInTheLedgerNamesItsOrder drives a RELEASE, the only movement that reaches
// the order through the reservation: a HOLD stays green with that join deleted.
func TestAReleaseInTheLedgerNamesItsOrder(t *testing.T) {
	ctx, _ := staffContext(t)
	basket := cart.NewStore(pool)

	var vid uuid.UUID
	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT pv.id, pv.sku FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.stock_quantity > pv.safety_stock + 2
		ORDER BY pv.stock_quantity DESC LIMIT 1`).Scan(&vid, &sku); err != nil {
		t.Fatalf("find a stocked variant: %v", err)
	}
	number := admintest.PlaceHeldOrder(t, pool, vid)

	if _, err := basket.CancelOrder(ctx, number); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	view, err := stock.NewStore(pool).Movements(ctx, sku)
	if err != nil {
		t.Fatalf("Movements: %v", err)
	}
	var release, hold bool
	for i := range view.Rows {
		m := &view.Rows[i]
		if m.OrderNumber != number {
			continue
		}
		switch m.Reason {
		case "release":
			release = true
		case "hold":
			hold = true
		}
	}
	if !release {
		seen := make([]string, 0, len(view.Rows))
		for i := range view.Rows {
			seen = append(seen, view.Rows[i].Reason+"/"+view.Rows[i].OrderNumber)
		}
		t.Errorf("no release naming %s in the ledger: %v", number, seen)
	}
	if !hold {
		t.Errorf("no hold naming %s in the ledger", number)
	}
}

func TestParseProtectedWritesDoNotCallInfrastructureARefusal(t *testing.T) {
	ctx, _ := staffContext(t)
	p := admintest.NamedPool(t, pool, "parse_closed_"+uuid.NewString()[:8])
	p.Close()
	s := products.NewStore(p)

	writes := []struct {
		name              string
		write             func() (map[string]string, error)
		refused, notFound error
	}{
		{name: "create product", refused: products.ErrRefused, notFound: products.ErrNotFound, write: func() (map[string]string, error) {
			_, errs, err := s.Create(ctx, &products.Form{
				Slug: "closed-product", Name: "Closed", BrandID: uuid.NewString(), CategoryID: uuid.NewString(),
			})
			return errs, err
		}},
		{name: "update product", refused: products.ErrRefused, notFound: products.ErrNotFound, write: func() (map[string]string, error) {
			return s.Update(ctx, &products.Form{
				Slug: "closed-product", Name: "Closed", BrandID: uuid.NewString(), CategoryID: uuid.NewString(),
			})
		}},
		{name: "add variant", refused: products.ErrRefused, notFound: products.ErrNotFound, write: func() (map[string]string, error) {
			return s.AddVariant(ctx, "closed-product", &products.VariantForm{SKU: "CLOSED", PriceCents: 100})
		}},
		{name: "create method", refused: shipping.ErrRefused, notFound: shipping.ErrNotFound, write: func() (map[string]string, error) {
			return shipping.NewStore(p).CreateMethod(ctx, &shipping.NewMethod{
				Code: "closed_method", Destination: "address", Name: "Closed",
			})
		}},
	}
	for _, tt := range writes {
		t.Run(tt.name, func(t *testing.T) {
			errs, err := tt.write()
			if err == nil || errors.Is(err, tt.refused) || errors.Is(err, tt.notFound) {
				t.Errorf("write error category = %v, want infrastructure only", err)
			}
			if len(errs) != 0 {
				t.Errorf("write field errors = %v, want none for infrastructure", errs)
			}
		})
	}
}

func TestAMistypedParcelDimensionDoesNotBecomeUnmeasured(t *testing.T) {
	ctx, actor := staffContext(t)
	s := products.NewStore(pool)
	h := admintest.ProductDesk(pool, s)
	slug := admintest.DraftProduct(t, ctx, pool, s)
	defer func() {
		if err := s.SetStatus(context.WithoutCancel(ctx), slug, "archived"); err != nil {
			t.Errorf("archive parcel fixture: %v", err)
		}
	}()

	tests := []struct {
		name  string
		field string
		id    string
		raw   string
	}{
		{name: "safety stock", field: "safety", id: "v-safety", raw: "1000001"},
		{name: "longest side", field: "parcel_longest", id: "v-longest", raw: "5001"},
		{name: "three-side sum", field: "parcel_sum", id: "v-sum", raw: "15001"},
		{name: "weight typo", field: "parcel_weight", id: "v-weight", raw: "10,000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sku := "PARSE-" + strings.ToUpper(uuid.NewString()[:8])
			form := url.Values{
				"sku":            {sku},
				"price":          {"1000"},
				"compare":        {""},
				"safety":         {"0"},
				"parcel_longest": {"450"},
				"parcel_sum":     {"1050"},
				"parcel_weight":  {"500"},
			}
			form.Set(tt.field, tt.raw)
			res := admintest.PostVariantForm(t, h, ctx, slug, form)
			if res.Code != http.StatusUnprocessableEntity {
				t.Errorf("AddVariant(%s=%q) status = %d, want 422", tt.field, tt.raw, res.Code)
			} else {
				body := res.Body.String()
				admintest.AssertRefusedInput(t, body, tt.id, tt.raw)
				for id, field := range map[string]string{
					"v-sku": "sku", "v-price": "price", "v-compare": "compare",
					"v-safety": "safety", "v-longest": "parcel_longest",
					"v-sum": "parcel_sum", "v-weight": "parcel_weight",
				} {
					if got := admintest.InputAttribute(t, admintest.InputElementByID(t, body, id), "value"); got != form.Get(field) {
						t.Errorf("input %q value = %q, want submitted %q", id, got, form.Get(field))
					}
				}
				for _, id := range []string{"v-safety", "v-longest", "v-sum", "v-weight"} {
					admintest.AssertTextNumberControl(t, body, id)
				}
			}
			var count int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM product_variants WHERE sku = $1`, sku).Scan(&count); err != nil {
				t.Fatalf("count refused variant: %v", err)
			}
			if count != 0 {
				t.Errorf("AddVariant(%s=%q) inserted %d rows", tt.field, tt.raw, count)
			}
		})
	}

	measuredSKU := "MEASURED-" + strings.ToUpper(uuid.NewString()[:8])
	measured := url.Values{
		"sku": {measuredSKU}, "price": {"1000"}, "safety": {"0"},
		"parcel_longest": {"450"}, "parcel_sum": {"1050"}, "parcel_weight": {"15000"},
	}
	if res := admintest.PostVariantForm(t, h, ctx, slug, measured); res.Code != http.StatusSeeOther {
		t.Fatalf("add measured variant status = %d, want 303", res.Code)
	}
	unmeasuredSKU := "UNMEASURED-" + strings.ToUpper(uuid.NewString()[:8])
	unmeasured := url.Values{
		"sku": {unmeasuredSKU}, "price": {"1000"}, "safety": {"0"},
		"parcel_longest": {""}, "parcel_sum": {""}, "parcel_weight": {""},
	}
	if res := admintest.PostVariantForm(t, h, ctx, slug, unmeasured); res.Code != http.StatusSeeOther {
		t.Fatalf("add unmeasured variant status = %d, want 303", res.Code)
	}

	variantIDs := make(map[string]uuid.UUID, 2)
	rows, err := pool.Query(ctx, `
		SELECT sku, id FROM product_variants WHERE sku = ANY($1::text[])
		ORDER BY sku`, []string{measuredSKU, unmeasuredSKU})
	if err != nil {
		t.Fatalf("read parcel variants: %v", err)
	}
	for rows.Next() {
		var sku string
		var id uuid.UUID
		if err := rows.Scan(&sku, &id); err != nil {
			t.Fatalf("scan parcel variant: %v", err)
		}
		variantIDs[sku] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate parcel variants: %v", err)
	}
	rows.Close()
	if len(variantIDs) != 2 {
		t.Fatalf("read %d parcel variants, want 2", len(variantIDs))
	}
	for _, sku := range []string{measuredSKU, unmeasuredSKU} {
		if err := stock.NewStore(pool).Receive(ctx, sku, 2, actor.String(), "parse-stock-"+uuid.NewString()); err != nil {
			t.Fatalf("stock %s: %v", sku, err)
		}
	}
	if err := s.SetStatus(ctx, slug, "active"); err != nil {
		t.Fatalf("publish parcel product: %v", err)
	}

	methodCode := "parse_pickup_" + uuid.NewString()[:8]
	if errs, err := shipping.NewStore(pool).CreateMethod(ctx, &shipping.NewMethod{
		Code: methodCode, Destination: "pickup_point", Name: "量測超取",
		Carrier: "測試承運人", MaxParcelWeightG: 10000,
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateMethod: %v %v", err, errs)
	}
	var methodID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_methods WHERE code = $1`, methodCode).Scan(&methodID); err != nil {
		t.Fatalf("read parcel method: %v", err)
	}
	defer func() {
		if err := shipping.NewStore(pool).SetMethodActive(context.WithoutCancel(ctx), methodID.String(), false); err != nil {
			t.Errorf("deactivate parcel method: %v", err)
		}
	}()

	basket := cart.NewStore(pool)
	for _, tt := range []struct {
		name    string
		sku     string
		offered bool
	}{
		{name: "measured oversized parcel", sku: measuredSKU, offered: false},
		{name: "unmeasured parcel", sku: unmeasuredSKU, offered: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cartID, err := basket.Create(ctx, uuid.NewString(), uuid.NullUUID{})
			if err != nil {
				t.Fatalf("create cart: %v", err)
			}
			if addErr := basket.Add(ctx, cartID, variantIDs[tt.sku], 1); addErr != nil {
				t.Fatalf("add %s to cart: %v", tt.sku, addErr)
			}
			choices, err := basket.ShippingChoices(ctx, cartID, 100000)
			if err != nil {
				t.Fatalf("ShippingChoices: %v", err)
			}
			codes := make([]string, 0, len(choices))
			for i := range choices {
				codes = append(codes, choices[i].Code)
			}
			if got := slices.Contains(codes, methodCode); got != tt.offered {
				t.Errorf("ShippingChoices(%s) offered %q = %v, want %v; choices=%v",
					tt.sku, methodCode, got, tt.offered, codes)
			}
		})
	}
}

func TestTheShopCanFindAWarrantyTheCustomerRegistered(t *testing.T) {
	ctx, _ := staffContext(t)
	serial, number := registeredWarranty(t, "SN-"+strings.ToUpper(uuid.NewString()[:8]))

	for _, term := range []struct {
		name string
		q    string
	}{
		{name: "by the serial off the label", q: serial},
		{name: "by the order number off the confirmation mail", q: number},
	} {
		t.Run(term.name, func(t *testing.T) {
			view, err := customers.NewStore(pool).Warranties(ctx, term.q)
			if err != nil {
				t.Fatalf("search warranties: %v", err)
			}
			if !view.Searching() {
				t.Fatal("the page did not run a search for a term long enough to be one")
			}
			if len(view.Rows) != 1 {
				t.Fatalf("searching %q found %d registrations, want 1", term.q, len(view.Rows))
			}
			got := view.Rows[0]
			if got.Serial != serial || got.Order != number {
				t.Errorf("found serial %q on order %q, want %q on %q",
					got.Serial, got.Order, serial, number)
			}
			if !got.InForce {
				t.Error("a warranty registered today reads as expired")
			}
		})
	}

	view, err := customers.NewStore(pool).Warranties(ctx, "SN-NOSUCHTHING")
	if err != nil {
		t.Fatalf("search warranties: %v", err)
	}
	if len(view.Rows) != 0 {
		t.Errorf("an unregistered serial found %d rows, want 0", len(view.Rows))
	}
}

func TestTheWarrantyLookupRefusesToListEverything(t *testing.T) {
	ctx, _ := staffContext(t)
	registeredWarranty(t, "SN-"+strings.ToUpper(uuid.NewString()[:8]))

	for _, term := range []string{"", " ", "A"} {
		view, err := customers.NewStore(pool).Warranties(ctx, term)
		if err != nil {
			t.Fatalf("search warranties %q: %v", term, err)
		}
		if view.Searching() {
			t.Errorf("%q ran a search", term)
		}
		if len(view.Rows) != 0 {
			t.Errorf("%q listed %d registrations without being asked", term, len(view.Rows))
		}
	}
}

func registeredWarranty(t *testing.T, serial string) (registered, orderNumber string) {
	t.Helper()
	ctx := t.Context()

	var variantID, productID uuid.UUID
	var warrantyNote string
	var warrantyMonths int32
	if err := pool.QueryRow(ctx, `
		SELECT pv.id, p.id, coalesce(p.warranty_note, ''), p.warranty_months
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.is_active AND p.status = 'active' AND p.warranty_months IS NOT NULL
		ORDER BY pv.id LIMIT 1`).Scan(&variantID, &productID, &warrantyNote, &warrantyMonths); err != nil {
		t.Fatalf("find a variant of a product with a stated term: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('wr-' || gen_random_uuid() || '@goen.invalid', 'customer', '保固客戶')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create customer: %v", err)
	}

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}

	var lineID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (
			order_id, product_id, variant_id, sku, product_name,
			warranty_note, warranty_months, unit_price_cents, quantity
		)
		SELECT $1, $2, pv.id, pv.sku, p.name, nullif($4, ''), $5, 100000, 1
		FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $3 RETURNING order_lines.id`,
		orderID, productID, variantID, warrantyNote, warrantyMonths).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'wr@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	ref := "cs_warranty_" + number
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, ref); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`, ref); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	admintest.MoveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number, shipped_at, delivered_at)
		VALUES ($1, 'black_cat', 'WR-' || $2, now() - interval '5 days', now() - interval '3 days')
		RETURNING id`, orderID, number).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := warranty.NewStore(pool).Register(
		ctx, lineID.String(), userID.String(), serial, 1); err != nil {
		t.Fatalf("register warranty: %v", err)
	}
	return serial, number
}

func TestCategoryIconVocabularyMatchesTheDatabaseConstraint(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var parentID uuid.UUID
	if queryErr := tx.QueryRow(ctx, `
		INSERT INTO categories (slug, name, position)
		VALUES ($1, '圖示字彙測試',
		        (SELECT coalesce(max(position), -1) + 1 FROM categories WHERE parent_id IS NULL))
		RETURNING id`, "icon-vocabulary-"+uuid.NewString()[:8]).Scan(&parentID); queryErr != nil {
		t.Fatalf("create parent: %v", queryErr)
	}
	for position, key := range icons.CategoryKeys() {
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO categories (parent_id, slug, name, icon_key, position)
			VALUES ($1, $2, $3, $3, $4)`,
			parentID, "icon-"+key+"-"+uuid.NewString()[:8], key, position); execErr != nil {
			t.Fatalf("database rejects renderer-owned icon %q: %v", key, execErr)
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO categories (parent_id, slug, name, icon_key, position)
		VALUES ($1, $2, '未知圖示', 'rocket', $3)`,
		parentID, "icon-unknown-"+uuid.NewString()[:8], len(icons.CategoryKeys()))
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "categories_icon_key_known" {
		t.Fatalf("unknown persisted icon was refused by %v, want categories_icon_key_known", err)
	}
}

// TestASplitReturnResumesTheHalfThatFailed holds the resume gate to BOTH
// sources.
//
// A return can be paid from card and credit — card first, credit last — and the
// two commit separately: the card through the provider, the credit as a ledger
// entry afterwards. A gate asking only whether the CARD half settled reports a
// return whose credit compensation did NOT as finished, and no other door posts
// that credit.
//
// It also has to resume only what is MISSING. Re-sending a settled card refund
// meets refunds_settled_is_history and re-posting the credit meets its
// idempotency key, so a retry that sent both could never finish the failed half.
//
// The half-paid state is CONSTRUCTED rather than raced into: what is under test
// is the resume, not how the state arose.
func TestASplitReturnResumesTheHalfThatFailed(t *testing.T) {
	ctx, _ := staffContext(t)
	requestID, orderNumber, accountID := admintest.CreditFundedReturn(t, pool, 2, 60000)

	// Approved, with the CARD half settled under the return's own request key —
	// which is what refundRequestKey produces and what makes a retry find the
	// same row — and no credit entry at all.
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests SET status = 'approved', decided_at = now(),
		       resolution = '退貨完成'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve the return: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, reason,
		                     return_request_id, status, provider_ref, succeeded_at)
		SELECT p.id, 'return:' || $1::text, 140000, '退貨完成', $1::uuid, 'succeeded',
		       're_constructed', now()
		FROM payments p JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $2 AND p.status = 'succeeded'`,
		requestID, orderNumber); err != nil {
		t.Fatalf("settle the card half: %v", err)
	}
	if got := admintest.CardRefunded(t, pool, orderNumber); got != 140000 {
		t.Fatalf("the card half reads %d, want 140000 — the fixture did not build the "+
			"state under test", got)
	}
	before := admintest.CreditBalance(t, pool, accountID)

	// The retry must RESUME the credit half rather than refuse the whole return.
	sent := &atomic.Int64{}
	s := returnDeskOver(pool, admintest.Refunder{Sent: sent})
	if err := s.Decide(ctx, requestID.String(), "approved", "退貨完成", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("the retry was refused (%v), so the credit half can never be paid "+
			"and the customer stays short", err)
	}
	if after := admintest.CreditBalance(t, pool, accountID); after <= before {
		t.Errorf("store credit went %d -> %d; the failed half was not resumed", before, after)
	}

	// And the card half is not sent TO STRIPE twice. The durable row proves the
	// outcome; this provider-side counter proves the retry made no remote call.
	if n := sent.Load(); n != 0 {
		t.Errorf("the retry sent %d refund(s) to the provider; the card half had "+
			"already landed and resuming means paying only what is MISSING", n)
	}
	if got := admintest.CardRefunded(t, pool, orderNumber); got != 140000 {
		t.Errorf("the card half is now %d, want 140000 — the retry re-sent a refund "+
			"that had already landed", got)
	}

	// The order now holds a refund from BOTH sources, which is the only shape
	// that can tell the two definitions of "what has gone back" apart. The back
	// office displays this total, while the invoice claim independently uses the
	// same authoritative view under lock. A card-only definition would leave the
	// 統一發票 recording part of a sale that was reversed.
	withInvoices := admintest.OrderStore(
		pool, admintest.Refunder{}, noDocuments{}, admintest.DisabledInvoiceWriter{},
	)
	view, viewErr := withInvoices.Order(ctx, orderNumber)
	if viewErr != nil {
		t.Fatalf("read the order: %v", viewErr)
	}
	credited := admintest.CreditBalance(t, pool, accountID) - before
	if credited <= 0 {
		t.Fatal("no credit was returned, so this proves nothing about the sum")
	}
	if want := int64(140000) + credited; view.RefundedCents != want {
		t.Errorf("the back office reports %d refunded and %d has gone back (card %d + credit %d).\n"+
			"The display and the database-derived allowance use one fact, "+
			"and a 折讓 short of what was refunded over-reports the sale to the 財政部",
			view.RefundedCents, want, 140000, credited)
	}
}

// noDocuments is the read side with no filed documents. The figure under test
// is what has been REFUNDED, which is a question about money and not about
// documents, so the documents are the part that can be empty.
type noDocuments struct{}

func (noDocuments) Documents(context.Context, string) ([]invoice.Document, error) {
	return nil, nil
}

// TestARefusedSystemIssueIsOnTheHealthPage: nobody watches an automatic issue,
// so ECPay refusing it stays in front of a person until a later issue exists. A
// staff claim's refusal was shown to the person who pressed the button and is
// not listed.
func TestARefusedSystemIssueIsOnTheHealthPage(t *testing.T) {
	ctx, actor := staffContext(t)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))

	refuse := func(number string, orderID uuid.UUID, kind string) {
		t.Helper()
		staffActor := uuid.NullUUID{UUID: actor, Valid: kind == "staff"}
		if _, err := pool.Exec(ctx, `
			INSERT INTO invoice_operations
			    (order_id, kind, provider_key, amount_cents, request_payload,
			     actor_user_id, actor_id_snapshot, actor_kind, request_id,
			     status, last_error, created_at)
			VALUES ($1, 'issue', replace($2, '-', ''), 100000, '{}', $3, $3, $4,
			        'refused:' || $2, 'rejected', 'issue_provider_rejected_2000006',
			        now() - interval '1 minute')`,
			orderID, number, staffActor, kind); err != nil {
			t.Fatalf("record a refused %s issue for %s: %v", kind, number, err)
		}
	}
	listed := func(number string) bool {
		t.Helper()
		view, err := health.NewStore(pool).WorkerHealth(ctx, worker)
		if err != nil {
			t.Fatalf("health: %v", err)
		}
		for _, c := range view.StrandedClaims {
			if c.OrderNumber == number {
				return true
			}
		}
		return false
	}

	systemNumber, systemOrder := paidPickingOrderForUser(t, cancelPointsCustomer(t), 100000)
	refuse(systemNumber, systemOrder, "system")
	staffNumber, staffOrder := paidPickingOrderForUser(t, cancelPointsCustomer(t), 100000)
	refuse(staffNumber, staffOrder, "staff")
	creditNumber, creditOrder, _ := admintest.PaidUnshippedOrder(t, pool, 0, 100000, false)
	refuse(creditNumber, creditOrder, "system")

	if !listed(systemNumber) {
		t.Error("a refused automatic issue is not on /admin/health; the paid order " +
			"goes uninvoiced with nothing to say so")
	}
	if !listed(creditNumber) {
		t.Error("a refused automatic issue on an order store credit paid in full is not " +
			"on /admin/health; the order is pending, and owes its invoice all the same")
	}
	if listed(staffNumber) {
		t.Error("a staff claim's refusal is on /admin/health; the person who pressed " +
			"the button already saw it")
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_operations
		    (order_id, kind, provider_key, amount_cents, request_payload,
		     actor_user_id, actor_id_snapshot, request_id)
		VALUES ($1, 'issue', replace($2, '-', '') || 'R1', 100000, '{}', $3, $3,
		        'retry:' || $2)`, systemOrder, systemNumber, actor); err != nil {
		t.Fatalf("issue %s again: %v", systemNumber, err)
	}
	if listed(systemNumber) {
		t.Error("the refused automatic issue is still listed after staff issued the order again")
	}
}

// TestAStrandedInvoiceClaimIsOnTheHealthPage holds the alarm and the only
// auditable recovery door for an aged ambiguous Allowance.
//
// A 折讓 claim is taken before ECPay is asked, because their allowance endpoint
// carries no idempotency field. A call that was not ANSWERED keeps its claim —
// right, because whether the document was filed is not knowable from here — and
// that leaves a row nothing else can resolve, the shape
// payment_webhook_events.unreconciled already has.
func TestAStrandedInvoiceClaimIsOnTheHealthPage(t *testing.T) {
	ctx, actor := staffContext(t)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))

	number, orderID, _, _ := admintest.TwoLineOrderWithStock(t, pool, "stranded")
	var invoiceID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
		VALUES ($1, 'invoice', 'GD-' || substr(replace(gen_random_uuid()::text,'-',''),1,8), 100000)
		RETURNING id`, orderID).Scan(&invoiceID); err != nil {
		t.Fatalf("file an invoice: %v", err)
	}

	// A claim taken JUST NOW is a call in flight, not a stuck one: the window is
	// the whole distinction, so the durable operation fixture has to sit on the
	// far side of it. A pending invoice_documents row is not a tax document and
	// is deliberately no longer a state the schema admits.
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_operations
		    (order_id,kind,target_document_id,provider_key,amount_cents,
		     request_payload,actor_user_id,actor_id_snapshot,request_id,
		     send_attempts,last_send_at,last_error,created_at,updated_at)
		SELECT $1,'allowance',d.id,d.number,50000,
		       jsonb_build_object(
		           'invoice_number',d.number,
		           'invoice_date',to_char(shop_day(d.issued_at), 'YYYY-MM-DD'),
		           'customer_name','王小明','email','stranded@goen.invalid',
		           'amount_cents',50000,
		           'lines',jsonb_build_array(jsonb_build_object(
		               'description','退貨折讓','quantity',1,
		               'unit_price_cents',50000,'amount_cents',50000))),
		       $3,$3,$4,1,now()-interval '1 hour','allowance_not_yet_visible',
		       now()-interval '1 hour',now()-interval '1 hour'
		FROM invoice_documents d WHERE d.id=$2`,
		orderID, invoiceID, actor, "invoice-stranded:"+number); err != nil {
		t.Fatalf("strand a claim: %v", err)
	}

	view, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if view.ClaimsSettled() {
		t.Fatal("a 折讓 claim the provider never answered is invisible on the one " +
			"page built to show what needs doing — the only sign is a button that " +
			"refuses, on one order, months later")
	}
	if view.AllHealthy() {
		t.Error("the page reads healthy with a claim nobody can settle outstanding")
	}
	var named, actionable bool
	var operation string
	for _, c := range view.StrandedClaims {
		if c.OrderNumber == number {
			named = true
			actionable = c.CanAuthorizeResend
			operation = c.Operation
		}
	}
	if !named {
		t.Errorf("the claim is counted and not named; an operator needs the order "+
			"to go and look at ECPay. Got %d claim(s).", len(view.StrandedClaims))
	}
	if !actionable {
		t.Fatal("an aged empty Allowance lookup has no explicit one-resend authorization door")
	}

	// Naming the operation is not confirmation. The typed conclusion is required
	// and an active worker lease makes even that conclusion ineligible.
	post := func(values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/admin/health/reconcile", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res := httptest.NewRecorder()
		healthHandler(pool).Reconcile(res, req)
		return res
	}
	omitted := post(url.Values{"invoice_operation": {operation}})
	if omitted.Code != http.StatusSeeOther ||
		omitted.Header().Get("Location") != "/admin/health?notflagged=1" {
		t.Fatalf("unconfirmed allowance form = %d %q, want refusal redirect",
			omitted.Code, omitted.Header().Get("Location"))
	}

	leaseOwner := uuid.New()
	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations
		SET lease_owner=$2, lease_until=now()+interval '1 minute'
		WHERE id=$1`, operation, leaseOwner); err != nil {
		t.Fatalf("hold a live worker lease: %v", err)
	}
	form := url.Values{
		"invoice_operation":  {operation},
		"invoice_resolution": {"confirmed_absent"},
	}
	leasing := post(form)
	if leasing.Code != http.StatusSeeOther ||
		leasing.Header().Get("Location") != "/admin/health?notflagged=1" {
		t.Fatalf("authorization during lease = %d %q, want refusal redirect",
			leasing.Code, leasing.Header().Get("Location"))
	}
	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations SET lease_owner=NULL, lease_until=NULL WHERE id=$1`,
		operation); err != nil {
		t.Fatalf("release worker lease: %v", err)
	}

	accepted := post(form)
	if accepted.Code != http.StatusSeeOther ||
		accepted.Header().Get("Location") != "/admin/health?invoicequeued=1" {
		t.Fatalf("confirmed allowance form = %d %q, want queued redirect",
			accepted.Code, accepted.Header().Get("Location"))
	}
	duplicate := post(form)
	if duplicate.Code != http.StatusSeeOther ||
		duplicate.Header().Get("Location") != "/admin/health?notflagged=1" {
		t.Fatalf("duplicate authorization = %d %q, want refusal redirect",
			duplicate.Code, duplicate.Header().Get("Location"))
	}

	var authorizations, audits int
	var auditActor uuid.UUID
	var auditRequest string
	if err := pool.QueryRow(ctx, `
		SELECT op.resend_authorizations,
		       (SELECT count(*)::integer FROM audit_events a
		        WHERE a.entity_id=op.id
		          AND a.action=$2),
		       coalesce((SELECT a.actor_id_snapshot FROM audit_events a
		                 WHERE a.entity_id=op.id
		                   AND a.action=$2
		                 ORDER BY a.occurred_at LIMIT 1),
		                '00000000-0000-0000-0000-000000000000'::uuid),
		       coalesce((SELECT a.request_id FROM audit_events a
		                 WHERE a.entity_id=op.id
		                   AND a.action=$2
		                 ORDER BY a.occurred_at LIMIT 1), '')
		FROM invoice_operations op
		WHERE op.id=$1`, operation, audit.ActionAuthorizeAllowanceResend).
		Scan(&authorizations, &audits, &auditActor, &auditRequest); err != nil {
		t.Fatalf("read authorization and audit: %v", err)
	}
	if authorizations != 1 || audits != 1 || auditActor != actor ||
		auditRequest != "req-"+actor.String()[:8] {
		t.Fatalf("authorization/audit = %d/%d actor %s request %q",
			authorizations, audits, auditActor, auditRequest)
	}
}

// TestASystemAuditRowIsReadAsTheSystem: a system row carries no user and no
// snapshot, which must neither break the trail nor read as an erased account.
func TestASystemAuditRowIsReadAsTheSystem(t *testing.T) {
	ctx, _ := staffContext(t)
	trigger := "evt_audit_" + uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events (actor_kind, action, entity_table, request_id)
		VALUES ('system', 'invoice.issue', 'invoice_documents', $1)`, trigger); err != nil {
		t.Fatalf("record a system audit row: %v", err)
	}

	view, err := audit.NewStore(pool).Events(ctx)
	if err != nil {
		t.Fatalf("Audit with a system row: %v", err)
	}
	for _, e := range view.Rows {
		if e.RequestID == trigger {
			if !e.System {
				t.Errorf("the system row reads as person %q", e.Actor)
			}
			return
		}
	}
	t.Errorf("no audit row carries %s among %d rows", trigger, len(view.Rows))
}
