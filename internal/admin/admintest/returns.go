//go:build integration

package admintest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/web"
)

func MoveOrderToShipped(t *testing.T, tx pgx.Tx, orderID uuid.UUID) {
	t.Helper()
	for _, status := range []string{"picking", "shipped"} {
		if _, err := tx.Exec(t.Context(),
			`UPDATE orders SET fulfillment_status = $2 WHERE id = $1`,
			orderID, status); err != nil {
			t.Fatalf("move the order to %s: %v", status, err)
		}
	}
}

func ReturnedOrder(
	t *testing.T, p *pgxpool.Pool, qty int32,
) (requestID uuid.UUID, orderNumber string) {
	t.Helper()
	ctx := t.Context()

	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit

	var orderID, lineID uuid.UUID
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
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'REF-SKU', '測試商品', 100000, 2) RETURNING id`,
		orderID).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'f@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 200000)`,
		orderID, "cs_ret_"+orderNumber); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 200000, NULL, NULL)`,
		"cs_ret_"+orderNumber); err != nil {
		t.Fatalf("capture: %v", err)
	}
	MoveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'black_cat', 'T-'||$2) RETURNING id`, orderID, orderNumber).Scan(&shipmentID); err != nil {
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
	return requestID, orderNumber
}

func ReturnedOrderAtWithReason(
	t *testing.T, p *pgxpool.Pool, delivered, requested time.Time, reason string,
) (requestID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit

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
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'REF-CALENDAR', '鑑賞期日曆測試', 100000, 2) RETURNING id`,
		orderID).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'calendar-return@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	sessionID := "cs_ret_calendar_" + orderNumber
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 200000)`, orderID, sessionID); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 200000, NULL, NULL)`, sessionID); err != nil {
		t.Fatalf("capture: %v", err)
	}
	MoveOrderToShipped(t, tx, orderID)

	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (
			order_id, carrier, tracking_number, shipped_at, delivered_at
		) VALUES ($1, 'black_cat', 'T-CALENDAR-' || $2, $3, $4)
		RETURNING id`, orderID, orderNumber, delivered.Add(-48*time.Hour), delivered).Scan(&shipmentID); err != nil {
		t.Fatalf("create delivered shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 2)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason, created_at)
		VALUES ($1, $2, $3) RETURNING id`, orderID, reason, requested).Scan(&requestID); err != nil {
		t.Fatalf("create return request: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 2)`, orderID, requestID, lineID); err != nil {
		t.Fatalf("create return line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return requestID
}

// LoyaltyReturn reaches the capture door that awards points, then constructs
// the already-delivered parcel a return decision consumes. prices are separate
// lines so returning one can distinguish proportional reversal from reversing
// the whole order.
func LoyaltyReturn(t *testing.T, pool *pgxpool.Pool, prices []int64, returnLine int) (
	requestID, orderID, userID uuid.UUID,
) {
	t.Helper()
	ctx := t.Context()
	var orderNumber string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('return-points-' || gen_random_uuid() || '@goen.invalid', 'customer', '點數退貨')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create customer: %v", err)
	}

	tx, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin order: %v", beginErr)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	lineIDs := make([]uuid.UUID, len(prices))
	var total int64
	for i, cents := range prices {
		total += cents
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, $2, '點數退貨商品', $3, 1, $4) RETURNING id`,
			orderID, fmt.Sprintf("RET-POINTS-%d-%s", i, orderID), cents, i).Scan(&lineIDs[i]); err != nil {
			t.Fatalf("create line %d: %v", i, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'points-return@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit order: %v", err)
	}

	awardByCapture(t, pool, orderNumber, "cs_return_points_"+orderID.String(), total)

	tx, beginErr = pool.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin delivery: %v", beginErr)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	MoveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number, delivered_at)
		VALUES ($1, 'black_cat', 'RP-' || $2, now()) RETURNING id`,
		orderID, orderNumber).Scan(&shipmentID); err != nil {
		t.Fatalf("create delivered shipment: %v", err)
	}
	for i, lineID := range lineIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
			VALUES ($1, $2, $3, 1)`, orderID, shipmentID, lineID); err != nil {
			t.Fatalf("ship line %d: %v", i, err)
		}
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, requested_by_user_id, reason)
		VALUES ($1, $2, '不合用') RETURNING id`, orderID, userID).Scan(&requestID); err != nil {
		t.Fatalf("create return: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, requestID, lineIDs[returnLine]); err != nil {
		t.Fatalf("create return line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit return: %v", err)
	}
	return requestID, orderID, userID
}

func CreditFundedReturn(
	t *testing.T, p *pgxpool.Pool, qty int32, creditCents int64,
) (requestID uuid.UUID, orderNumber string, accountID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	const price = 100000
	total := int64(2) * price
	card := total - creditCents

	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit

	var userID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('ret-credit-'||gen_random_uuid()||'@goen.invalid', 'customer', '退貨額度')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT post_store_credit($1, $2, '測試發放', NULL, $3, NULL)`,
		userID, creditCents, "grant:"+userID.String()); err != nil {
		t.Fatalf("grant credit: %v", err)
	}

	var orderID, lineID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &orderNumber); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'REF-SKU', '測試商品', $2, 2) RETURNING id`,
		orderID, price).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'f@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	// The spend, while the order is still an open unpaid checkout: the only moment store_credit_guard allows it.
	if _, err := tx.Exec(ctx, `SELECT post_store_credit($1, $2, '訂單折抵', $3, $4, NULL)`,
		userID, -creditCents, orderID, "spend:"+orderID.String()); err != nil {
		t.Fatalf("spend credit: %v", err)
	}
	if card > 0 {
		captureCard(t, tx, orderID, "cs_cred_"+orderNumber, card)
	}

	MoveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'black_cat', 'TC-'||$2) RETURNING id`,
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
	if err := tx.QueryRow(ctx,
		`SELECT id FROM store_credit_accounts WHERE user_id = $1`, userID).
		Scan(&accountID); err != nil {
		t.Fatalf("read credit account: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return requestID, orderNumber, accountID
}

func CreditBalance(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID) int64 {
	t.Helper()
	var cents int64
	if err := pool.QueryRow(t.Context(),
		`SELECT coalesce(sum(amount_cents), 0)::bigint FROM store_credit_entries
		 WHERE account_id = $1`, accountID).Scan(&cents); err != nil {
		t.Fatalf("read credit balance: %v", err)
	}
	return cents
}

func CardRefunded(t *testing.T, pool *pgxpool.Pool, orderNumber string) int64 {
	t.Helper()
	var cents int64
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce(sum(r.amount_cents), 0) FROM refunds r
		JOIN payments p ON p.id = r.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE o.order_number = $1 AND r.status = 'succeeded'`,
		orderNumber).Scan(&cents); err != nil {
		t.Fatalf("read refunds: %v", err)
	}
	return cents
}

func RefundRecoveryStaffContext(
	t *testing.T, pool *pgxpool.Pool, label string,
) (context.Context, uuid.UUID, string) {
	t.Helper()
	ctx, actor := StaffContext(t, pool)
	requestID := "refund-" + label + "-" + uuid.NewString()[:8]
	return web.WithRequestID(ctx, requestID), actor, requestID
}

func PreapprovedReturn(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	returnID, _ := ReturnedOrder(t, pool, 1)
	if _, err := pool.Exec(t.Context(), `
		UPDATE return_requests
		SET status = 'approved', decided_at = now(), resolution = 'refund recovery'
		WHERE id = $1`, returnID); err != nil {
		t.Fatalf("pre-approve return: %v", err)
	}
	return returnID
}

func ShopNoonDaysAgo(t *testing.T, days int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(shoptime.Zone)
	if err != nil {
		t.Fatalf("load %s: %v", shoptime.Zone, err)
	}
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, loc).AddDate(0, 0, -days)
}

// awardByCapture pays the order through the capture webhook, which is the door
// that awards its points.
func awardByCapture(t *testing.T, pool *pgxpool.Pool, orderNumber, session string, total int64) {
	t.Helper()
	ctx := t.Context()
	payments := payment.NewStore(pool)
	if err := payments.OpenPayment(ctx, orderNumber, session, total); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	claimed, err := payments.ProcessWebhook(ctx, &payment.WebhookEvent{
		ID:   "evt_admin_return_points_" + uuid.NewString(),
		Type: "checkout.session.completed", ObjectRef: session,
		Payload: []byte(`{"object":"event"}`),
	}, func(ctx context.Context, tx *payment.WebhookTx) error {
		_, captureErr := tx.Capture(ctx, payment.Capture{
			SessionID: session, AmountRecv: total, Currency: payment.Currency,
		})
		return captureErr
	})
	if err != nil {
		t.Fatalf("capture and award: %v", err)
	}
	if !claimed {
		t.Fatal("the unique capture webhook was not claimed")
	}
}

func captureCard(t *testing.T, tx pgx.Tx, orderID uuid.UUID, session string, cents int64) {
	t.Helper()
	ctx := t.Context()
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`, orderID, session, cents); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`, session, cents); err != nil {
		t.Fatalf("capture: %v", err)
	}
}
