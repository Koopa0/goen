//go:build integration

package returns_test

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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/outbox"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/invoice"
	adminpages "github.com/koopa0/goen/internal/ui/pages/admin"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/health"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/loyalty"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/admin/refundstate"
	"github.com/koopa0/goen/internal/admin/reports"
	"github.com/koopa0/goen/internal/admin/returns"
	"github.com/koopa0/goen/internal/i18n"
	returnrules "github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/web"
)

type returnQueryCountKey struct{}

type returnQueryTracer struct {
	queries *atomic.Int64
	mu      *sync.Mutex
	names   *[]string
}

func (t returnQueryTracer) TraceQueryStart(
	ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData,
) context.Context {
	if _, ok := ctx.Value(returnQueryCountKey{}).(struct{}); ok {
		t.queries.Add(1)
		name, _, _ := strings.Cut(data.SQL, "\n")
		t.mu.Lock()
		*t.names = append(*t.names, name)
		t.mu.Unlock()
	}
	return ctx
}

func (returnQueryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// returnedOrderAtOn is the delivered sibling of returnedOrder: the rescission
// window is a read of the two explicit database clocks, so neither may inherit
// the test process's wall clock.
func returnedOrderAtOn(
	t *testing.T, p *pgxpool.Pool, delivered, requested time.Time,
) (requestID uuid.UUID) {
	t.Helper()
	return admintest.ReturnedOrderAtWithReason(t, p, delivered, requested, "不合用")
}

func deliveredOrderAt(t *testing.T, delivered time.Time) (number string, lineID uuid.UUID) {
	t.Helper()
	return deliveredOrderAtOn(t, pool, delivered)
}

func deliveredOrderAtOn(t *testing.T, p *pgxpool.Pool, delivered time.Time) (number string, lineID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'REF-POLICY', '政策視窗測試', 100000, 1) RETURNING id`,
		orderID).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'policy-return@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	sessionID := "cs_ret_policy_" + number
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, sessionID); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`, sessionID); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	admintest.MoveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (
			order_id, carrier, tracking_number, shipped_at, delivered_at
		) VALUES ($1, 'black_cat', 'T-POLICY-' || $2, $3, $4)
		RETURNING id`, orderID, number, delivered.Add(-48*time.Hour), delivered).Scan(&shipmentID); err != nil {
		t.Fatalf("create delivered shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, lineID
}

func TestAReturnTakesBackItsPointsAndSpend(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})

	t.Run("full return", func(t *testing.T) {
		requestID, orderID, userID := admintest.LoyaltyReturn(t, pool, []int64{1200000}, 0)
		if err := s.Decide(ctx, requestID.String(), "approved", "全額退貨", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("settle return: %v", err)
		}
		assertReturnedLoyalty(t, requestID, orderID, userID, 0, -120)

		// The already-paid retry is refused, but must not post a second clawback.
		_ = s.Decide(ctx, requestID.String(), "approved", "重試", "", uuid.NullUUID{})
		var rows int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM loyalty_entries
			WHERE order_id = $1 AND kind = 'clawback'`, orderID).Scan(&rows); err != nil {
			t.Fatalf("count retried clawbacks: %v", err)
		}
		if rows != 1 {
			t.Errorf("retry left %d clawbacks, want 1", rows)
		}
		var tier uuid.NullUUID
		if err := pool.QueryRow(ctx,
			`SELECT member_tier($1, $2, NULL)`, userID, loyalty.MembershipWindowDays).Scan(&tier); err != nil {
			t.Fatalf("read tier: %v", err)
		}
		if tier.Valid {
			t.Errorf("fully returned only order still grants tier %s", tier.UUID)
		}
	})

	t.Run("partial return", func(t *testing.T) {
		requestID, orderID, userID := admintest.LoyaltyReturn(t, pool, []int64{700000, 500000}, 1)
		// Change today's tier AFTER the award. A clawback derived from member_tier
		// at return time takes 65 points; the durable 120-point award lot says this
		// 5/12 return owns 50. This is the temporal mismatch the fixture locks.
		addTierSpend(t, userID, 5000000)
		var multiplier int32
		if err := pool.QueryRow(ctx, `
			SELECT t.points_multiplier_bp
			FROM membership_tiers t
			WHERE t.id = member_tier($1, $2, $3)`,
			userID, loyalty.MembershipWindowDays, orderID).Scan(&multiplier); err != nil {
			t.Fatalf("read changed return-time tier: %v", err)
		}
		if multiplier != 13000 {
			t.Fatalf("return-time multiplier = %d, want 13000; the fixture cannot distinguish the old recomputation", multiplier)
		}
		var refunded int64
		if err := pool.QueryRow(ctx, `SELECT return_refundable_amount($1)`, requestID).Scan(&refunded); err != nil {
			t.Fatalf("read refundable amount: %v", err)
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "部分退貨", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("settle return: %v", err)
		}
		assertReturnedLoyalty(t, requestID, orderID, userID,
			5000000+1200000-refunded, -(refunded / 10000))
	})
}

func addTierSpend(t *testing.T, userID uuid.UUID, cents int64) {
	t.Helper()
	ctx := t.Context()
	var orderID uuid.UUID
	var number string
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tier order: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &number); err != nil {
		t.Fatalf("create tier order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1::uuid, 'RET-TIER-' || ($1::uuid)::text, '等級測試商品', $2, 1)`, orderID, cents); err != nil {
		t.Fatalf("create tier line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'tier-return@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create tier private data: %v", err)
	}
	ref := "cs_return_tier_" + orderID.String()
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3::bigint)`, orderID, ref, cents); err != nil {
		t.Fatalf("open tier payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2::bigint, NULL, NULL)`, ref, cents); err != nil {
		t.Fatalf("capture tier payment: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tier order %s: %v", number, err)
	}
}

// TestAClawbackOnlyFailureRemainsRetryable models the narrow commit boundary
// after the money and customer timeline have landed but before the separate
// points posting. A queue derived only from money calls this return complete,
// hides the retry control, and strands the missing clawback forever.
func TestAClawbackOnlyFailureRemainsRetryable(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, orderID, _ := admintest.LoyaltyReturn(t, pool, []int64{1200000}, 0)
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests
		SET status = 'approved', decided_at = now(), resolution = '退款完成'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve return: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, reason,
		                     return_request_id, status, provider_ref, succeeded_at)
		SELECT p.id, 'return:' || ($1::uuid)::text, 1200000, '退款完成', $1::uuid,
		       'succeeded', 're_points_gap_' || ($1::uuid)::text, now()
		FROM payments p
		WHERE p.order_id = $2 AND p.status = 'succeeded'`, requestID, orderID); err != nil {
		t.Fatalf("settle money before clawback: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_events (order_id, kind, return_request_id)
		VALUES ($1, 'refunded', $2)`, orderID, requestID); err != nil {
		t.Fatalf("record timeline before clawback: %v", err)
	}

	s := storeOver(pool, admintest.Refunder{})
	if err := s.Inspect(ctx, requestID.String(), []returns.LineInspection{{
		OrderLineID: returnLineID(t, requestID), Received: 1, Restocked: 0,
	}}, actor); err != nil {
		t.Fatalf("inspect points-gap return: %v", err)
	}
	completeErr := s.Complete(ctx, requestID.String(), "已驗貨", actor)
	pgErr, ok := errors.AsType[*pgconn.PgError](completeErr)
	if !errors.Is(completeErr, returns.ErrRefused) || !ok ||
		pgErr.ConstraintName != "return_requests_completed_points_settled" {
		t.Fatalf("completion without clawback = %v, want return_requests_completed_points_settled",
			completeErr)
	}
	view, err := s.Queue(ctx)
	if err != nil {
		t.Fatalf("read queue: %v", err)
	}
	for i := range view.Rows {
		if view.Rows[i].ID == requestID.String() {
			if !view.Rows[i].PayoutOutstanding || view.Rows[i].PayoutBlocked {
				t.Fatalf("points-only gap renders outstanding=%v blocked=%v, want true/false",
					view.Rows[i].PayoutOutstanding, view.Rows[i].PayoutBlocked)
			}
			goto retry
		}
	}
	t.Fatalf("return %s is absent from queue", requestID)

retry:
	if decideErr := s.Decide(ctx, requestID.String(), "approved", "補登點數", "", uuid.NullUUID{}); decideErr != nil {
		t.Fatalf("retry missing clawback: %v", decideErr)
	}
	var points, requested int64
	if queryErr := pool.QueryRow(ctx, `
		SELECT points, requested_points
		FROM loyalty_entries
		WHERE return_request_id = $1 AND kind = 'clawback'`, requestID).
		Scan(&points, &requested); queryErr != nil {
		t.Fatalf("read retried clawback: %v", queryErr)
	}
	if points != -120 || requested != 120 {
		t.Errorf("retried clawback = %d requested %d, want -120/120", points, requested)
	}
	if completeErr := s.Complete(ctx, requestID.String(), "已退款、驗貨並回收點數", actor); completeErr != nil {
		t.Fatalf("complete return after clawback retry: %v", completeErr)
	}
	view, err = s.Queue(ctx)
	if err != nil {
		t.Fatalf("read repaired queue: %v", err)
	}
	for i := range view.Rows {
		if view.Rows[i].ID == requestID.String() && view.Rows[i].PayoutOutstanding {
			t.Error("completed clawback still offers a payout retry")
		}
	}
}

// TestASettledReturnCanClawPointsBackAfterOwnerErasure pins the recovery order:
// card money may settle, the user may then lawfully erase their account, and
// the separately durable loyalty clawback must still be visible and runnable.
// The award lot and loyalty account are retained accounting records even though
// orders.user_id is detached.
func TestASettledReturnCanClawPointsBackAfterOwnerErasure(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, orderID, userID := admintest.LoyaltyReturn(t, pool, []int64{1200000}, 0)
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests
		SET status = 'approved', decided_at = now(), resolution = '退款完成'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve return: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, reason,
		                     return_request_id, status, provider_ref, succeeded_at)
		SELECT p.id, 'return:' || ($1::uuid)::text, 1200000, '退款完成', $1::uuid,
		       'succeeded', 're_erased_points_gap_' || ($1::uuid)::text, now()
		FROM payments p
		WHERE p.order_id = $2 AND p.status = 'succeeded'`, requestID, orderID); err != nil {
		t.Fatalf("settle money before erasure: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_events (order_id, kind, return_request_id)
		VALUES ($1, 'refunded', $2)`, orderID, requestID); err != nil {
		t.Fatalf("record timeline before erasure: %v", err)
	}

	if err := account.NewStore(pool).Erase(ctx, userID.String()); err != nil {
		t.Fatalf("erase owner after money settled: %v", err)
	}
	var detached bool
	if err := pool.QueryRow(ctx, `
		SELECT user_id IS NULL FROM orders WHERE id = $1`, orderID).Scan(&detached); err != nil {
		t.Fatalf("read erased order owner: %v", err)
	}
	if !detached {
		t.Fatal("erasure did not detach the order owner")
	}

	s := storeOver(pool, admintest.Refunder{})
	view, err := s.Queue(ctx)
	if err != nil {
		t.Fatalf("read erased-owner recovery queue: %v", err)
	}
	found := false
	for i := range view.Rows {
		if view.Rows[i].ID != requestID.String() {
			continue
		}
		found = true
		if !view.Rows[i].PayoutOutstanding || view.Rows[i].PayoutBlocked {
			t.Fatalf("erased-owner clawback renders outstanding=%v blocked=%v, want true/false",
				view.Rows[i].PayoutOutstanding, view.Rows[i].PayoutBlocked)
		}
	}
	if !found {
		t.Fatalf("return %s is absent from the erased-owner recovery queue", requestID)
	}

	if err := s.Decide(ctx, requestID.String(), "approved", "補登點數", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("retry clawback after erasure: %v", err)
	}
	var points, requested int64
	if err := pool.QueryRow(ctx, `
		SELECT points, requested_points
		FROM loyalty_entries
		WHERE return_request_id = $1 AND kind = 'clawback'`, requestID).
		Scan(&points, &requested); err != nil {
		t.Fatalf("read post-erasure clawback: %v", err)
	}
	if points != -120 || requested != 120 {
		t.Errorf("post-erasure clawback = %d requested %d, want -120/120", points, requested)
	}

	if err := s.Inspect(ctx, requestID.String(), []returns.LineInspection{{
		OrderLineID: returnLineID(t, requestID), Received: 1, Restocked: 0,
	}}, actor); err != nil {
		t.Fatalf("inspect erased-owner return: %v", err)
	}
	if err := s.Complete(ctx, requestID.String(), "已退款、驗貨並回收點數", actor); err != nil {
		t.Fatalf("complete erased-owner return after clawback: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read completed erased-owner return: %v", err)
	}
	if status != "completed" {
		t.Errorf("erased-owner return status = %q, want completed", status)
	}
}

func assertReturnedLoyalty(t *testing.T, requestID, orderID, userID uuid.UUID, wantSpend, wantPoints int64) {
	t.Helper()
	ctx := t.Context()
	var spend int64
	if err := pool.QueryRow(ctx,
		`SELECT member_spend($1, $2, NULL)`, userID, loyalty.MembershipWindowDays).Scan(&spend); err != nil {
		t.Fatalf("read member spend: %v", err)
	}
	if spend != wantSpend {
		t.Errorf("member spend = %d, want %d", spend, wantSpend)
	}
	var points, requested int64
	var key string
	if err := pool.QueryRow(ctx, `
		SELECT points, requested_points, idempotency_key
		FROM loyalty_entries
		WHERE order_id = $1 AND kind = 'clawback'`, orderID).Scan(&points, &requested, &key); err != nil {
		t.Fatalf("read clawback: %v", err)
	}
	if points != wantPoints || requested != -wantPoints {
		t.Errorf("clawback points/requested = %d/%d, want %d/%d",
			points, requested, wantPoints, -wantPoints)
	}
	if want := "return:" + requestID.String(); key != want {
		t.Errorf("clawback key = %q, want %q", key, want)
	}
	var balance int64
	if err := pool.QueryRow(ctx, `
		SELECT b.points FROM loyalty_balances b
		JOIN store_credit_accounts a ON a.id = b.account_id
		WHERE a.user_id = $1`, userID).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if balance != 120+wantPoints {
		t.Errorf("points balance = %d, want %d", balance, 120+wantPoints)
	}
}

func couponedShippedOrder(t *testing.T, lines int) (requestID uuid.UUID, orderNumber string) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents, discount_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 8000, 50000
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	lineIDs := make([]uuid.UUID, 2)
	for i := range lineIDs {
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, $2, '測試商品', 50000, 1, $3) RETURNING id`,
			orderID, fmt.Sprintf("CPN-SKU-%d", i), i).Scan(&lineIDs[i]); err != nil {
			t.Fatalf("create line %d: %v", i, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'c@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	ref := "cs_cpn_" + orderNumber
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 58000)`, orderID, ref); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 58000, NULL, NULL)`, ref); err != nil {
		t.Fatalf("capture: %v", err)
	}
	admintest.MoveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'black_cat', 'TC-'||$2) RETURNING id`, orderID, orderNumber).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason) VALUES ($1, '不合用') RETURNING id`,
		orderID).Scan(&requestID); err != nil {
		t.Fatalf("create return request: %v", err)
	}
	for i := range lineIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
			VALUES ($1, $2, $3, 1)`, orderID, shipmentID, lineIDs[i]); err != nil {
			t.Fatalf("create shipment line %d: %v", i, err)
		}
		if i < lines {
			if _, err := tx.Exec(ctx, `
				INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
				VALUES ($1, $2, $3, 1)`, orderID, requestID, lineIDs[i]); err != nil {
				t.Fatalf("create return line %d: %v", i, err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return requestID, orderNumber
}

func twoLineOrderForSequentialReturns(t *testing.T) (
	orderID uuid.UUID, orderNumber string, lineIDs [2]uuid.UUID,
) {
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
		SELECT next_order_number(), v.id, sm.code, v.name, 15000
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	for i := range lineIDs {
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, $2, '運費退貨商品', 100000, 1, $3) RETURNING id`,
			orderID, fmt.Sprintf("RETURN-FEE-%d-%s", i, orderID), i).Scan(&lineIDs[i]); err != nil {
			t.Fatalf("create line %d: %v", i, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'return-fee@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	ref := "cs_return_fee_" + orderNumber
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 215000)`, orderID, ref); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 215000, NULL, NULL)`, ref); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	admintest.MoveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'black_cat', 'RETURN-FEE-' || $2) RETURNING id`, orderID, orderNumber).
		Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	for i := range lineIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
			VALUES ($1, $2, $3, 1)`, orderID, shipmentID, lineIDs[i]); err != nil {
			t.Fatalf("ship line %d: %v", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return orderID, orderNumber, lineIDs
}

func TestARefundIsWhatTheCustomerPaid(t *testing.T) {
	tests := []struct {
		name  string
		lines int
		want  int64
		why   string
	}{
		{
			name: "one of two lines, so a proportional share of the coupon", lines: 1,
			want: 25000,
			why:  "the customer paid NT$250 for this item after the coupon, not NT$500",
		},
		{
			name: "both lines, so the whole contract and the delivery fee with it", lines: 2,
			want: 58000,
			why:  "a rescission returns everything paid under the contract, delivery included",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := admintest.StaffContext(t, pool)
			s := storeOver(pool, admintest.Refunder{})
			requestID, _ := couponedShippedOrder(t, tt.lines)

			if err := s.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err != nil {
				t.Fatalf("approve: %v — a return the shop cannot pay for is the defect", err)
			}
			var amount int64
			if err := pool.QueryRow(ctx, `
				SELECT amount_cents FROM refunds WHERE return_request_id = $1`,
				requestID).Scan(&amount); err != nil {
				t.Fatalf("read refund: %v", err)
			}
			if amount != tt.want {
				t.Errorf("refunded %d, want %d — %s", amount, tt.want, tt.why)
			}
		})
	}
}

func TestTheDeliveryFeeIsPaidBackOnce(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	orderID, number, lines := twoLineOrderForSequentialReturns(t)
	customerReturns := returnrules.NewStore(pool)
	shop := storeOver(pool, admintest.Refunder{})

	openAndApprove := func(reason string, lineID uuid.UUID) int64 {
		t.Helper()
		if err := customerReturns.Open(ctx, number, uuid.NullUUID{}, &returnrules.Request{
			Reason: reason, Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open %s return: %v", reason, err)
		}
		var requestID uuid.UUID
		if err := pool.QueryRow(ctx, `
			SELECT id FROM return_requests
			WHERE order_id = $1 AND status = 'requested'`, orderID).Scan(&requestID); err != nil {
			t.Fatalf("find %s return: %v", reason, err)
		}
		if err := shop.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("approve %s return: %v", reason, err)
		}
		var amount int64
		if err := pool.QueryRow(ctx,
			`SELECT amount_cents FROM refunds WHERE return_request_id = $1`, requestID).
			Scan(&amount); err != nil {
			t.Fatalf("read %s refund: %v", reason, err)
		}
		return amount
	}

	first := openAndApprove("first line", lines[0])
	if first != 100000 {
		t.Errorf("first line refunded %d, want 100000 without delivery", first)
	}
	second := openAndApprove("second line", lines[1])
	if second != 115000 {
		t.Errorf("second line refunded %d, want 115000 with delivery", second)
	}

	var captured, refunded int64
	if err := pool.QueryRow(ctx, `
		SELECT p.captured_amount_cents,
		       (SELECT coalesce(sum(r.amount_cents), 0)
		        FROM refunds r JOIN return_requests rr ON rr.id = r.return_request_id
		        WHERE rr.order_id = $1)
		FROM payments p WHERE p.order_id = $1 AND p.status = 'succeeded'`, orderID).
		Scan(&captured, &refunded); err != nil {
		t.Fatalf("read refund total: %v", err)
	}
	if captured != 215000 || refunded != captured {
		t.Errorf("captured=%d refunded=%d, want both 215000", captured, refunded)
	}
}

func TestApprovingAReturnRefundsWhatTheORDERSays(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	requestID, _ := admintest.ReturnedOrder(t, pool, 1)

	if err := s.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("approve: %v", err)
	}

	var status string
	var amount int64
	var providerRef *string
	if err := pool.QueryRow(ctx, `
		SELECT status, amount_cents, provider_ref FROM refunds
		WHERE return_request_id = $1`, requestID).Scan(&status, &amount, &providerRef); err != nil {
		t.Fatalf("read refund: %v", err)
	}
	if status != "succeeded" {
		t.Errorf("refund status is %q, want succeeded", status)
	}
	if amount != 100000 {
		t.Errorf("refunded %d, want 100000 — one unit at the order's own price", amount)
	}
	if providerRef == nil || *providerRef == "" {
		t.Error("no provider reference recorded; the refund cannot be reconciled")
	}

	var returnStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
		t.Fatalf("read return: %v", err)
	}
	if returnStatus != "approved" {
		t.Errorf("return is %q, want approved", returnStatus)
	}
}

func TestAFailedRefundLeavesARowToReconcile(t *testing.T) {
	tests := []struct {
		name       string
		refunder   admintest.Refunder
		wantStatus string
		why        string
	}{
		{
			name:       "stripe unreachable before anything was asked",
			refunder:   admintest.Refunder{FailIntent: true},
			wantStatus: "pending",
			why:        "goen never asked Stripe to refund, so the claim is still outstanding",
		},
		{
			name:       "the request timed out, so nobody knows what Stripe did",
			refunder:   admintest.Refunder{RefundErr: errors.New("context deadline exceeded")},
			wantStatus: "pending",
			why: "goen did not hear an answer, and 'failed' would claim the money " +
				"is still at the shop AND free the capture to be claimed twice",
		},
		{
			name: "stripe refused the refund",
			refunder: admintest.Refunder{RefundErr: fmt.Errorf("%w: %w",
				refunds.ErrCreateRejected,
				&stripe.Error{
					Type: stripe.ErrorTypeInvalidRequest,
					Code: stripe.ErrorCodeChargeAlreadyRefunded,
					Msg:  "charge has already been refunded",
				})},
			wantStatus: "failed",
			why:        "Stripe was asked and said no",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := admintest.StaffContext(t, pool)
			s := storeOver(pool, tt.refunder)
			requestID, _ := admintest.ReturnedOrder(t, pool, 2)

			if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err == nil {
				t.Fatal("a refund that did not happen was reported as success")
			}

			var status string
			var amount int64
			if err := pool.QueryRow(ctx, `
				SELECT status, amount_cents FROM refunds WHERE return_request_id = $1`,
				requestID).Scan(&status, &amount); err != nil {
				t.Fatalf("no refund row survives a failed provider call — nothing "+
					"can reconcile the money: %v", err)
			}
			if status != tt.wantStatus {
				t.Errorf("refund status is %q, want %q — %s", status, tt.wantStatus, tt.why)
			}
			if amount != 200000 {
				t.Errorf("row records %d, want 200000", amount)
			}

			var returnStatus string
			if err := pool.QueryRow(ctx,
				`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
				t.Fatalf("read return: %v", err)
			}
			// APPROVED. The decision commits before a cent moves, which is what
			// stops two staff members deciding one return at once from both
			// paying, so a refund that fails afterwards cannot reopen it. What
			// must be true instead is that the attempt is on record and can be
			// finished.
			if returnStatus != "approved" {
				t.Errorf("return is %q after a refund that did not happen, want "+
					"approved — the shop DID agree to the return, and the money "+
					"is what is outstanding", returnStatus)
			}
		})
	}
}

func TestAStalledRefundCanBeRetriedToCompletion(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	requestID, _ := admintest.ReturnedOrder(t, pool, 2)

	stalled := storeOver(pool, admintest.Refunder{
		RefundErr: errors.New("read tcp 1.2.3.4:443: i/o timeout"),
	})

	if err := stalled.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err == nil {
		t.Fatal("a refund that timed out was reported as success")
	}

	healthy := storeOver(pool, admintest.Refunder{})
	if err := healthy.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("the retry was refused, so a stalled refund can never be finished "+
			"and the customer is never paid: %v", err)
	}

	var rows int
	var status string
	var succeededAt *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT count(*), min(status), min(succeeded_at) FROM refunds
		WHERE return_request_id = $1`, requestID).Scan(&rows, &status, &succeededAt); err != nil {
		t.Fatalf("read refunds: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d refund rows after a retry, want 1 — request_key exists so the "+
			"retry finds its own row rather than opening a second claim", rows)
	}
	if status != "succeeded" {
		t.Errorf("refund status is %q after a successful retry, want succeeded", status)
	}
	if succeededAt == nil {
		t.Error("no succeeded_at on a succeeded refund")
	}

	var returnStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
		t.Fatalf("read return: %v", err)
	}
	if returnStatus != "approved" {
		t.Errorf("return is %q after the refund finally went through, want approved", returnStatus)
	}
}

// TestReturnCompletionWaitsForExactPayoutAndClawback keeps an inspected parcel
// visible as approved work while its provider claim is ambiguous. Completion is
// admitted only after the same durable claim succeeds and the corresponding
// loyalty reversal is present; otherwise completed would hide every retry door.
func TestReturnCompletionWaitsForExactPayoutAndClawback(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, _, _ := admintest.LoyaltyReturn(t, pool, []int64{1200000}, 0)
	lineID := returnLineID(t, requestID)

	stalled := storeOver(pool, admintest.Refunder{
		RefundErr: errors.New("provider outcome is ambiguous"),
	})
	if err := stalled.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", actor); err == nil {
		t.Fatal("ambiguous provider refund was reported as settled")
	}
	if err := stalled.Inspect(ctx, requestID.String(), []returns.LineInspection{{
		OrderLineID: lineID, Received: 1, Restocked: 0,
	}}, actor); err != nil {
		t.Fatalf("inspect return before payout retry: %v", err)
	}

	err := stalled.Complete(ctx, requestID.String(), "已驗貨", actor)
	if !errors.Is(err, returns.ErrRefused) {
		t.Fatalf("complete with ambiguous payout = %v, want ErrRefused", err)
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "return_requests_completed_money_settled" {
		t.Fatalf("incomplete payout refused by %v, want return_requests_completed_money_settled", err)
	}

	healthy := storeOver(pool, admintest.Refunder{})
	if err := healthy.Decide(ctx, requestID.String(), "approved", "完成退款", "", actor); err != nil {
		t.Fatalf("retry approved payout and clawback: %v", err)
	}
	if err := healthy.Complete(ctx, requestID.String(), "已退款並驗貨", actor); err != nil {
		t.Fatalf("complete exactly settled return: %v", err)
	}

	var status string
	var succeededRefunds, clawbacks int
	if err := pool.QueryRow(ctx, `
		SELECT r.status,
		       (SELECT count(*) FROM refunds rf
		        WHERE rf.return_request_id = r.id AND rf.status = 'succeeded'),
		       (SELECT count(*) FROM loyalty_entries e
		        WHERE e.return_request_id = r.id AND e.kind = 'clawback')
		FROM return_requests r WHERE r.id = $1`, requestID).
		Scan(&status, &succeededRefunds, &clawbacks); err != nil {
		t.Fatalf("read completed return settlement: %v", err)
	}
	if status != "completed" || succeededRefunds != 1 || clawbacks != 1 {
		t.Errorf("completed settlement status/refunds/clawbacks = %s/%d/%d, want completed/1/1",
			status, succeededRefunds, clawbacks)
	}
}

func TestAStalledRefundOffersItsRetryInTheQueue(t *testing.T) {
	ctx := t.Context()
	isolated := dbtest.Pool(t)
	seed, err := os.ReadFile("../../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatalf("read isolated stalled-refund seed: %v", err)
	}
	if _, execErr := isolated.Exec(ctx, string(seed)); execErr != nil {
		t.Fatalf("load isolated stalled-refund seed: %v", execErr)
	}

	for _, tc := range []struct {
		name            string
		refunder        admintest.Refunder
		wantOutstanding bool
		wantBlocked     bool
	}{
		{
			name: "an ambiguous transport failure remains retryable",
			refunder: admintest.Refunder{
				RefundErr: errors.New("read tcp 1.2.3.4:443: i/o timeout"),
			},
			wantOutstanding: true,
		},
		{
			name:            "a provider refusal offers a successor",
			refunder:        admintest.Refunder{State: refundstate.Failed},
			wantOutstanding: true,
		},
		{
			name:     "a settled payout offers nothing twice",
			refunder: admintest.Refunder{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			staffCtx, _ := admintest.StaffContext(t, isolated)
			requestID, _ := admintest.ReturnedOrder(t, isolated, 1)
			s := storeOver(isolated, tc.refunder)
			_ = s.Decide(staffCtx, requestID.String(), "approved", "退款", "", uuid.NullUUID{})

			view, err := s.Queue(staffCtx)
			if err != nil {
				t.Fatalf("read queue: %v", err)
			}
			var found *adminpages.Return
			for i := range view.Rows {
				if view.Rows[i].ID == requestID.String() {
					found = &view.Rows[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("return %s is absent from queue", requestID)
			}
			if !found.Decided {
				t.Error("the committed approval is rendered as undecided")
			}
			if found.PayoutOutstanding != tc.wantOutstanding || found.PayoutBlocked != tc.wantBlocked {
				t.Errorf("payout flags = outstanding %v blocked %v, want %v/%v",
					found.PayoutOutstanding, found.PayoutBlocked,
					tc.wantOutstanding, tc.wantBlocked)
			}
		})
	}

	t.Run("a split payout offers the half that has not landed", func(t *testing.T) {
		staffCtx, _ := admintest.StaffContext(t, isolated)
		requestID, orderNumber, _ := admintest.CreditFundedReturn(t, isolated, 2, 60000)
		if _, err := isolated.Exec(staffCtx, `
			UPDATE return_requests SET status = 'approved', decided_at = now(), resolution = '退款'
			WHERE id = $1`, requestID); err != nil {
			t.Fatalf("approve split return: %v", err)
		}
		if _, err := isolated.Exec(staffCtx, `
			INSERT INTO refunds (payment_id, request_key, amount_cents, reason,
			                     return_request_id, status, provider_ref, succeeded_at)
			SELECT p.id, 'return:' || $1::text, 140000, '退款', $1::uuid,
			       'succeeded', 're_queue_split', now()
			FROM payments p JOIN orders o ON o.id = p.order_id
			WHERE o.order_number = $2 AND p.status = 'succeeded'`, requestID, orderNumber); err != nil {
			t.Fatalf("construct settled card half: %v", err)
		}
		s := storeOver(isolated, admintest.Refunder{})
		view, err := s.Queue(staffCtx)
		if err != nil {
			t.Fatalf("read split queue: %v", err)
		}
		for i := range view.Rows {
			if view.Rows[i].ID == requestID.String() {
				if !view.Rows[i].PayoutOutstanding || view.Rows[i].PayoutBlocked {
					t.Errorf("split payout flags = outstanding %v blocked %v, want true/false",
						view.Rows[i].PayoutOutstanding, view.Rows[i].PayoutBlocked)
				}
				return
			}
		}
		t.Fatalf("split return %s is absent from queue", requestID)
	})
}

// TestAnOldRecoverySurvivesTheBoundedReturnQueue proves ordering happens before
// LIMIT. The returns page is the only retry door; if fifty newer intake rows can
// hide an approved-but-unpaid return, that customer can remain unpaid forever.
func TestAnOldRecoverySurvivesTheBoundedReturnQueue(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	requestID, _ := admintest.ReturnedOrder(t, pool, 1)
	s := storeOver(pool, admintest.Refunder{State: refundstate.Failed})
	decideErr := s.Decide(ctx, requestID.String(), "approved", "terminal retry", "", uuid.NullUUID{})
	if !errors.Is(decideErr, refundstate.ErrIncomplete) || errors.Is(decideErr, returns.ErrRefused) || errors.Is(decideErr, refundstate.ErrRefused) {
		t.Fatalf("terminal decision = %v, want only refundstate.ErrIncomplete", decideErr)
	}

	rows, err := pool.Query(ctx, `
		WITH method AS (
			SELECT v.id, sm.code, v.name
			FROM shipping_method_versions v
			JOIN shipping_methods sm ON sm.id = v.method_id
			ORDER BY v.effective_at
			LIMIT 1
		), crowded_orders AS (
			INSERT INTO orders (
				shipping_version_id, shipping_method_code, shipping_method_name
			)
			SELECT m.id, m.code, m.name
			FROM method m CROSS JOIN generate_series(1, $1::integer)
			RETURNING id
		), crowded_lines AS (
			INSERT INTO order_lines
				(order_id, sku, product_name, unit_price_cents, quantity)
			SELECT id, 'QUEUE-' || id::text, 'queue fixture', 10000, 1
			FROM crowded_orders
			RETURNING order_id
		), crowded_private_data AS (
			INSERT INTO order_private_data
				(order_id, email, recipient_name, phone, postal_code, city, district, street)
			SELECT id, 'queue+' || id::text || '@example.invalid', 'queue fixture',
			       '0912345678', '110', '台北市', '信義區', '測試路 1 號'
			FROM crowded_orders
			RETURNING order_id
		), crowded_returns AS (
			INSERT INTO return_requests (order_id, reason)
			SELECT l.order_id, 'newer queue intake'
			FROM crowded_lines l
			JOIN crowded_private_data p USING (order_id)
			RETURNING order_id
		)
		SELECT order_id FROM crowded_returns`, web.PageSize+5)
	if err != nil {
		t.Fatalf("create bounded-queue crowd: %v", err)
	}
	var crowdedOrders []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if scanErr := rows.Scan(&id); scanErr != nil {
			rows.Close()
			t.Fatalf("scan crowded order: %v", scanErr)
		}
		crowdedOrders = append(crowdedOrders, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		t.Fatalf("iterate crowded orders: %v", rowsErr)
	}
	rows.Close()
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		for _, query := range []string{
			`DELETE FROM return_requests WHERE order_id = ANY($1::uuid[])`,
			`DELETE FROM order_private_data WHERE order_id = ANY($1::uuid[])`,
			`DELETE FROM order_lines WHERE order_id = ANY($1::uuid[])`,
			`DELETE FROM orders WHERE id = ANY($1::uuid[])`,
		} {
			if _, cleanupErr := pool.Exec(cleanupCtx, query, crowdedOrders); cleanupErr != nil {
				t.Errorf("remove bounded-queue crowd: %v", cleanupErr)
			}
		}
	})

	queue, err := s.Queue(ctx)
	if err != nil {
		t.Fatalf("read bounded return queue: %v", err)
	}
	if len(queue.Rows) != web.PageSize {
		t.Fatalf("bounded queue has %d rows, want %d", len(queue.Rows), web.PageSize)
	}
	for i := range queue.Rows {
		if queue.Rows[i].ID != requestID.String() {
			continue
		}
		if !queue.Rows[i].CanRetryPayout() {
			t.Fatalf("old recovery row at position %d has outstanding/blocked %v/%v, want a retry",
				i, queue.Rows[i].PayoutOutstanding, queue.Rows[i].PayoutBlocked)
		}
		return
	}
	t.Fatalf("approved recovery %s was hidden behind %d newer intake rows",
		requestID, len(crowdedOrders))
}

func TestTerminalRefundHTTPUsesThePayoutRecoveryNotice(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	requestID, _ := admintest.ReturnedOrder(t, pool, 1)
	h := handlerOver(storeOver(pool, admintest.Refunder{State: refundstate.Cancelled}))
	form := url.Values{
		"decision":   {"approved"},
		"confirm":    {"approved"},
		"resolution": {"provider cancelled"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/returns/"+requestID.String()+"/decide", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", requestID.String())
	res := httptest.NewRecorder()

	h.Decide(res, req)
	if res.Code != http.StatusSeeOther ||
		res.Header().Get("Location") != "/admin/returns?refundfailed=1" {
		t.Fatalf("terminal refund HTTP = %d %q, want payout-recovery redirect",
			res.Code, res.Header().Get("Location"))
	}
}

func TestReturnResolutionOverTheDurableBoundIsRefusedBeforeDecision(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	requestID, _ := admintest.ReturnedOrder(t, pool, 1)
	s := storeOver(pool, admintest.Refunder{})
	tooLong := strings.Repeat("界", 301)
	if err := s.Decide(ctx, requestID.String(), "approved", tooLong, "", uuid.NullUUID{}); !errors.Is(err, returns.ErrInvalid) {
		t.Fatalf("overlong Store resolution = %v, want ErrInvalid", err)
	}

	form := url.Values{"decision": {"approved"}, "confirm": {"approved"}, "resolution": {tooLong}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/returns/"+requestID.String()+"/decide", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", requestID.String())
	res := httptest.NewRecorder()
	handlerOver(s).Decide(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/returns?refused=1" {
		t.Fatalf("overlong resolution HTTP = %d %q, want refused redirect",
			res.Code, res.Header().Get("Location"))
	}

	var status string
	var attempts int
	if err := pool.QueryRow(ctx, `
		SELECT r.status,
		       (SELECT count(*) FROM refunds rf WHERE rf.return_request_id = r.id)
		FROM return_requests r WHERE r.id = $1`, requestID).Scan(&status, &attempts); err != nil {
		t.Fatalf("read return after overlong resolution: %v", err)
	}
	if status != "requested" || attempts != 0 {
		t.Errorf("overlong resolution left status/attempts = %s/%d, want requested/0", status, attempts)
	}
}

func TestAPendingProviderRefundIsNotRecordedAsSucceeded(t *testing.T) {
	tests := []struct {
		name  string
		state refundstate.State
	}{
		{name: "stripe accepted it and has not settled it", state: refundstate.Pending},
		{name: "stripe needs something else to happen first", state: refundstate.RequiresAction},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := admintest.StaffContext(t, pool)
			s := storeOver(pool, admintest.Refunder{State: tt.state})
			requestID, _ := admintest.ReturnedOrder(t, pool, 1)

			if err := s.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err != nil {
				t.Fatalf("a refund Stripe ACCEPTED was treated as a failure: %v", err)
			}

			var status string
			var succeededAt, failedAt *time.Time
			var providerRef *string
			if err := pool.QueryRow(ctx, `
				SELECT status, succeeded_at, failed_at, provider_ref FROM refunds
				WHERE return_request_id = $1`, requestID).
				Scan(&status, &succeededAt, &failedAt, &providerRef); err != nil {
				t.Fatalf("read refund: %v", err)
			}
			if status != string(tt.state) {
				t.Errorf("refund status is %q, want %q — goen recorded a state the "+
					"provider never claimed", status, tt.state)
			}
			if succeededAt != nil {
				t.Errorf("succeeded_at is %v on a refund that has not succeeded", *succeededAt)
			}
			if failedAt != nil {
				t.Errorf("failed_at is %v on a refund that has not failed", *failedAt)
			}
			if providerRef == nil || *providerRef == "" {
				t.Error("no provider reference on a refund Stripe accepted — it is " +
					"the only handle anybody has for chasing it")
			}

			var returnStatus string
			if err := pool.QueryRow(ctx,
				`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&returnStatus); err != nil {
				t.Fatalf("read return: %v", err)
			}
			if returnStatus != "approved" {
				t.Errorf("return is %q, want approved — the shop accepted the goods back", returnStatus)
			}
			var refundEvents int
			if err := pool.QueryRow(ctx, `
				SELECT count(*) FROM order_events e
				JOIN return_requests r ON r.order_id = e.order_id
				WHERE r.id = $1 AND e.kind = 'refunded'`, requestID).Scan(&refundEvents); err != nil {
				t.Fatalf("count events: %v", err)
			}
			if refundEvents != 0 {
				t.Errorf("%d 'refunded' events on an order whose refund has not landed — "+
					"the customer reads that timeline", refundEvents)
			}
		})
	}
}

func TestRejectingAReturnMovesNoMoney(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	requestID, _ := admintest.ReturnedOrder(t, pool, 2)

	if err := s.Decide(ctx, requestID.String(), "rejected", "超過鑑賞期", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("reject: %v", err)
	}

	var refundRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refunds WHERE return_request_id = $1`, requestID).Scan(&refundRows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if refundRows != 0 {
		t.Errorf("%d refunds written for a REJECTED return", refundRows)
	}

	var status, resolution string
	if err := pool.QueryRow(ctx,
		`SELECT status, coalesce(resolution, '') FROM return_requests WHERE id = $1`,
		requestID).Scan(&status, &resolution); err != nil {
		t.Fatalf("read return: %v", err)
	}
	if status != "rejected" || resolution != "超過鑑賞期" {
		t.Errorf("return is %q/%q, want rejected and the reason the staff gave", status, resolution)
	}
}

func TestAReturnIsDecidedOnce(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	requestID, _ := admintest.ReturnedOrder(t, pool, 1)

	if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); !errors.Is(err, returns.ErrRefused) {
		t.Errorf("second decision gave %v, want ErrRefused", err)
	}

	var refundRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refunds WHERE return_request_id = $1`, requestID).Scan(&refundRows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if refundRows != 1 {
		t.Errorf("%d refunds after two approvals, want 1", refundRows)
	}
}

func TestTheLoserOfTwoSimultaneousDecisionsWritesNoAuditRow(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	requestID, _ := admintest.ReturnedOrder(t, pool, 1)

	before := auditRowsFor(t, requestID)

	t1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin T1: %v", err)
	}
	defer func() { _ = t1.Rollback(ctx) }()
	if _, err := t1.Exec(ctx, `
		UPDATE return_requests SET status = 'rejected', decided_at = now()
		WHERE id = $1 AND status = 'requested'`, requestID); err != nil {
		t.Fatalf("T1 decide: %v", err)
	}

	decided := make(chan error, 1)
	go func() { decided <- s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}) }()

	select {
	case err := <-decided:
		t.Fatalf("T2 finished before T1 committed (%v); it never met the lock, "+
			"so this run proves nothing", err)
	case <-time.After(250 * time.Millisecond):
	}

	if err := t1.Commit(ctx); err != nil {
		t.Fatalf("commit T1: %v", err)
	}

	if err := <-decided; !errors.Is(err, returns.ErrRefused) {
		t.Errorf("the second decision returned %v, want ErrRefused — it updated no "+
			"row and reported success", err)
	}

	if after := auditRowsFor(t, requestID); after != before {
		t.Errorf("%d audit rows for this return, was %d — the losing decision was "+
			"recorded as though it had been made", after, before)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "rejected" {
		t.Errorf("the return is %q, want rejected — the loser overwrote the winner", status)
	}

	// THE MONEY. Every assertion above stays true even when the loser refunds
	// before losing the CAS: a count proves the database held the line, and only
	// this says whether a cent moved.
	var refundRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refunds WHERE return_request_id = $1`, requestID).Scan(&refundRows); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	if refundRows != 0 {
		t.Errorf("%d refunds against a return that was REJECTED — the losing "+
			"decision paid before it found out it had lost", refundRows)
	}
}

func auditRowsFor(t *testing.T, requestID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM audit_events
		WHERE entity_table = 'return_requests' AND entity_id = $1`, requestID).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

func TestARefundCannotExceedWhatWasCaptured(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	requestID, orderNumber := admintest.ReturnedOrder(t, pool, 2)

	if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("first approval: %v", err)
	}

	// Written straight in: the return ceiling would refuse a second request before the refund guard.
	var orderID, lineID, second uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT o.id, ol.id FROM orders o JOIN order_lines ol ON ol.order_id = o.id
		WHERE o.order_number = $1`, orderNumber).Scan(&orderID, &lineID); err != nil {
		t.Fatalf("find order: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason) VALUES ($1, '再退一次') RETURNING id`,
		orderID).Scan(&second); err != nil {
		t.Fatalf("create second return: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 2)`, orderID, second, lineID); err == nil {
		if decideErr := s.Decide(ctx, second.String(), "approved", "", "", uuid.NullUUID{}); decideErr == nil {
			t.Fatal("the same order was refunded twice")
		}
	}

	var total int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(r.amount_cents), 0) FROM refunds r
		JOIN payments p ON p.id = r.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND r.status IN ('pending', 'succeeded')`,
		orderNumber).Scan(&total); err != nil {
		t.Fatalf("sum refunds: %v", err)
	}
	if total > 200000 {
		t.Errorf("%d refunded against a capture of 200000", total)
	}
}

// TestAnOverClaimIsRefusedInWordsRatherThanByAConstraint goes through a goodwill refund:
// a second return meets return_within_shipment first and never reaches this guard.
func TestAnOverClaimIsRefusedInWordsRatherThanByAConstraint(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	requestID, orderNumber := admintest.ReturnedOrder(t, pool, 2)

	var paymentID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT p.id FROM payments p JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND p.status = 'succeeded'`,
		orderNumber).Scan(&paymentID); err != nil {
		t.Fatalf("find payment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds
		    (payment_id, request_key, amount_cents, reason, status, provider_ref, succeeded_at)
		VALUES ($1,$2,150000,'善意退款','succeeded',$3,now())`,
		paymentID, "goodwill:"+orderNumber, "re_goodwill_"+requestID.String()); err != nil {
		t.Fatalf("post the goodwill refund: %v", err)
	}

	err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{})
	if err == nil {
		t.Fatal("a return claiming more than remains was approved")
	}
	if !errors.Is(err, returns.ErrRefused) {
		t.Fatalf("refused with %v, want ErrRefused", err)
	}
	// Figures only: matching the words would bind this to a message that is translated.
	for _, want := range []string{"200000", "150000", "50000"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "refunds_within_capture") {
		t.Errorf("the constraint reached the staff member: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read the return: %v", err)
	}
	if status != "requested" {
		t.Errorf("a refused approval left the return %s", status)
	}
}

func TestTheReturnQueueShowsWhatIsComingBack(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	requestID, _ := admintest.ReturnedOrder(t, pool, 2)

	view, err := s.Queue(ctx)
	if err != nil {
		t.Fatalf("read the queue: %v", err)
	}

	var found *adminpages.Return
	for i := range view.Rows {
		if view.Rows[i].ID == requestID.String() {
			found = &view.Rows[i]
		}
	}
	if found == nil {
		t.Fatalf("the return %s is not in the queue", requestID)
	}
	if len(found.Lines) == 0 {
		t.Fatal("the queue row carries no lines — the decision is still blind")
	}
	line := found.Lines[0]
	if line.SKU == "" || line.Name == "" || line.Quantity != 2 {
		t.Errorf("the line is %+v, want a sku, a name and 2 units", line)
	}
}

func TestTheReturnQueueNamesTheRefundChannels(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})

	tests := []struct {
		name   string
		setup  func(t *testing.T) uuid.UUID
		card   int64
		credit int64
		want   string
		not    string
	}{
		{
			name: "credit-only",
			setup: func(t *testing.T) uuid.UUID {
				t.Helper()
				id, _, _ := admintest.CreditFundedReturn(t, pool, 2, 200000)
				return id
			},
			credit: 200000,
			want:   "店儲 NT$2,000 退回額度",
			not:    "走 Stripe",
		},
		{
			name: "card and credit split",
			setup: func(t *testing.T) uuid.UUID {
				t.Helper()
				id, _, _ := admintest.CreditFundedReturn(t, pool, 2, 60000)
				return id
			},
			card:   140000,
			credit: 60000,
			want:   "卡款 NT$1,400 走 Stripe，店儲 NT$600 退回額度",
		},
		{
			name: "card-only",
			setup: func(t *testing.T) uuid.UUID {
				t.Helper()
				id, _ := admintest.ReturnedOrder(t, pool, 2)
				return id
			},
			card: 200000,
			want: "卡款 NT$2,000 走 Stripe",
			not:  "額度",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestID := tt.setup(t)
			if err := s.Decide(ctx, requestID.String(), "approved", "核准", "", uuid.NullUUID{}); err != nil {
				t.Fatalf("Decide: %v", err)
			}
			view, err := s.Queue(ctx)
			if err != nil {
				t.Fatalf("Returns: %v", err)
			}
			var found *adminpages.Return
			for i := range view.Rows {
				if view.Rows[i].ID == requestID.String() {
					found = &view.Rows[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("return %s is not in the queue", requestID)
			}
			if found.CardRefundCents != tt.card || found.CreditRefundCents != tt.credit {
				t.Errorf("frozen split card/credit = %d/%d, want %d/%d",
					found.CardRefundCents, found.CreditRefundCents, tt.card, tt.credit)
			}
			zh := i18n.WithLocale(ctx, i18n.ZhHant)
			got := found.PayoutChannel(zh)
			if got != tt.want {
				t.Errorf("PayoutChannel(zh-Hant) = %q, want %q", got, tt.want)
			}
			if tt.not != "" && strings.Contains(got, tt.not) {
				t.Errorf("PayoutChannel(zh-Hant) = %q, must not mention %q", got, tt.not)
			}
			en := found.PayoutChannel(i18n.WithLocale(ctx, i18n.En))
			if !strings.Contains(strings.ToLower(en), "stripe") && tt.card > 0 {
				t.Errorf("PayoutChannel(en) = %q, want the card half named", en)
			}
			if !strings.Contains(strings.ToLower(en), "store credit") && tt.credit > 0 {
				t.Errorf("PayoutChannel(en) = %q, want the credit half named", en)
			}
		})
	}
}

func TestTheReturnQueueUsesAConstantQueryCountForAnyNumberOfApprovedRows(t *testing.T) {
	ctx := t.Context()
	isolated := dbtest.Pool(t)
	seed, err := os.ReadFile("../../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatalf("read isolated return queue seed: %v", err)
	}
	if _, execErr := isolated.Exec(ctx, string(seed)); execErr != nil {
		t.Fatalf("load isolated return queue seed: %v", execErr)
	}
	first, _ := admintest.ReturnedOrder(t, isolated, 1)
	second, _ := admintest.ReturnedOrder(t, isolated, 2)

	var queries atomic.Int64
	var queryNames []string
	var queryNamesMu sync.Mutex
	config := isolated.Config()
	config.ConnConfig.Tracer = returnQueryTracer{
		queries: &queries,
		mu:      &queryNamesMu,
		names:   &queryNames,
	}
	traced, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open traced admin pool: %v", err)
	}
	t.Cleanup(traced.Close)

	if _, execErr := traced.Exec(ctx, `
		UPDATE return_requests
		SET status = 'approved', decided_at = now(), created_at = now() + interval '100 years'
		WHERE id = ANY($1::uuid[])`, []uuid.UUID{first, second}); execErr != nil {
		t.Fatalf("approve the return queue fixtures: %v", execErr)
	}

	counted := context.WithValue(ctx, returnQueryCountKey{}, struct{}{})
	view, err := storeOver(traced, admintest.Refunder{}).Queue(counted)
	if err != nil {
		t.Fatalf("read the traced return queue: %v", err)
	}
	found := map[string]bool{first.String(): false, second.String(): false}
	for i := range view.Rows {
		if _, ok := found[view.Rows[i].ID]; ok {
			found[view.Rows[i].ID] = true
		}
	}
	for id, ok := range found {
		if !ok {
			t.Errorf("approved return %s is absent from the traced queue", id)
		}
	}
	got := queries.Load()
	if got != 4 {
		t.Errorf("Returns() made %d queries, want 4 (queue, lines, payout facts, assessments)", got)
	}
	queryNamesMu.Lock()
	gotNames := slices.Clone(queryNames)
	queryNamesMu.Unlock()
	// Sorted, because the property is which queries run and how often, not their
	// order; a query repeated per row is still an extra name.
	slices.Sort(gotNames)
	wantNames := []string{
		"-- name: LatestEligibilityAssessments :many",
		"-- name: ReturnLines :many",
		"-- name: ReturnPayoutFacts :many",
		"-- name: ReturnQueue :many",
	}
	if diff := cmp.Diff(wantNames, gotNames); diff != "" {
		t.Errorf("Returns() query names mismatch (-want +got):\n%s", diff)
	}
	t.Logf("Returns() query count = %d (queue, lines, payout facts, assessments)", got)
}

func TestTheRescissionWindowIsCountedOnTheShopsCalendar(t *testing.T) {
	isolated := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, isolated)
	s := storeOver(isolated, admintest.Refunder{})

	cases := []struct {
		name           string
		delivered      string
		requested      string
		wantWindow     string
		wantRescission bool
	}{
		{
			name:           "a parcel handed over in the Taipei morning",
			delivered:      "2026-08-25T07:00:00+08:00",
			requested:      "2026-09-01T12:00:00+08:00",
			wantWindow:     "within",
			wantRescission: true,
		},
		{
			name:           "a request made in the Taipei small hours one day late",
			delivered:      "2026-08-25T12:00:00+08:00",
			requested:      "2026-09-02T06:00:00+08:00",
			wantWindow:     "goodwill",
			wantRescission: false,
		},
		{
			name:           "the last shop day of the advertised fourteen",
			delivered:      "2026-08-25T07:00:00+08:00",
			requested:      "2026-09-08T12:00:00+08:00",
			wantWindow:     "goodwill",
			wantRescission: false,
		},
		{
			name:           "a request in the Taipei small hours one day past fourteen",
			delivered:      "2026-08-25T12:00:00+08:00",
			requested:      "2026-09-09T06:00:00+08:00",
			wantWindow:     "after",
			wantRescission: false,
		},
		{
			name:           "a January filing still statutory when read in September",
			delivered:      "2026-01-01T07:00:00+08:00",
			requested:      "2026-01-06T12:00:00+08:00",
			wantWindow:     "within",
			wantRescission: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// These hours are the lock: 07:00 Taipei is 23:00 UTC on the
			// previous day, and 06:00 Taipei is 22:00 UTC on the previous
			// day. Moving either into the middle of the day makes shop_day(x)
			// equal x::date and silently makes this test agree with the defect.
			delivered, err := time.Parse(time.RFC3339, tc.delivered)
			if err != nil {
				t.Fatalf("parse delivered_at: %v", err)
			}
			requested, err := time.Parse(time.RFC3339, tc.requested)
			if err != nil {
				t.Fatalf("parse requested_at: %v", err)
			}
			requestID := returnedOrderAtOn(t, isolated, delivered, requested)

			view, err := s.Queue(ctx)
			if err != nil {
				t.Fatalf("read the queue: %v", err)
			}
			var found *adminpages.Return
			for i := range view.Rows {
				if view.Rows[i].ID == requestID.String() {
					found = &view.Rows[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("the return %s is not in the queue", requestID)
			}
			if found.Window != tc.wantWindow {
				t.Errorf("window is %q, want %q", found.Window, tc.wantWindow)
			}
			if got := found.Rescission(); got != tc.wantRescission {
				t.Errorf("Rescission() is %t, want %t", got, tc.wantRescission)
			}
		})
	}
}

func TestAReturnPaysBackBothSources(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	requestID, orderNumber, accountID := admintest.CreditFundedReturn(t, pool, 2, 60000)

	if err := s.Decide(ctx, requestID.String(), "approved", "退貨完成", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	var refunded int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(r.amount_cents), 0) FROM refunds r
		JOIN payments p ON p.id = r.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND r.status = 'succeeded'`,
		orderNumber).Scan(&refunded); err != nil {
		t.Fatalf("read refunds: %v", err)
	}
	if refunded != 140000 {
		t.Errorf("card refunded %d, want 140000 — the whole capture, card first", refunded)
	}

	var compensated int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(e.amount_cents), 0) FROM store_credit_entries e
		JOIN orders o ON o.id = e.order_id
		WHERE o.order_number = $1 AND e.amount_cents > 0 AND e.reverses_id IS NULL`,
		orderNumber).Scan(&compensated); err != nil {
		t.Fatalf("read compensation: %v", err)
	}
	if compensated != 60000 {
		t.Errorf("credit compensated %d, want 60000", compensated)
	}
	if got := creditBalanceOf(t, accountID); got != 60000 {
		t.Errorf("balance = %d, want 60000 — the credit they spent came back", got)
	}
}

func TestAPartialReturnPaysTheCardFirst(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	requestID, orderNumber, accountID := admintest.CreditFundedReturn(t, pool, 1, 60000)

	if err := s.Decide(ctx, requestID.String(), "approved", "退一件", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	var refunded int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(r.amount_cents), 0) FROM refunds r
		JOIN payments p ON p.id = r.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND r.status = 'succeeded'`,
		orderNumber).Scan(&refunded); err != nil {
		t.Fatalf("read refunds: %v", err)
	}
	if refunded != 100000 {
		t.Errorf("card refunded %d, want the whole claim of 100000 — card first", refunded)
	}
	if got := creditBalanceOf(t, accountID); got != 0 {
		t.Errorf("balance = %d, want 0 — the card paid the whole claim, so none of "+
			"the credit was needed", got)
	}
}

func TestAWhollyCreditFundedReturnNeedsNoProvider(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	requestID, orderNumber, accountID := admintest.CreditFundedReturn(t, pool, 2, 200000)

	if decideErr := s.Decide(ctx, requestID.String(), "approved", "全額購物金", "", uuid.NullUUID{}); decideErr != nil {
		t.Fatalf("Decide: %v", decideErr)
	}

	var refundRows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM refunds r
		JOIN payments p ON p.id = r.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1`, orderNumber).Scan(&refundRows); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	if refundRows != 0 {
		t.Errorf("%d refund rows for an order with no card payment, want 0", refundRows)
	}
	if got := creditBalanceOf(t, accountID); got != 200000 {
		t.Errorf("balance = %d, want 200000 — every cent came back as credit", got)
	}
}

func TestACreditOnlyRefundIsOnTheCustomersTimeline(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	requestID, _, _ := admintest.CreditFundedReturn(t, pool, 2, 200000)

	if err := s.Decide(ctx, requestID.String(), "approved", "全額購物金", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	var events int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events e
		WHERE e.return_request_id = $1 AND e.kind = 'refunded'`, requestID).Scan(&events); err != nil {
		t.Fatalf("count timeline events: %v", err)
	}
	if events != 1 {
		t.Errorf("%d refunded events after a credit-only refund, want 1 — the customer "+
			"has no per-entry credit history, so without this their timeline says no money moved", events)
	}

	var entries int
	var credited int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*), coalesce(sum(amount_cents), 0)::bigint
		FROM store_credit_entries
		WHERE idempotency_key = 'return-credit:' || $1::text`, requestID).
		Scan(&entries, &credited); err != nil {
		t.Fatalf("read returned credit: %v", err)
	}
	if entries != 1 || credited != 200000 {
		t.Errorf("returned credit is %d row(s) totalling %d, want one row of 200000", entries, credited)
	}
}

// TestAMissingRefundTimelineEventIsRecoveredAfterMoneyCommits injects the
// boundary where the credit ledger commits but the separately appended customer
// timeline fails. The approved return must keep offering useful work, recreate
// exactly one event without paying twice, and only then become completable.
func TestAMissingRefundTimelineEventIsRecoveredAfterMoneyCommits(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, _, accountID := admintest.CreditFundedReturn(t, pool, 2, 200000)

	const trigger = "test_fail_return_refunded_event"
	const function = "test_fail_return_refunded_event_fn"
	dropFailure := func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `
			DROP TRIGGER IF EXISTS test_fail_return_refunded_event ON order_events;
			DROP FUNCTION IF EXISTS test_fail_return_refunded_event_fn();`)
	}
	dropFailure()
	t.Cleanup(dropFailure)
	install := fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'injected return event failure';
		END;
		$$;
		CREATE TRIGGER %s
		BEFORE INSERT ON order_events
		FOR EACH ROW
		WHEN (NEW.return_request_id = '%s'::uuid)
		EXECUTE FUNCTION %s();`, function, trigger, requestID, function)
	if _, err := pool.Exec(ctx, install); err != nil {
		t.Fatalf("install event failure: %v", err)
	}

	s := storeOver(pool, admintest.Refunder{})
	if err := s.Decide(ctx, requestID.String(), "approved", "全額購物金", "", uuid.NullUUID{}); err == nil {
		t.Fatal("injected timeline failure was reported as a complete payout")
	}
	if got := creditBalanceOf(t, accountID); got != 200000 {
		t.Fatalf("credit after event failure = %d, want the committed 200000", got)
	}
	var events int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events WHERE return_request_id = $1`, requestID).
		Scan(&events); err != nil {
		t.Fatalf("count failed timeline append: %v", err)
	}
	if events != 0 {
		t.Fatalf("failed timeline append left %d events, want 0", events)
	}
	view, err := s.Queue(ctx)
	if err != nil {
		t.Fatalf("read event-recovery queue: %v", err)
	}
	found := false
	for i := range view.Rows {
		if view.Rows[i].ID != requestID.String() {
			continue
		}
		found = true
		if !view.Rows[i].PayoutOutstanding || view.Rows[i].PayoutBlocked {
			t.Fatalf("missing event renders outstanding=%v blocked=%v, want true/false",
				view.Rows[i].PayoutOutstanding, view.Rows[i].PayoutBlocked)
		}
	}
	if !found {
		t.Fatalf("return %s is absent from the event-recovery queue", requestID)
	}

	dropFailure()
	if err := s.Decide(ctx, requestID.String(), "approved", "補登退款事件", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("retry missing refunded event: %v", err)
	}
	if got := creditBalanceOf(t, accountID); got != 200000 {
		t.Errorf("credit after event retry = %d, want no duplicate posting", got)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events WHERE return_request_id = $1`, requestID).
		Scan(&events); err != nil {
		t.Fatalf("count repaired timeline append: %v", err)
	}
	if events != 1 {
		t.Errorf("event retry left %d refunded events, want exactly 1", events)
	}
	if err := s.Decide(ctx, requestID.String(), "approved", "重複補登", "", uuid.NullUUID{}); !errors.Is(err, returns.ErrRefused) {
		t.Fatalf("second event retry = %v, want an already-settled refusal", err)
	}

	if err := s.Inspect(ctx, requestID.String(), []returns.LineInspection{{
		OrderLineID: returnLineID(t, requestID), Received: 2, Restocked: 0,
	}}, actor); err != nil {
		t.Fatalf("inspect event-repaired return: %v", err)
	}
	if err := s.Complete(ctx, requestID.String(), "退款事件已補登", actor); err != nil {
		t.Fatalf("complete event-repaired return: %v", err)
	}
}

func TestASplitReturnStillPostsCreditWhenTheCardAttemptTerminates(t *testing.T) {
	tests := []struct {
		name     string
		refunder admintest.Refunder
	}{
		{name: "provider object failed", refunder: admintest.Refunder{State: refundstate.Failed}},
		{name: "create API rejected", refunder: admintest.Refunder{RefundErr: fmt.Errorf(
			"%w: provider rejected create", refunds.ErrCreateRejected)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := admintest.StaffContext(t, pool)
			requestID, _, accountID := admintest.CreditFundedReturn(t, pool, 2, 60000)
			s := storeOver(pool, tt.refunder)

			err := s.Decide(ctx, requestID.String(), "approved", "split terminal", "", uuid.NullUUID{})
			if !errors.Is(err, refundstate.ErrIncomplete) || errors.Is(err, returns.ErrRefused) || errors.Is(err, refundstate.ErrRefused) {
				t.Fatalf("split terminal decision = %v, want only refundstate.ErrIncomplete", err)
			}
			if got := creditBalanceOf(t, accountID); got != 60000 {
				t.Errorf("credit after terminal card outcome = %d, want frozen 60000", got)
			}
			var refundStatus string
			var events int
			if queryErr := pool.QueryRow(ctx, `
				SELECT status FROM refunds WHERE return_request_id = $1`, requestID).
				Scan(&refundStatus); queryErr != nil {
				t.Fatalf("read terminal card attempt: %v", queryErr)
			}
			if refundStatus != "failed" {
				t.Errorf("card attempt = %q, want failed", refundStatus)
			}
			if queryErr := pool.QueryRow(ctx, `
				SELECT count(*) FROM order_events WHERE return_request_id = $1`, requestID).
				Scan(&events); queryErr != nil {
				t.Fatalf("count split terminal timeline event: %v", queryErr)
			}
			if events != 0 {
				t.Errorf("split terminal timeline events = %d, want 0 — refunded is the "+
					"completed-tense word and the card half has not settled", events)
			}

			queue, err := s.Queue(ctx)
			if err != nil {
				t.Fatalf("read split terminal queue: %v", err)
			}
			for i := range queue.Rows {
				if queue.Rows[i].ID == requestID.String() {
					if !queue.Rows[i].CanRetryPayout() {
						t.Fatalf("split terminal row has outstanding/blocked %v/%v, want card retry",
							queue.Rows[i].PayoutOutstanding, queue.Rows[i].PayoutBlocked)
					}
					return
				}
			}
			t.Fatalf("split terminal return %s absent from recovery queue", requestID)
		})
	}
}

func TestASplitRefundWhoseCardIsPendingStillRecordsTheCreditThatLanded(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{State: refundstate.Pending})
	requestID, _, _ := admintest.CreditFundedReturn(t, pool, 2, 60000)

	if err := s.Decide(ctx, requestID.String(), "approved", "分拆退款", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	var refundState string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM refunds WHERE return_request_id = $1`, requestID).
		Scan(&refundState); err != nil {
		t.Fatalf("read card refund: %v", err)
	}
	if refundState != "pending" {
		t.Fatalf("card refund is %q, want pending — the fixture does not distinguish accepted from moved", refundState)
	}

	var credited int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(amount_cents), 0)::bigint FROM store_credit_entries
		WHERE idempotency_key = 'return-credit:' || $1::text`, requestID).
		Scan(&credited); err != nil {
		t.Fatalf("read returned credit: %v", err)
	}
	if credited != 60000 {
		t.Fatalf("returned credit is %d, want 60000 — no money moved, so this proves nothing", credited)
	}

	var events int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events e
		WHERE e.return_request_id = $1 AND e.kind = 'refunded'`, requestID).Scan(&events); err != nil {
		t.Fatalf("count timeline events: %v", err)
	}
	if events != 0 {
		t.Errorf("%d refunded events after credit landed while the card stayed pending, want 0 — "+
			"refunded tells the customer the money is back", events)
	}

	if err := s.Decide(ctx, requestID.String(), "approved", "分拆退款", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("retry while the card is still pending: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events e
		WHERE e.return_request_id = $1 AND e.kind = 'refunded'`, requestID).Scan(&events); err != nil {
		t.Fatalf("count timeline events after retry: %v", err)
	}
	if events != 0 {
		t.Errorf("%d refunded events after retrying a still-pending card, want 0", events)
	}
}

func TestTheRefundFigureCountsCreditToo(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	figures := reports.NewStore(pool)

	before, err := figures.Report(ctx, 30)
	if err != nil {
		t.Fatalf("read report before refund: %v", err)
	}
	requestID, orderNumber, _ := admintest.CreditFundedReturn(t, pool, 2, 200000)
	if decideErr := s.Decide(ctx, requestID.String(), "approved", "全額購物金", "", uuid.NullUUID{}); decideErr != nil {
		t.Fatalf("Decide: %v", decideErr)
	}
	after, err := figures.Report(ctx, 30)
	if err != nil {
		t.Fatalf("read report after refund: %v", err)
	}

	var card, credit int64
	if err := pool.QueryRow(ctx, `
		SELECT card_cents, credit_cents FROM order_refunds
		WHERE order_number = $1`, orderNumber).Scan(&card, &credit); err != nil {
		t.Fatalf("read the one refund definition: %v", err)
	}
	if card != 0 || credit != 200000 {
		t.Fatalf("order_refunds reads card=%d credit=%d, want 0/200000 — the fixture must be credit-only", card, credit)
	}
	if delta, want := after.RefundedCents-before.RefundedCents, card+credit; delta != want {
		t.Errorf("the report moved by %d while order_refunds says %d went back "+
			"(card %d + credit %d); both surfaces must read one definition", delta, want, card, credit)
	}
}

func TestCompensatingAReturnTwiceGivesCreditOnce(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	requestID, _, accountID := admintest.CreditFundedReturn(t, pool, 2, 200000)

	if err := s.Decide(ctx, requestID.String(), "approved", "第一次", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("first Decide: %v", err)
	}
	// Retry the public compensation operation with the exact same authority.
	// Calling post_store_credit directly would now (correctly) be a different
	// attribution and must be rejected rather than mistaken for a replay.
	if _, err := pool.Exec(ctx, `
		SELECT compensate_return_with_credit($1, 200000, $2)`,
		requestID, actor); err != nil {
		t.Fatalf("second compensation: %v", err)
	}

	if got := creditBalanceOf(t, accountID); got != 200000 {
		t.Errorf("balance = %d after two compensations, want 200000 — the second must "+
			"have found the first by its key", got)
	}
}

func creditBalanceOf(t *testing.T, accountID uuid.UUID) int64 {
	t.Helper()
	var cents int64
	if err := pool.QueryRow(t.Context(),
		`SELECT coalesce(sum(amount_cents), 0) FROM store_credit_entries WHERE account_id = $1`,
		accountID).Scan(&cents); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return cents
}

// returnedOrderWithStock is returnedOrder with a REAL variant behind its line, and its
// own product per call: returning produces stock, so a shared row fails shuffled.
func returnedOrderWithStock(t *testing.T, name string, qty int32) (requestID, variantID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	slug := name + "-" + uuid.NewString()[:8]
	var productID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, $1, '退貨測試商品', 'active', now()
		FROM brands b, categories c WHERE b.slug = 'pixelight' AND c.slug = 'phones'
		RETURNING id`, slug).Scan(&productID); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, safety_stock, position)
		VALUES ($1, upper($2), 100000, 0, 1) RETURNING id`,
		productID, slug).Scan(&variantID); err != nil {
		t.Fatalf("create variant: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT record_inventory_movement($1, 5, 'receipt', $2, 'admin', NULL, NULL)`,
		variantID, "seed:"+slug); err != nil {
		t.Fatalf("stock the variant: %v", err)
	}

	var orderID, lineID uuid.UUID
	var orderNumber string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, $2, upper($3), '退貨測試商品', 100000, 2) RETURNING id`,
		orderID, variantID, slug).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'r@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 200000)`,
		orderID, "cs_rets_"+orderNumber); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 200000, NULL, NULL)`,
		"cs_rets_"+orderNumber); err != nil {
		t.Fatalf("capture: %v", err)
	}
	admintest.MoveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'black_cat', 'TS-'||$2) RETURNING id`,
		orderID, orderNumber).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 2)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason) VALUES ($1, '不合用') RETURNING id`,
		orderID).Scan(&requestID); err != nil {
		t.Fatalf("create return request: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, $4)`, orderID, requestID, lineID, qty); err != nil {
		t.Fatalf("create return line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return requestID, variantID
}

func stockOf(t *testing.T, variantID uuid.UUID) int32 {
	t.Helper()
	var n int32
	if err := pool.QueryRow(t.Context(),
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, variantID).Scan(&n); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return n
}

func returnLineID(t *testing.T, requestID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(),
		`SELECT order_line_id FROM return_request_lines WHERE return_request_id = $1`,
		requestID).Scan(&id); err != nil {
		t.Fatalf("read return line: %v", err)
	}
	return id
}

func TestAnInspectedReturnPutsTheSellableUnitsBack(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, variantID := returnedOrderWithStock(t, "restock", 2)
	lineID := returnLineID(t, requestID)

	before := stockOf(t, variantID)
	if err := s.Decide(ctx, requestID.String(), "approved", "", "", actor); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got := stockOf(t, variantID); got != before {
		t.Fatalf("approving a return moved stock %d -> %d; nothing has come back yet",
			before, got)
	}

	if err := s.Inspect(ctx, requestID.String(), []returns.LineInspection{{
		OrderLineID: lineID, Received: 2, Restocked: 1, Note: "一件外盒破損",
	}}, actor); err != nil {
		t.Fatalf("inspect: %v", err)
	}

	if got, want := stockOf(t, variantID), before+1; got != want {
		t.Errorf("stock is %d after restocking one of two returned units, want %d", got, want)
	}

	var reason, sourceType string
	var delta int32
	if err := pool.QueryRow(ctx, `
		SELECT reason, coalesce(source_type, ''), delta FROM inventory_movements
		WHERE variant_id = $1 ORDER BY created_at DESC LIMIT 1`,
		variantID).Scan(&reason, &sourceType, &delta); err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	if reason != "return" || delta != 1 {
		t.Errorf("the ledger says %q %+d, want return +1", reason, delta)
	}
	if sourceType != "return_request" {
		t.Errorf("the movement points at %q, want return_request — a shop asking "+
			"why the number moved gets no answer otherwise", sourceType)
	}
}

func TestAReturnCannotCloseWithAnUninspectedLine(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, variantID := returnedOrderWithStock(t, "uninspected", 2)
	lineID := returnLineID(t, requestID)

	if err := s.Decide(ctx, requestID.String(), "approved", "", "", actor); err != nil {
		t.Fatalf("approve: %v", err)
	}

	if err := s.Complete(ctx, requestID.String(), "", actor); !errors.Is(err, returns.ErrRefused) {
		t.Fatalf("closing an uninspected return = %v, want ErrRefused", err)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "approved" {
		t.Errorf("the return is %q after a refused completion, want approved", status)
	}

	if err := s.Inspect(ctx, requestID.String(), []returns.LineInspection{{
		OrderLineID: lineID, Received: 2, Restocked: 2,
	}}, actor); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if err := s.Complete(ctx, requestID.String(), "已退款並入庫", actor); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "completed" {
		t.Errorf("the return is %q after closing, want completed", status)
	}
	if got, want := stockOf(t, variantID), int32(5-0+2); got != want {
		t.Errorf("stock is %d after closing, want %d — completing a return must not "+
			"restock a second time", got, want)
	}
}

func TestInspectingIsRefusedBeforeApproval(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, variantID := returnedOrderWithStock(t, "unapproved", 2)
	lineID := returnLineID(t, requestID)

	before := stockOf(t, variantID)
	err := s.Inspect(ctx, requestID.String(), []returns.LineInspection{{
		OrderLineID: lineID, Received: 2, Restocked: 2,
	}}, actor)
	if !errors.Is(err, returns.ErrRefused) {
		t.Fatalf("inspecting an undecided return = %v, want ErrRefused", err)
	}
	if got := stockOf(t, variantID); got != before {
		t.Errorf("stock moved %d -> %d on a refused inspection", before, got)
	}
}

func TestRestockingMoreThanArrivedIsRefused(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, variantID := returnedOrderWithStock(t, "overrestock", 1)
	lineID := returnLineID(t, requestID)

	if err := s.Decide(ctx, requestID.String(), "approved", "", "", actor); err != nil {
		t.Fatalf("approve: %v", err)
	}
	before := stockOf(t, variantID)

	err := s.Inspect(ctx, requestID.String(), []returns.LineInspection{{
		OrderLineID: lineID, Received: 1, Restocked: 2,
	}}, actor)
	if !errors.Is(err, returns.ErrInvalid) {
		t.Fatalf("restocking 2 of 1 received = %v, want ErrInvalid", err)
	}
	if got := stockOf(t, variantID); got != before {
		t.Errorf("stock moved %d -> %d on a refused inspection", before, got)
	}
}

func TestALineIsInspectedOnceAndACorrectionIsAnAdjustment(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	requestID, variantID := returnedOrderWithStock(t, "twice", 2)
	lineID := returnLineID(t, requestID)

	if err := s.Decide(ctx, requestID.String(), "approved", "", "", actor); err != nil {
		t.Fatalf("approve: %v", err)
	}
	before := stockOf(t, variantID)

	if err := s.Inspect(ctx, requestID.String(), []returns.LineInspection{{
		OrderLineID: lineID, Received: 2, Restocked: 2,
	}}, actor); err != nil {
		t.Fatalf("first inspection: %v", err)
	}

	err := s.Inspect(ctx, requestID.String(), []returns.LineInspection{{
		OrderLineID: lineID, Received: 2, Restocked: 1,
	}}, actor)
	if !errors.Is(err, returns.ErrRefused) {
		t.Fatalf("re-inspecting = %v, want ErrRefused", err)
	}

	if got, want := stockOf(t, variantID), before+2; got != want {
		t.Errorf("stock is %d after a refused re-inspection, want %d", got, want)
	}
	var movements int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_movements
		WHERE variant_id = $1 AND reason = 'return'`, variantID).Scan(&movements); err != nil {
		t.Fatalf("count movements: %v", err)
	}
	if movements != 1 {
		t.Errorf("%d return movements after two inspections, want 1", movements)
	}

	var restocked int32
	if err := pool.QueryRow(ctx, `
		SELECT restocked_quantity FROM return_request_lines
		WHERE return_request_id = $1`, requestID).Scan(&restocked); err != nil {
		t.Fatalf("read the line: %v", err)
	}
	if restocked != 2 {
		t.Errorf("the line records %d restocked, want 2 — the refused submission "+
			"changed the record without changing the stock", restocked)
	}
}

// TestTheLoserOfTwoSimultaneousDecisionsPostsNoCredit is the same rule with no
// provider anywhere in it: a wholly credit-funded order has no payment row, so
// the losing decision's payout would be a single INSERT into the ledger, raising
// the customer's balance on a return the shop had just refused.
func TestTheLoserOfTwoSimultaneousDecisionsPostsNoCredit(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := storeOver(pool, admintest.Refunder{})
	// WHOLLY credit-funded: no payment row exists, so the payout on this path is
	// one INSERT into the ledger and there is no provider to blame.
	requestID, _, accountID := admintest.CreditFundedReturn(t, pool, 2, 200000)
	before := admintest.CreditBalance(t, pool, accountID)

	t1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin T1: %v", err)
	}
	defer func() { _ = t1.Rollback(ctx) }()
	if _, err := t1.Exec(ctx, `
		UPDATE return_requests SET status = 'rejected', decided_at = now()
		WHERE id = $1 AND status = 'requested'`, requestID); err != nil {
		t.Fatalf("T1 decide: %v", err)
	}

	decided := make(chan error, 1)
	go func() { decided <- s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}) }()

	select {
	case err := <-decided:
		t.Fatalf("T2 finished before T1 committed (%v); it never met the lock", err)
	case <-time.After(250 * time.Millisecond):
	}
	if err := t1.Commit(ctx); err != nil {
		t.Fatalf("commit T1: %v", err)
	}
	if err := <-decided; !errors.Is(err, returns.ErrRefused) {
		t.Errorf("the second decision returned %v, want ErrRefused", err)
	}

	if after := admintest.CreditBalance(t, pool, accountID); after != before {
		t.Errorf("store credit went %d -> %d on a return that was REJECTED", before, after)
	}
}

// TestASplitReturnResumesWhenTheCREDITHalfLanded is the mirror of its neighbour.
//
// refundCard answers (ref, RefundPending, nil) for a refund Stripe has accepted
// and not settled — no error — so payApprovedReturn goes on to post the credit.
// goen consumes no refund webhook, so that row stays pending for ever and
// pressing 同意 again is the only door. splitRefund must therefore exclude this
// return's own compensation from the credit side, or the retry reads the credit
// it just posted as credit already returned and refuses the whole payout.
func TestASplitReturnResumesWhenTheCREDITHalfLanded(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	requestID, orderNumber, accountID := admintest.CreditFundedReturn(t, pool, 2, 60000)

	// Approved, with the CREDIT half posted under the key the compensation uses
	// and the card half left PENDING — which is what a Stripe refund that has
	// been accepted and not settled looks like.
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests SET status = 'approved', decided_at = now(),
		       resolution = '退貨完成'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve the return: %v", err)
	}
	var userID, orderID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT user_id, id FROM orders WHERE order_number = $1`, orderNumber).
		Scan(&userID, &orderID); err != nil {
		t.Fatalf("read the order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		SELECT post_store_credit($1, $2::bigint, $3::text, $4, $5::text, NULL)`,
		userID, int64(60000), "退貨補償", orderID,
		"return-credit:"+requestID.String()); err != nil {
		t.Fatalf("post the credit half: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, reason,
		                     return_request_id, status)
		SELECT p.id, 'return:' || $1::text, 140000, '退貨完成', $1::uuid, 'pending'
		FROM payments p JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $2 AND p.status = 'succeeded'`,
		requestID, orderNumber); err != nil {
		t.Fatalf("open the card half: %v", err)
	}
	if got := admintest.CardRefunded(t, pool, orderNumber); got != 0 {
		t.Fatalf("the card half reads %d settled, want 0 — the fixture did not "+
			"build the state under test", got)
	}
	before := admintest.CreditBalance(t, pool, accountID)

	sent := &atomic.Int64{}
	s := storeOver(pool, admintest.Refunder{Sent: sent})
	if err := s.Decide(ctx, requestID.String(), "approved", "退貨完成", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("the retry was refused (%v), so the CARD half can never be sent "+
			"and the customer stays short — goen consumes no refund webhook, so "+
			"pressing 同意 again is the only door", err)
	}
	if sent.Load() != 1 {
		t.Errorf("the provider was called %d time(s); the card half was the one "+
			"still owed", sent.Load())
	}
	if after := admintest.CreditBalance(t, pool, accountID); after != before {
		t.Errorf("store credit went %d -> %d; the credit half had already landed "+
			"and resuming means paying only what is MISSING", before, after)
	}
	var events int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events e
		WHERE e.return_request_id = $1 AND e.kind = 'refunded'`, requestID).Scan(&events); err != nil {
		t.Fatalf("count timeline events after the card half settled: %v", err)
	}
	if events != 1 {
		t.Errorf("%d refunded events after both sources settled, want 1", events)
	}
}

// TestATerminalCardRetrySurvivesErasureAfterCreditLanded proves the two source
// obligations are independent. Once the exact frozen credit half is posted,
// erasure may detach the account; a later known-failed card attempt must still
// get a successor without trying to post credit to an erased customer again.
func TestATerminalCardRetrySurvivesErasureAfterCreditLanded(t *testing.T) {
	ctx, staffID := admintest.StaffContext(t, pool)
	requestID, orderNumber, accountID := admintest.CreditFundedReturn(t, pool, 2, 60000)
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests SET status = 'approved', decided_at = now(),
		       resolution = 'erasure retry'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve split return: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT compensate_return_with_credit($1, 60000, $2)`, requestID, staffID); err != nil {
		t.Fatalf("post frozen credit half: %v", err)
	}
	before := admintest.CreditBalance(t, pool, accountID)

	failed := storeOver(pool, admintest.Refunder{State: refundstate.Failed})
	if err := failed.Decide(ctx, requestID.String(), "approved", "erasure retry", "", uuid.NullUUID{}); err == nil {
		t.Fatal("known-failed card attempt was reported as settled")
	}
	var customerID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT user_id FROM orders WHERE order_number = $1`, orderNumber).Scan(&customerID); err != nil {
		t.Fatalf("read return owner: %v", err)
	}
	if err := account.NewStore(pool).Erase(ctx, customerID.String()); err != nil {
		t.Fatalf("erase after exact credit posting: %v", err)
	}
	var detached bool
	if err := pool.QueryRow(ctx, `
		SELECT user_id IS NULL FROM orders WHERE order_number = $1`, orderNumber).
		Scan(&detached); err != nil {
		t.Fatalf("read erased order owner: %v", err)
	}
	if !detached {
		t.Fatal("erasure did not detach the order owner")
	}

	sent := &atomic.Int64{}
	healthy := storeOver(pool, admintest.Refunder{Sent: sent})
	if err := healthy.Decide(ctx, requestID.String(), "approved", "erasure retry", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("retry terminal card attempt after erasure: %v", err)
	}
	if sent.Load() != 1 {
		t.Errorf("provider calls after erasure = %d, want one successor", sent.Load())
	}
	if after := admintest.CreditBalance(t, pool, accountID); after != before {
		t.Errorf("erased account credit changed %d -> %d; exact credit was already posted",
			before, after)
	}
	var attempts, succeeded int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status = 'succeeded')
		FROM refunds WHERE return_request_id = $1`, requestID).Scan(&attempts, &succeeded); err != nil {
		t.Fatalf("read post-erasure attempts: %v", err)
	}
	if attempts != 2 || succeeded != 1 {
		t.Errorf("post-erasure attempts/succeeded = %d/%d, want 2/1", attempts, succeeded)
	}
}

// TestAProviderRefusalGetsANewDurableAttempt holds the distinction between
// retrying ambiguity and retrying a known terminal outcome. Ambiguity reuses one
// provider key; failed/cancelled is immutable evidence and gets a linked next
// generation with a fresh DB-derived key.
func TestAProviderRefusalGetsANewDurableAttempt(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.ReturnDesk(pool, admintest.Refunder{State: refundstate.Failed})
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

	healthy := admintest.ReturnDesk(pool, admintest.Refunder{})
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
	ctx, _ := admintest.StaffContext(t, pool)
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
	s := admintest.ReturnDesk(pool, admintest.Refunder{Sent: sent})
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
