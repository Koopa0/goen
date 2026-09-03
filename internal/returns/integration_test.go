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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/returns"
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

// shippedOrder writes a paid order with one line of `ordered` units, of which
// `shipped` have gone out. Equal values cannot tell the two ceilings apart.
func shippedOrder(t *testing.T, ordered, shipped int32) (number string, lineID uuid.UUID) {
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
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'RET-SKU', '測試商品', 100000, $2) RETURNING id`,
		orderID, ordered).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'r@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if shipped > 0 {
		shipReturnFixture(t, tx, orderID, lineID, number, ordered, shipped)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, lineID
}

func shipReturnFixture(
	t *testing.T,
	tx pgx.Tx,
	orderID, lineID uuid.UUID,
	number string,
	ordered, shipped int32,
) {
	t.Helper()
	ctx := t.Context()
	ref := "cs_returns_" + number
	amount := int64(ordered) * 100000
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`, orderID, ref, amount); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`, ref, amount); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move order to picking: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'shipped' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move order to shipped: %v", err)
	}
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, '黑貓', 'TW-'||$2) RETURNING id`, orderID, number).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, $4)`, orderID, shipmentID, lineID, shipped); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
}

func shippedTwoLineOrder(
	t *testing.T,
	shippingCents int64,
) (orderID uuid.UUID, number string, lines [2]uuid.UUID) {
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
		SELECT next_order_number(), v.id, sm.code, v.name, $1
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, shippingCents).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	for i := range lines {
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, $2, '競態退貨商品', 100000, 1, $3) RETURNING id`,
			orderID, fmt.Sprintf("RETURN-RACE-%d-%s", i, orderID), i).Scan(&lines[i]); err != nil {
			t.Fatalf("create line %d: %v", i, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'return-race@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	ref := "cs_return_race_" + number
	amount := int64(200000) + shippingCents
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`, orderID, ref, amount); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`, ref, amount); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move order to picking: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'shipped' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move order to shipped: %v", err)
	}
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, '黑貓', 'RETURN-RACE-' || $2) RETURNING id`, orderID, number).
		Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	for i := range lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
			VALUES ($1, $2, $3, 1)`, orderID, shipmentID, lines[i]); err != nil {
			t.Fatalf("ship line %d: %v", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return orderID, number, lines
}

func returnsApplicationPool(t *testing.T, name string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse application pool config: %v", err)
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["application_name"] = name
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, setRoleErr := conn.Exec(ctx, `SET ROLE store`)
		return setRoleErr
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open application pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func waitForReturnsLock(t *testing.T, pid int, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case <-done:
			t.Fatal("return submission finished before reaching the intended database lock")
		default:
		}
		var waiting bool
		err := pool.QueryRow(t.Context(), `
			SELECT wait_event_type = 'Lock' FROM pg_stat_activity WHERE pid = $1`, pid).
			Scan(&waiting)
		if err == nil && waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("return submission never blocked on the intended database lock: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func returnsOperationResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("operation did not finish after its database lock was released")
		return nil
	}
}

type returnErasureOrder struct {
	userID  uuid.UUID
	orderID uuid.UUID
	number  string
	lineID  uuid.UUID
}

// fundedShippedOrderForReturnErasure makes the source split material to account
// lifetime. cardCents plus NT$1,000 of store funds must fund the requested units.
func fundedShippedOrderForReturnErasure(
	t *testing.T,
	quantity int32,
	cardCents int64,
) returnErasureOrder {
	t.Helper()
	const storeFundsCents int64 = 100000
	if quantity <= 0 || cardCents < 0 || cardCents+storeFundsCents != int64(quantity)*100000 {
		t.Fatalf("invalid return-erasure funding quantity/card/store = %d/%d/%d",
			quantity, cardCents, storeFundsCents)
	}
	ctx := t.Context()
	u, err := account.NewStore(pool).Register(ctx, &account.Credentials{
		Email:    "return-erasure-" + uuid.NewString() + "@example.com",
		Password: "a sufficiently long password",
		Name:     "退貨競態測試",
	})
	if err != nil {
		t.Fatalf("register return-erasure account: %v", err)
	}
	fixture := returnErasureOrder{userID: uuid.MustParse(u.ID)}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin return-erasure order: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at
		LIMIT 1
		RETURNING id, order_number`, fixture.userID).Scan(&fixture.orderID, &fixture.number); err != nil {
		t.Fatalf("create return-erasure order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, $2, '退貨競態商品', 100000, $3)
		RETURNING id`, fixture.orderID, "RETURN-ERASE-"+fixture.orderID.String(), quantity).
		Scan(&fixture.lineID); err != nil {
		t.Fatalf("create return-erasure line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'return-erasure@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, fixture.orderID); err != nil {
		t.Fatalf("create return-erasure private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		SELECT post_store_credit($1, $2, '退貨競態測試額度', NULL, $3, NULL)`,
		fixture.userID, storeFundsCents,
		"return-erasure-grant:"+fixture.orderID.String()); err != nil {
		t.Fatalf("grant return-erasure credit: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT spend_store_credit($1, $2)`, fixture.orderID, -storeFundsCents); err != nil {
		t.Fatalf("fund return-erasure order with credit: %v", err)
	}
	if cardCents > 0 {
		ref := "cs_return_erasure_" + fixture.number
		if _, err := tx.Exec(ctx,
			`SELECT open_payment($1, $2, $3)`, fixture.orderID, ref, cardCents); err != nil {
			t.Fatalf("open return-erasure payment: %v", err)
		}
		if _, err := tx.Exec(ctx,
			`SELECT capture_payment($1, $2, NULL, NULL)`, ref, cardCents); err != nil {
			t.Fatalf("capture return-erasure payment: %v", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, fixture.orderID); err != nil {
		t.Fatalf("move return-erasure order to picking: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'shipped' WHERE id = $1`, fixture.orderID); err != nil {
		t.Fatalf("move return-erasure order to shipped: %v", err)
	}
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, '黑貓', 'RETURN-ERASURE-' || $2)
		RETURNING id`, fixture.orderID, fixture.number).Scan(&shipmentID); err != nil {
		t.Fatalf("create return-erasure shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, $4)`, fixture.orderID, shipmentID, fixture.lineID, quantity); err != nil {
		t.Fatalf("ship return-erasure line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit return-erasure order: %v", err)
	}
	return fixture
}

func assertReturnRowCounts(t *testing.T, orderID uuid.UUID, wantHeaders, wantLines int) {
	t.Helper()
	var headers, lines int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*),
		       coalesce((SELECT count(*)
		                 FROM return_request_lines rl
		                 JOIN return_requests r ON r.id = rl.return_request_id
		                 WHERE r.order_id = $1), 0)
		FROM return_requests
		WHERE order_id = $1`, orderID).Scan(&headers, &lines); err != nil {
		t.Fatalf("count return rows: %v", err)
	}
	if headers != wantHeaders || lines != wantLines {
		t.Errorf("return headers/lines = %d/%d, want %d/%d",
			headers, lines, wantHeaders, wantLines)
	}
}

func openReturnUnits(
	t *testing.T,
	s *returns.Store,
	fixture returnErasureOrder,
	quantity int32,
	reason string,
) uuid.UUID {
	t.Helper()
	if err := s.Open(t.Context(), fixture.number, uuid.NullUUID{}, &returns.Request{
		Reason: reason,
		Lines:  map[string]int32{fixture.lineID.String(): quantity},
	}); err != nil {
		t.Fatalf("open %q return: %v", reason, err)
	}
	var requestID uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		SELECT id FROM return_requests
		WHERE order_id = $1 AND reason = $2`, fixture.orderID, reason).Scan(&requestID); err != nil {
		t.Fatalf("read %q return id: %v", reason, err)
	}
	return requestID
}

type returnPlacedHere struct{}

func (returnPlacedHere) PlacedHere(context.Context, *http.Request, string, bool) bool {
	return true
}

func TestReturnableIsWhatShippedNotWhatWasOrdered(t *testing.T) {
	s := returns.NewStore(pool)

	tests := []struct {
		name             string
		ordered, shipped int32
		wantReturnable   int32
	}{
		{"nothing shipped", 3, 0, 0},
		{"partly shipped", 3, 1, 1},
		{"fully shipped", 3, 3, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			number, _ := shippedOrder(t, tt.ordered, tt.shipped)
			o, err := s.Order(t.Context(), number)
			if err != nil {
				t.Fatalf("read order: %v", err)
			}
			if len(o.Lines) != 1 {
				t.Fatalf("%d lines, want 1", len(o.Lines))
			}
			if o.Lines[0].Returnable != tt.wantReturnable {
				t.Errorf("ordered %d, shipped %d: returnable is %d, want %d",
					tt.ordered, tt.shipped, o.Lines[0].Returnable, tt.wantReturnable)
			}
		})
	}
}

// The later request deliberately carries an earlier transaction timestamp:
// payout identity follows the frozen approval decision, not created_at.
func TestReturnRefundableAmountAllocatesShippingOnceAcrossPartialReturns(t *testing.T) {
	ctx := t.Context()
	const shippingCents int64 = 10000
	orderID, number, lines := shippedTwoLineOrder(t, shippingCents)
	s := returns.NewStore(pool)

	// now() is the transaction start time in PostgreSQL. Keep this transaction
	// open so the request inserted below sorts before the request approved first.
	earlyTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin long-lived return transaction: %v", err)
	}
	defer func() { _ = earlyTx.Rollback(context.WithoutCancel(ctx)) }()
	var earlyStarted time.Time
	if err := earlyTx.QueryRow(ctx, `SELECT now()`).Scan(&earlyStarted); err != nil {
		t.Fatalf("start long-lived return transaction: %v", err)
	}

	if err := s.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
		Reason: "first partial",
		Lines:  map[string]int32{lines[0].String(): 1},
	}); err != nil {
		t.Fatalf("open first partial return: %v", err)
	}
	var firstID uuid.UUID
	var firstCreated time.Time
	if err := pool.QueryRow(ctx, `
		SELECT id, created_at FROM return_requests
		WHERE order_id = $1 AND reason = 'first partial'`, orderID).
		Scan(&firstID, &firstCreated); err != nil {
		t.Fatalf("read first partial return: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests
		SET status = 'approved', resolution = 'approved fixture', decided_at = now()
		WHERE id = $1`, firstID); err != nil {
		t.Fatalf("approve first partial return: %v", err)
	}

	var secondID uuid.UUID
	if err := earlyTx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason)
		VALUES ($1, 'second partial')
		RETURNING id`, orderID).Scan(&secondID); err != nil {
		t.Fatalf("open timestamp-inverted second partial return: %v", err)
	}
	if _, err := earlyTx.Exec(ctx, `
		INSERT INTO return_request_lines
		       (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, secondID, lines[1]); err != nil {
		t.Fatalf("add timestamp-inverted second partial line: %v", err)
	}
	if err := earlyTx.Commit(ctx); err != nil {
		t.Fatalf("commit timestamp-inverted second partial return: %v", err)
	}

	var secondCreated time.Time
	if err := pool.QueryRow(ctx, `
		SELECT created_at FROM return_requests WHERE id = $1`, secondID).
		Scan(&secondCreated); err != nil {
		t.Fatalf("read second partial timestamp: %v", err)
	}
	if !secondCreated.Equal(earlyStarted) || !secondCreated.Before(firstCreated) {
		t.Fatalf("second request created_at %s, want transaction start %s before first %s",
			secondCreated, earlyStarted, firstCreated)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests
		SET status = 'approved', resolution = 'approved fixture', decided_at = now()
		WHERE id = $1`, secondID); err != nil {
		t.Fatalf("approve second partial return: %v", err)
	}

	var firstCents, secondCents int64
	if err := pool.QueryRow(ctx, `
		SELECT return_refundable_amount($1), return_refundable_amount($2)`,
		firstID, secondID).Scan(&firstCents, &secondCents); err != nil {
		t.Fatalf("read partial return amounts: %v", err)
	}
	if firstCents != 100000 || secondCents != 100000+shippingCents {
		t.Errorf("first/second refundable cents = %d/%d, want 100000/%d",
			firstCents, secondCents, 100000+shippingCents)
	}
	if firstCents+secondCents != 200000+shippingCents {
		t.Errorf("partial refunds total %d, want the order's exact %d",
			firstCents+secondCents, 200000+shippingCents)
	}
}

// TestStoreRoleGuestCardReturnCommitsThroughDeferredOwnerGuard exercises the
// runtime role, not the migration owner. A guest order has no live account but
// a card-funded return needs none; the narrow SECURITY DEFINER trigger must be
// callable and must admit it at the deferred commit.
func TestStoreRoleGuestCardReturnCommitsThroughDeferredOwnerGuard(t *testing.T) {
	number, lineID := shippedOrder(t, 1, 1)
	app := returnsApplicationPool(t, "guest-card-return-"+uuid.NewString()[:8])
	if err := returns.NewStore(app).Open(t.Context(), number, uuid.NullUUID{}, &returns.Request{
		Reason: "card funded guest return",
		Lines:  map[string]int32{lineID.String(): 1},
	}); err != nil {
		t.Fatalf("store-role guest card return: %v", err)
	}
}

func TestOpenRefusesMoreThanShipped(t *testing.T) {
	ctx := t.Context()
	s := returns.NewStore(pool)
	number, lineID := shippedOrder(t, 3, 1)

	err := s.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
		Reason: "不合用", Lines: map[string]int32{lineID.String(): 2},
	})
	if !errors.Is(err, returns.ErrTooMany) || errors.Is(err, returns.ErrInvalid) {
		t.Fatalf("returning 2 of a line that shipped 1 gave %v, want only ErrTooMany", err)
	}

	var rows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM return_requests r JOIN orders o ON o.id = r.order_id
		WHERE o.order_number = $1`, number).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Errorf("%d requests written for a refused claim, want 0", rows)
	}
}

// TestAConcurrentReturnRendersTheFreshQuantity holds a production interleaving:
// another approved request is invisible to the first form read but commits
// before its write. return_within_shipment must become ErrTooMany, and the
// rejection must reread the order rather than advertising the refused ceiling.
func TestAConcurrentReturnRendersTheFreshQuantity(t *testing.T) {
	ctx := t.Context()
	number, lineID := shippedOrder(t, 3, 3)
	var orderID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE order_number = $1`, number).
		Scan(&orderID); err != nil {
		t.Fatalf("read order id: %v", err)
	}

	competitor, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin competing return: %v", err)
	}
	defer func() { _ = competitor.Rollback(ctx) }()
	var requestID uuid.UUID
	if err := competitor.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason)
		VALUES ($1, 'competing return') RETURNING id`, orderID).
		Scan(&requestID); err != nil {
		t.Fatalf("create competing return: %v", err)
	}
	if _, err := competitor.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 2)`, orderID, requestID, lineID); err != nil {
		t.Fatalf("claim two units in competing return: %v", err)
	}
	if _, err := competitor.Exec(ctx, `
		UPDATE return_requests
		SET status = 'approved', decided_at = now(), resolution = 'approved in fixture'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve competing return: %v", err)
	}

	app := "wave0-return-" + uuid.NewString()
	appPool := returnsApplicationPool(t, app)
	var pid int
	if err := appPool.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("read return backend pid: %v", err)
	}
	s := returns.NewStore(appPool)
	h := returns.NewHandler(s, returnPlacedHere{}, slog.New(slog.DiscardHandler), false)
	form := url.Values{
		"qty_" + lineID.String(): {"2"},
		"reason":                 {"尺寸不合"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/orders/"+number+"/return", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("number", number)
	res := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.Submit(res, req)
		close(done)
	}()
	waitForReturnsLock(t, pid, done)
	if err := competitor.Commit(ctx); err != nil {
		t.Fatalf("commit competing return: %v", err)
	}
	<-done

	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("raced return answered %d, want 422; body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, i18n.T(ctx, i18n.KeyReturnTooMany)) {
		t.Error("the refusal does not say the return quantity is too large")
	}
	if strings.Contains(body, i18n.T(ctx, i18n.KeyReturnInvalid)) {
		t.Error("a quantity race was reported as a generic invalid request")
	}
	fieldAt := strings.Index(body, `name="qty_`+lineID.String()+`"`)
	if fieldAt < 0 {
		t.Fatal("the refused line is absent from the fresh form")
	}
	field := body[fieldAt:min(len(body), fieldAt+300)]
	if !strings.Contains(field, `max="1"`) {
		t.Errorf("the form kept the stale ceiling instead of the fresh 1: %s", field)
	}
	if !strings.Contains(field, `value="2"`) {
		t.Errorf("the fresh form lost the submitted quantity 2: %s", field)
	}
	if !strings.Contains(body, "尺寸不合") {
		t.Error("the fresh form lost the submitted reason")
	}
}

// TestReturnCreditRequiresLiveAccountSerializesGuestReturnAndErasure holds both
// order-lock interleavings and the aggregate source split. The access grant is
// already authority to file, so every Open deliberately carries a null actor;
// account lifetime must be decided by money sources, not by that actor field.
func TestReturnCreditRequiresLiveAccountSerializesGuestReturnAndErasure(t *testing.T) {
	const accountRule = "return_credit_requires_live_account"

	t.Run("return commits before erasure", func(t *testing.T) {
		ctx := t.Context()
		fixture := fundedShippedOrderForReturnErasure(t, 1, 0)

		returnTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin winning return: %v", err)
		}
		defer func() { _ = returnTx.Rollback(context.WithoutCancel(ctx)) }()
		var requestID uuid.UUID
		if err := returnTx.QueryRow(ctx, `
			INSERT INTO return_requests (order_id, requested_by_user_id, reason)
			VALUES ($1, NULL, 'return wins erasure race')
			RETURNING id`, fixture.orderID).Scan(&requestID); err != nil {
			t.Fatalf("create winning return header: %v", err)
		}
		if _, err := returnTx.Exec(ctx, `
			INSERT INTO return_request_lines
				(order_id, return_request_id, order_line_id, quantity)
			VALUES ($1, $2, $3, 1)`, fixture.orderID, requestID, fixture.lineID); err != nil {
			t.Fatalf("create winning return line: %v", err)
		}

		erasePool := returnsApplicationPool(t, "erase-behind-return-"+uuid.NewString()[:8])
		var erasePID int
		if err := erasePool.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&erasePID); err != nil {
			t.Fatalf("read erase backend pid: %v", err)
		}
		eraseResult := make(chan error, 1)
		eraseFinished := make(chan struct{})
		go func() {
			defer close(eraseFinished)
			eraseResult <- account.NewStore(erasePool).Erase(
				context.WithoutCancel(ctx), fixture.userID.String())
		}()
		waitForReturnsLock(t, erasePID, eraseFinished)

		if err := returnTx.Commit(ctx); err != nil {
			t.Fatalf("commit winning return: %v", err)
		}
		if err := returnsOperationResult(t, eraseResult); !errors.Is(err, account.ErrOpenReturn) {
			t.Fatalf("erasure behind committed credit return = %v, want ErrOpenReturn", err)
		}
		assertReturnRowCounts(t, fixture.orderID, 1, 1)

		var stillAttached bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM users u JOIN orders o ON o.user_id = u.id
				WHERE u.id = $1 AND o.id = $2
			)`, fixture.userID, fixture.orderID).Scan(&stillAttached); err != nil {
			t.Fatalf("read refused erasure state: %v", err)
		}
		if !stillAttached {
			t.Error("refused erasure detached the return's credit destination")
		}
	})

	t.Run("erasure commits before guest return", func(t *testing.T) {
		ctx := t.Context()
		fixture := fundedShippedOrderForReturnErasure(t, 1, 0)

		privateBlocker, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin private-data blocker: %v", err)
		}
		defer func() { _ = privateBlocker.Rollback(context.WithoutCancel(ctx)) }()
		if _, err := privateBlocker.Exec(ctx, `
			SELECT 1 FROM order_private_data WHERE order_id = $1 FOR UPDATE`,
			fixture.orderID); err != nil {
			t.Fatalf("hold private-data row: %v", err)
		}

		erasePool := returnsApplicationPool(t, "erase-before-return-"+uuid.NewString()[:8])
		var erasePID int
		if err := erasePool.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&erasePID); err != nil {
			t.Fatalf("read erase backend pid: %v", err)
		}
		eraseResult := make(chan error, 1)
		eraseFinished := make(chan struct{})
		go func() {
			defer close(eraseFinished)
			eraseResult <- account.NewStore(erasePool).Erase(
				context.WithoutCancel(ctx), fixture.userID.String())
		}()
		waitForReturnsLock(t, erasePID, eraseFinished)

		openPool := returnsApplicationPool(t, "return-behind-erase-"+uuid.NewString()[:8])
		var openPID int
		if err := openPool.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&openPID); err != nil {
			t.Fatalf("read return backend pid: %v", err)
		}
		openResult := make(chan error, 1)
		openFinished := make(chan struct{})
		go func() {
			defer close(openFinished)
			openResult <- returns.NewStore(openPool).Open(
				context.WithoutCancel(ctx), fixture.number, uuid.NullUUID{}, &returns.Request{
					Reason: "erase wins return race",
					Lines:  map[string]int32{fixture.lineID.String(): 1},
				})
		}()
		waitForReturnsLock(t, openPID, openFinished)

		if err := privateBlocker.Commit(ctx); err != nil {
			t.Fatalf("release private-data blocker: %v", err)
		}
		if err := returnsOperationResult(t, eraseResult); err != nil {
			t.Fatalf("winning erasure: %v", err)
		}
		if err := returnsOperationResult(t, openResult); !errors.Is(err, returns.ErrAccountErased) {
			t.Fatalf("%s mapped raced guest return to %v, want ErrAccountErased",
				accountRule, err)
		}
		assertReturnRowCounts(t, fixture.orderID, 0, 0)

		h := returns.NewHandler(returns.NewStore(openPool), returnPlacedHere{},
			slog.New(slog.DiscardHandler), false)
		form := url.Values{
			"qty_" + fixture.lineID.String(): {"1"},
			"reason":                         {"account was concurrently erased"},
		}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/orders/"+fixture.number+"/return", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("number", fixture.number)
		out := httptest.NewRecorder()
		h.Submit(out, req)
		if out.Code != http.StatusUnprocessableEntity {
			t.Fatalf("erased-account return handler = %d, want 422; body=%s",
				out.Code, out.Body.String())
		}
		if !strings.Contains(out.Body.String(), i18n.T(ctx, i18n.KeyReturnAccountErased)) {
			t.Error("erased-account return handler omitted its recoverable account message")
		}
		assertReturnRowCounts(t, fixture.orderID, 0, 0)
	})

	t.Run("aggregate partial returns keep credit owner alive", func(t *testing.T) {
		ctx := t.Context()
		fixture := fundedShippedOrderForReturnErasure(t, 2, 100000)
		storePool := returnsApplicationPool(t, "aggregate-return-"+uuid.NewString()[:8])
		s := returns.NewStore(storePool)
		firstID := openReturnUnits(t, s, fixture, 1, "aggregate first")
		if _, err := pool.Exec(ctx, `
			UPDATE return_requests
			SET status = 'approved', resolution = 'approved fixture', decided_at = now()
			WHERE id = $1`, firstID); err != nil {
			t.Fatalf("approve aggregate first return: %v", err)
		}
		openReturnUnits(t, s, fixture, 1, "aggregate second")

		var exposure int64
		if err := pool.QueryRow(ctx,
			`SELECT open_return_credit_exposure($1)`, fixture.orderID).Scan(&exposure); err != nil {
			t.Fatalf("read aggregate credit exposure: %v", err)
		}
		if exposure != 100000 {
			t.Fatalf("aggregate credit exposure = %d, want 100000", exposure)
		}
		if err := account.NewStore(storePool).Erase(ctx, fixture.userID.String()); !errors.Is(err, account.ErrOpenReturn) {
			t.Fatalf("erase across two unresolved partial returns = %v, want ErrOpenReturn", err)
		}
		assertReturnRowCounts(t, fixture.orderID, 2, 2)
	})

	t.Run("post-erasure second partial return is refused", func(t *testing.T) {
		ctx := t.Context()
		fixture := fundedShippedOrderForReturnErasure(t, 2, 100000)
		storePool := returnsApplicationPool(t, "post-erase-return-"+uuid.NewString()[:8])
		s := returns.NewStore(storePool)
		firstID := openReturnUnits(t, s, fixture, 1, "card-only first")
		if _, err := pool.Exec(ctx, `
			UPDATE return_requests
			SET status = 'approved', resolution = 'approved fixture', decided_at = now()
			WHERE id = $1`, firstID); err != nil {
			t.Fatalf("approve card-only first return: %v", err)
		}

		if err := account.NewStore(storePool).Erase(ctx, fixture.userID.String()); err != nil {
			t.Fatalf("erase while the one approved return still fits the card: %v", err)
		}
		err := s.Open(ctx, fixture.number, uuid.NullUUID{}, &returns.Request{
			Reason: "second would need credit",
			Lines:  map[string]int32{fixture.lineID.String(): 1},
		})
		if !errors.Is(err, returns.ErrAccountErased) {
			t.Fatalf("%s mapped post-erasure second return to %v, want ErrAccountErased",
				accountRule, err)
		}
		assertReturnRowCounts(t, fixture.orderID, 1, 1)
	})
}

func TestOpenWritesHeaderAndLinesTogether(t *testing.T) {
	ctx := t.Context()
	s := returns.NewStore(pool)
	number, lineID := shippedOrder(t, 3, 2)

	if err := s.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
		Reason: "尺寸不合", Lines: map[string]int32{lineID.String(): 2},
	}); err != nil {
		t.Fatalf("open: %v", err)
	}

	var status, reason string
	var lines, quantity int
	if err := pool.QueryRow(ctx, `
		SELECT r.status, r.reason,
		       (SELECT count(*) FROM return_request_lines rl WHERE rl.return_request_id = r.id),
		       (SELECT coalesce(sum(rl.quantity), 0) FROM return_request_lines rl WHERE rl.return_request_id = r.id)
		FROM return_requests r JOIN orders o ON o.id = r.order_id
		WHERE o.order_number = $1`, number).Scan(&status, &reason, &lines, &quantity); err != nil {
		t.Fatalf("read request: %v", err)
	}
	if status != "requested" {
		t.Errorf("status is %q, want requested", status)
	}
	if reason != "尺寸不合" {
		t.Errorf("reason is %q, want what the customer typed", reason)
	}
	if lines != 1 || quantity != 2 {
		t.Errorf("%d lines totalling %d, want 1 line of 2", lines, quantity)
	}

	o, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if o.Lines[0].Returnable != 0 {
		t.Errorf("%d still returnable after claiming both shipped units, want 0",
			o.Lines[0].Returnable)
	}
}

func TestOnlyOneOpenRequestAtATime(t *testing.T) {
	ctx := t.Context()
	s := returns.NewStore(pool)
	number, lineID := shippedOrder(t, 4, 4)

	first := &returns.Request{Reason: "不合用", Lines: map[string]int32{lineID.String(): 1}}
	if err := s.Open(ctx, number, uuid.NullUUID{}, first); err != nil {
		t.Fatalf("first: %v", err)
	}
	second := &returns.Request{Reason: "還是不合用", Lines: map[string]int32{lineID.String(): 1}}
	if err := s.Open(ctx, number, uuid.NullUUID{}, second); !errors.Is(err, returns.ErrAlreadyOpen) {
		t.Errorf("second request gave %v, want ErrAlreadyOpen", err)
	}
}

// The database serialises the two writers; the pool-side pre-check is not what
// is under test.
func TestOneOpenReturnPerOrder(t *testing.T) {
	ctx := t.Context()
	orderID, number, lines := shippedTwoLineOrder(t, 0)

	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin first return: %v", err)
	}
	defer func() { _ = tx1.Rollback(ctx) }()
	q1 := db.New(pool).WithTx(tx1)
	requestID, err := q1.CreateReturnRequest(ctx, db.CreateReturnRequestParams{
		OrderID: orderID, Reason: "first line",
	})
	if err != nil {
		t.Fatalf("create first return: %v", err)
	}
	if err := q1.CreateReturnRequestLine(ctx, db.CreateReturnRequestLineParams{
		OrderID: orderID, ReturnRequestID: requestID, OrderLineID: lines[0], Quantity: 1,
	}); err != nil {
		t.Fatalf("claim first line: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- returns.NewStore(pool).Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "second line", Lines: map[string]int32{lines[1].String(): 1},
		})
	}()
	select {
	case openErr := <-done:
		t.Fatalf("second return finished before the first committed: %v", openErr)
	case <-time.After(500 * time.Millisecond):
	}

	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("commit first return: %v", err)
	}
	if err := <-done; !errors.Is(err, returns.ErrAlreadyOpen) {
		t.Fatalf("competing return gave %v, want ErrAlreadyOpen", err)
	}

	var open int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM return_requests
		WHERE order_id = $1 AND status = 'requested'`, orderID).Scan(&open); err != nil {
		t.Fatalf("count open returns: %v", err)
	}
	if open != 1 {
		t.Errorf("%d open returns survived, want 1", open)
	}
}

// A blank reason is legal (§19 I); a request with no lines is not a return.
func TestOpenRefusesAnEmptyRequest(t *testing.T) {
	ctx := t.Context()
	s := returns.NewStore(pool)
	number, lineID := shippedOrder(t, 2, 2)
	id := lineID.String()

	tests := []struct {
		name string
		req  *returns.Request
	}{
		{"no lines", &returns.Request{Reason: "不合用", Lines: map[string]int32{}}},
		{"all zero", &returns.Request{Reason: "不合用", Lines: map[string]int32{id: 0}}},
		{"negative", &returns.Request{Reason: "不合用", Lines: map[string]int32{id: -1}}},
		{"unknown line", &returns.Request{Reason: "不合用", Lines: map[string]int32{
			"00000000-0000-4000-8000-000000000000": 1}}},
		{"control character in reason", &returns.Request{
			Reason: "不合用\x00", Lines: map[string]int32{id: 1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.Open(ctx, number, uuid.NullUUID{}, tt.req); !errors.Is(err, returns.ErrInvalid) {
				t.Errorf("got %v, want ErrInvalid", err)
			}
		})
	}
}

func TestBlankReasonIsNotBlamedWhenNoItemWasChosen(t *testing.T) {
	ctx := t.Context()
	number, _ := shippedOrder(t, 2, 2)
	h := returns.NewHandler(returns.NewStore(pool), returnPlacedHere{},
		slog.New(slog.DiscardHandler), false)
	form := url.Values{"reason": {""}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/orders/"+number+"/return", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("number", number)
	res := httptest.NewRecorder()

	h.Submit(res, req)

	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blank reason/no items answered %d, want 422; body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, i18n.T(ctx, i18n.KeyReturnInvalid)) {
		t.Error("the refusal does not ask the customer to choose an item")
	}
	for _, contradicted := range []string{"請填寫退貨原因", "Give a reason"} {
		if strings.Contains(body, contradicted) {
			t.Errorf("the refusal still requires a legally optional reason: %q", contradicted)
		}
	}
}

// TestAReturnNeedsNoReason holds Consumer Protection Act §19 I: rescinding
// inside seven days needs no reason, and §19 V makes that unwaivable.
func TestAReturnNeedsNoReason(t *testing.T) {
	ctx := t.Context()
	s := returns.NewStore(pool)

	for _, reason := range []string{"", "   \t "} {
		number, lineID := shippedOrder(t, 2, 2)
		req := &returns.Request{
			Reason: reason,
			Lines:  map[string]int32{lineID.String(): 1},
		}
		if err := s.Open(ctx, number, uuid.NullUUID{}, req); err != nil {
			t.Errorf("a return with reason %q was refused: %v — §19 I needs none, "+
				"and the page says so", reason, err)
		}
	}
}

// TestReasonIsBoundedInRunesNotBytes proves the length limit counts characters,
// so a Chinese customer is not cut off at a third of an English one's room.
func TestReasonIsBoundedInRunesNotBytes(t *testing.T) {
	ctx := t.Context()
	s := returns.NewStore(pool)
	number, lineID := shippedOrder(t, 2, 2)
	id := lineID.String()

	// 500 runes, 1500 bytes: at the limit, so accepted.
	atLimit := strings.Repeat("退", returns.MaxReasonRunes)
	if err := s.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
		Reason: atLimit, Lines: map[string]int32{id: 1},
	}); err != nil {
		t.Errorf("a %d-rune reason was refused: %v", returns.MaxReasonRunes, err)
	}

	number2, lineID2 := shippedOrder(t, 2, 2)
	if err := s.Open(ctx, number2, uuid.NullUUID{}, &returns.Request{
		Reason: atLimit + "退", Lines: map[string]int32{lineID2.String(): 1},
	}); !errors.Is(err, returns.ErrInvalid) {
		t.Errorf("a %d-rune reason was accepted, want ErrInvalid", returns.MaxReasonRunes+1)
	}
}
