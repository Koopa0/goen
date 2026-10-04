//go:build integration

package refunds_test

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestRefundBeforeShipmentPaysEveryLegAndCancels(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	var sent atomic.Int64
	s := refunds.NewStore(pool, admintest.Refunder{Sent: &sent}, nil)
	number, orderID, variantID := admintest.PaidUnshippedOrder(t, pool, 900000, 300000, true)

	var stockHeld int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, variantID).Scan(&stockHeld); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	var awarded int64
	if err := pool.QueryRow(ctx, `
		SELECT points FROM loyalty_entries WHERE order_id = $1 AND kind = 'award'`,
		orderID).Scan(&awarded); err != nil || awarded <= 0 {
		t.Fatalf("read award = %d, %v; the fixture must carry points to claw back", awarded, err)
	}

	for press := range 2 {
		if _, err := s.RefundBeforeShipment(ctx, number, "顧客取消"); err != nil {
			t.Fatalf("press %d: %v", press+1, err)
		}
	}
	admintest.AssertTerminalNotice(t, pool, orderID, email.TerminalCancelledByStaff, true)

	var returnID uuid.UUID
	var returnStatus string
	if err := pool.QueryRow(ctx, `
		SELECT id, status FROM return_requests WHERE order_id = $1 AND before_shipment`,
		orderID).Scan(&returnID, &returnStatus); err != nil {
		t.Fatalf("read refund: %v", err)
	}
	var card, cardRows, credit, reversals, clawed, clawbacks, cancelClawbacks,
		refundedEvents, cancelledEvents, held, released, releaseMoves, restocks int64
	var creditOrder uuid.NullUUID
	var stockAfter int32
	if err := pool.QueryRow(ctx, `
		SELECT
		  (SELECT coalesce(sum(amount_cents), 0) FROM refunds
		   WHERE return_request_id = $2 AND status = 'succeeded'),
		  (SELECT count(*) FROM refunds WHERE return_request_id = $2),
		  (SELECT coalesce(sum(amount_cents), 0) FROM store_credit_entries
		   WHERE idempotency_key = 'return-credit:' || $2::text),
		  (SELECT order_id FROM store_credit_entries
		   WHERE idempotency_key = 'return-credit:' || $2::text),
		  (SELECT count(*) FROM store_credit_entries r
		   JOIN store_credit_entries s ON s.id = r.reverses_id WHERE s.order_id = $1),
		  (SELECT coalesce(sum(points), 0) FROM loyalty_entries
		   WHERE return_request_id = $2 AND kind = 'clawback'),
		  (SELECT count(*) FROM loyalty_entries WHERE order_id = $1 AND kind = 'clawback'),
		  (SELECT count(*) FROM loyalty_entries WHERE idempotency_key = 'cancel:' || $1::text),
		  (SELECT count(*) FROM order_events
		   WHERE order_id = $1 AND kind = 'refunded' AND return_request_id = $2),
		  (SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'cancelled'),
		  (SELECT count(*) FROM inventory_reservations WHERE order_id = $1 AND state = 'held'),
		  (SELECT count(*) FROM inventory_reservations WHERE order_id = $1 AND state = 'released'),
		  (SELECT count(*) FROM inventory_movements m JOIN inventory_reservations r
		     ON m.source_type = 'reservation' AND m.source_id = r.id
		   WHERE r.order_id = $1 AND m.reason = 'release'),
		  (SELECT count(*) FROM inventory_movements
		   WHERE source_type = 'return_request' AND source_id = $2),
		  (SELECT stock_quantity FROM product_variants WHERE id = $3)`,
		orderID, returnID, variantID).Scan(&card, &cardRows, &credit, &creditOrder, &reversals,
		&clawed, &clawbacks, &cancelClawbacks, &refundedEvents, &cancelledEvents,
		&held, &released, &releaseMoves, &restocks, &stockAfter); err != nil {
		t.Fatalf("read refund legs: %v", err)
	}

	if got := admintest.FulfillmentOf(t, pool, orderID); got != "cancelled" || returnStatus != "completed" {
		t.Errorf("order/refund = %s/%s, want cancelled/completed", got, returnStatus)
	}
	if card != 900000 || cardRows != 1 || sent.Load() != 1 {
		t.Errorf("card refund %d in %d rows after %d provider calls, want 900000 once", card, cardRows, sent.Load())
	}
	if credit != 300000 || creditOrder.UUID != orderID {
		t.Errorf("credit refund %d on order %v, want 300000 posted with the order", credit, creditOrder)
	}
	if reversals != 0 {
		t.Errorf("%d spend reversals as well as the return's credit, want none", reversals)
	}
	if clawed != -awarded || clawbacks != 1 || cancelClawbacks != 0 {
		t.Errorf("clawed %d of %d in %d rows (%d cancel rows), want the award once through the return",
			clawed, awarded, clawbacks, cancelClawbacks)
	}
	if refundedEvents != 1 || cancelledEvents != 1 {
		t.Errorf("refunded/cancelled events = %d/%d, want 1/1", refundedEvents, cancelledEvents)
	}
	if held != 0 || released != 1 || releaseMoves != 1 || restocks != 0 || stockAfter != stockHeld+1 {
		t.Errorf("stock held/released/moves/restocks = %d/%d/%d/%d, %d -> %d; want the hold released once",
			held, released, releaseMoves, restocks, stockHeld, stockAfter)
	}
}

func TestRefundBeforeShipmentWaitsForInvoiceCorrection(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := refunds.NewStore(pool, admintest.Refunder{}, nil)

	// invoicedRefund is a paid, unshipped order whose 統一發票 is live when its
	// refund settles, so the first press stops at the invoice.
	invoicedRefund := func(t *testing.T) (string, uuid.UUID) {
		t.Helper()
		number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, true)
		if _, err := pool.Exec(ctx, `
			INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
			VALUES ($1, 'invoice', $2, 500000)`, orderID, uuid.NewString()[:32]); err != nil {
			t.Fatalf("issue invoice: %v", err)
		}
		if _, err := s.RefundBeforeShipment(ctx, number, "顧客取消"); !pgerr.IsConstraint(err, "orders_cancel_invoice_resolved") {
			t.Fatalf("refund with a live invoice = %v", err)
		}
		if got := admintest.FulfillmentOf(t, pool, orderID); got != "picking" {
			t.Fatalf("order is %s with its invoice live, want picking", got)
		}
		return number, orderID
	}
	allowance := func(t *testing.T, orderID uuid.UUID, cents int64) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO invoice_documents (order_id, kind, original_id, number, amount_cents)
			SELECT order_id, 'allowance', id, $2, $3 FROM invoice_documents
			WHERE order_id = $1 AND kind = 'invoice'`, orderID, uuid.NewString()[:32], cents); err != nil {
			t.Fatalf("file allowance: %v", err)
		}
	}
	resume := func(t *testing.T, number string, orderID uuid.UUID, allowed bool) {
		t.Helper()
		_, err := s.RefundBeforeShipment(ctx, number, "")
		if allowed {
			if err != nil || admintest.FulfillmentOf(t, pool, orderID) != "cancelled" {
				t.Fatalf("resolved invoice: %v, order %s", err, admintest.FulfillmentOf(t, pool, orderID))
			}
			return
		}
		if !pgerr.IsConstraint(err, "orders_cancel_invoice_resolved") || admintest.FulfillmentOf(t, pool, orderID) == "cancelled" {
			t.Fatalf("unresolved invoice let the order cancel: %v", err)
		}
	}

	for _, tc := range []struct {
		name                       string
		allowance                  int64
		voidInvoice, voidAllowance bool
		allowed                    bool
	}{
		{name: "live invoice"},
		{name: "partial allowance", allowance: 250000},
		{name: "full allowance", allowance: 500000, allowed: true},
		{name: "voided allowance", allowance: 500000, voidAllowance: true},
		{name: "voided invoice", voidInvoice: true, allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			number, orderID := invoicedRefund(t)
			if tc.allowance > 0 {
				allowance(t, orderID, tc.allowance)
			}
			if tc.voidInvoice || tc.voidAllowance {
				kind := "invoice"
				if tc.voidAllowance {
					kind = "allowance"
				}
				if _, err := pool.Exec(ctx, `
					UPDATE invoice_documents SET status = 'voided', voided_at = now()
					WHERE order_id = $1 AND kind = $2`, orderID, kind); err != nil {
					t.Fatalf("void %s: %v", kind, err)
				}
			}
			resume(t, number, orderID, tc.allowed)
		})
	}

	// A void the provider rejected leaves the whole refund to relieve, so a full
	// allowance resolves the invoice; one still pending or in attention does not.
	for _, kind := range []string{"issue", "void", "allowance"} {
		for _, status := range []string{"pending", "attention", "rejected"} {
			t.Run(kind+"/"+status, func(t *testing.T) {
				number, orderID := invoicedRefund(t)
				allowance(t, orderID, 500000)
				if _, err := pool.Exec(ctx, `
					INSERT INTO invoice_operations (order_id, kind, target_document_id, provider_key,
					    amount_cents, request_payload, actor_user_id, actor_id_snapshot, request_id,
					    status, available_at)
					SELECT d.order_id, $2, CASE WHEN $2 = 'issue' THEN NULL ELSE d.id END,
					       replace(o.order_number, '-', ''), d.amount_cents, '{}', $3, $3, $4, $5,
					       now() + interval '1 day'
					FROM invoice_documents d JOIN orders o ON o.id = d.order_id
					WHERE d.order_id = $1 AND d.kind = 'invoice'`,
					orderID, kind, staff, uuid.NewString(), status); err != nil {
					t.Fatalf("record %s %s operation: %v", kind, status, err)
				}
				resume(t, number, orderID, status == "rejected")
			})
		}
	}
}

func TestRefundBeforeShipmentConfirmsBeforeItPays(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	var sent atomic.Int64
	s := refunds.NewStore(pool, admintest.Refunder{Sent: &sent}, nil)
	h := refunds.NewHandler(s, nil, slog.New(slog.DiscardHandler))
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 400000, 0, true)

	post := func(form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/admin/orders/"+number+"/refund", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("number", number)
		rec := httptest.NewRecorder()
		h.RefundBeforeShipment(rec, req)
		return rec
	}
	nothingWritten := func(t *testing.T, step string) {
		t.Helper()
		var refunds int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM return_requests WHERE order_id = $1`, orderID).Scan(&refunds); err != nil {
			t.Fatalf("count refunds: %v", err)
		}
		if refunds != 0 || sent.Load() != 0 {
			t.Fatalf("%s wrote %d refunds and made %d provider calls", step, refunds, sent.Load())
		}
	}

	first := post(url.Values{})
	body := html.UnescapeString(first.Body.String())
	if first.Code != http.StatusOK || !strings.Contains(body, `name="confirm"`) ||
		!strings.Contains(body, pages.TWD(400000)) || !strings.Contains(body, `value="400000"`) {
		t.Fatalf("first POST = %d, want the confirmation showing the total: %s", first.Code, body)
	}
	nothingWritten(t, "the first POST")

	stale := post(url.Values{"confirm": {"refund"}, "total": {"1"}, "reason": {"顧客取消"}})
	if stale.Code != http.StatusOK || !strings.Contains(stale.Body.String(), `value="400000"`) {
		t.Fatalf("stale total = %d, want the confirmation again", stale.Code)
	}
	nothingWritten(t, "a stale total")

	blank := post(url.Values{"confirm": {"refund"}, "total": {"400000"}, "reason": {"  "}})
	if blank.Code != http.StatusUnprocessableEntity || !strings.Contains(blank.Body.String(), `aria-invalid="true"`) {
		t.Fatalf("blank reason = %d, want 422 marking the reason", blank.Code)
	}
	nothingWritten(t, "a blank reason")

	done := post(url.Values{"confirm": {"refund"}, "total": {"400000"}, "reason": {"顧客取消"}})
	if done.Code != http.StatusSeeOther || done.Header().Get("Location") != "/admin/orders/"+number+"?refunded=1" {
		t.Fatalf("confirmed POST = %d %s", done.Code, done.Header().Get("Location"))
	}
	if got := admintest.FulfillmentOf(t, pool, orderID); got != "cancelled" || sent.Load() != 1 {
		t.Fatalf("order %s after %d provider calls, want cancelled after one", got, sent.Load())
	}
}

// TestAdminCancelClawsBackLoyaltyPoints holds that a paid order the back office
// cancels — by refunding it before shipment — claws back its award lot,
// including when the lot was partly or wholly spent before cancellation.
func TestAdminCancelClawsBackLoyaltyPoints(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := refunds.NewStore(pool, admintest.Refunder{}, nil)

	t.Run("untouched lot", func(t *testing.T) {
		userID := admintest.Customer(t, pool)
		number, orderID := admintest.PaidPickingOrderForUser(t, pool, userID, 1200000)
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
			userID := admintest.Customer(t, pool)
			number, orderID := admintest.PaidPickingOrderForUser(t, pool, userID, 1200000)
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
		userID := admintest.Customer(t, pool)
		_, pickingID := admintest.PaidPickingOrderForUser(t, pool, userID, 500000)
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
