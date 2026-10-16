//go:build integration

package payment_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
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
	return placedOrder(t, totalCents, 0)
}

// holdableOrder is order carrying two holdable variant lines for hold.
func holdableOrder(t *testing.T, totalCents int64) (number string, id uuid.UUID) {
	t.Helper()
	return placedOrder(t, totalCents, 2)
}

func placedOrder(t *testing.T, totalCents int64, holdable int) (number string, id uuid.UUID) {
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
	addHoldableLines(t, tx, id, holdable)
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
// capture behavior, not webhook redelivery. A paid session always names its
// currency, so a fixture that leaves it out means the one goen charges in.
func captureThroughWebhook(t *testing.T, s *payment.Store, c payment.Capture) (string, error) {
	t.Helper()
	if c.Currency == "" {
		c.Currency = payment.Currency
	}
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
			_, waiting, err := dbtest.Blocked(ctx, pool, dbtest.Match{QueryLike: queryPattern})
			if err != nil {
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

// hold reserves the unit on the nth variant line of an order holdableOrder
// placed.
func hold(t *testing.T, orderID uuid.UUID, nth int, forDuration time.Duration, key string) time.Time {
	t.Helper()
	var variantID uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		SELECT variant_id FROM order_lines
		WHERE order_id = $1 AND variant_id IS NOT NULL
		ORDER BY position LIMIT 1 OFFSET $2`, orderID, nth).Scan(&variantID); err != nil {
		t.Fatalf("find variant line %d of the order, which holdableOrder places: %v", nth, err)
	}
	var reservationID uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		SELECT hold_inventory($1, $2, 1, $3::interval, $4)`, orderID, variantID, pgtype.Interval{
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

// ownedOrder places an order for a signed-in customer without opening payment.
func ownedOrder(t *testing.T, userID uuid.UUID, cents int64) (number string, orderID uuid.UUID) {
	t.Helper()
	return ownedOrderHolding(t, userID, cents, 0)
}

// addHoldableLines puts one unit each of the n variants with the most stock on
// an order, free, inside the transaction placing it: order_lines_written_while_placing
// admits no line later, and hold_inventory holds nothing an order's own lines do
// not carry. Free, so what the order owes, which every session here is priced
// from, does not move.
func addHoldableLines(t *testing.T, tx pgx.Tx, orderID uuid.UUID, n int) {
	t.Helper()
	if _, err := tx.Exec(t.Context(), `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents,
		                         quantity, position)
		SELECT $1, pv.id, pv.sku, p.name, 0, 1,
		       row_number() OVER (ORDER BY pv.stock_quantity DESC, pv.id)
		FROM (SELECT id, sku, product_id, stock_quantity FROM product_variants
		      ORDER BY stock_quantity DESC, id LIMIT $2) pv
		JOIN products p ON p.id = pv.product_id`, orderID, n); err != nil {
		t.Fatalf("put holdable variants on the order: %v", err)
	}
}

// ownedOrderHolding is ownedOrder with holdable free variant lines for hold.
func ownedOrderHolding(t *testing.T, userID uuid.UUID, cents int64, holdable int) (number string, orderID uuid.UUID) {
	t.Helper()
	return ownedOrderPlacing(t, userID, cents, holdable, nil)
}

// ownedOrderPlacing is ownedOrderHolding with whatever else the placing
// transaction writes, such as a coupon redemption, which no later one may.
func ownedOrderPlacing(t *testing.T, userID uuid.UUID, cents int64, holdable int,
	placing func(tx pgx.Tx, orderID uuid.UUID)) (number string, orderID uuid.UUID) {
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
	addHoldableLines(t, tx, orderID, holdable)
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'tiered@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if placing != nil {
		placing(tx, orderID)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, orderID
}

// paymentRows counts the payment attempts recorded for an order.
func paymentRows(t *testing.T, orderID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM payments WHERE order_id = $1`, orderID).Scan(&n); err != nil {
		t.Fatalf("count payment attempts: %v", err)
	}
	return n
}

// staffContext is a signed-in admin. Advance records audit_events from the
// context; a missing actor is refused before status effects run.
func staffContext(t *testing.T) (context.Context, uuid.UUID) {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('audit-' || gen_random_uuid() || '@goen.invalid', 'admin', '稽核測試')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	ctx := user.NewContext(t.Context(), user.User{ID: id.String(), Role: user.RoleAdmin})
	return web.WithRequestID(ctx, "req-"+id.String()[:8]), id
}

func adminRolePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return rolePool(t, "admin")
}

func rolePool(t *testing.T, role string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse %s-role pool: %v", role, err)
	}
	cfg.MaxConns = 1
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, roleErr := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
		return roleErr
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open %s-role pool: %v", role, err)
	}
	t.Cleanup(p.Close)
	return p
}

func spendCreditOnOrder(t *testing.T, orderID uuid.UUID, debitCents int64) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `SELECT spend_store_credit($1, $2)`,
		orderID, debitCents); err != nil {
		t.Fatalf("spend credit on the order: %v", err)
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

// placedBy gives req the grant the browser that placed the order holds.
func placedBy(t *testing.T, req *http.Request, number string) {
	t.Helper()
	w := httptest.NewRecorder()
	grant := httptest.NewRequestWithContext(req.Context(), http.MethodGet, "/", http.NoBody)
	if err := access.NewStore(pool, false).Grant(w, grant, number); err != nil {
		t.Fatalf("grant access to %s: %v", number, err)
	}
	for _, c := range w.Result().Cookies() {
		req.AddCookie(c)
	}
}

// gatewayRecordingCalls gives an external-package integration test a real
// Gateway whose Stripe is a local server counting the sessions it creates.
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
	return gatewayAt(t, srv.URL), &callCount
}

// disabledGateway is a Gateway built without a Stripe key: it refuses every
// refund with payment.ErrDisabled.
func disabledGateway(t *testing.T) *payment.Gateway {
	t.Helper()
	gateway, err := payment.NewGateway("", "", "https://goen.test")
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	return gateway
}

// gatewayAt is a real Gateway, built through the production Stripe client,
// whose Stripe is the stand-in at stripeURL.
func gatewayAt(t *testing.T, stripeURL string) *payment.Gateway {
	t.Helper()
	gateway, err := payment.NewGatewayAt(
		"sk_test_notreal", testWebhookSecret, "https://goen.example", stripeURL,
	)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	return gateway
}
