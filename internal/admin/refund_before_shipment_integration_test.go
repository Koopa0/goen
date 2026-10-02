//go:build integration

package admin_test

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ordernotice"
	"github.com/koopa0/goen/internal/ui/pages"
)

// paidUnshippedOrder is a customer's order for one unit, paid with card and
// store credit, holding its stock and awarded its points. picking moves it on;
// otherwise it is paid and still pending, which is committed all the same.
func paidUnshippedOrder(t *testing.T, cardCents, creditCents int64, picking bool) (number string, orderID, variantID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	userID := creditedAccount(t, creditCents)
	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.is_active AND p.status = 'active' AND pv.stock_quantity - pv.safety_stock > 2
		LIMIT 1`).Scan(&variantID); err != nil {
		t.Fatalf("find variant: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 6000
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, $3, 1
		FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $2`,
		orderID, variantID, cardCents+creditCents-6000); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'before-shipment@example.com', '收件人', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		orderID, variantID, "before-shipment:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if creditCents > 0 {
		if _, err := tx.Exec(ctx, `SELECT spend_store_credit($1, $2)`, orderID, -creditCents); err != nil {
			t.Fatalf("spend credit: %v", err)
		}
	}
	if cardCents > 0 {
		session := "cs_before_shipment_" + number
		if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`, orderID, session, cardCents); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`, session, cardCents); err != nil {
			t.Fatalf("capture: %v", err)
		}
	}
	if picking {
		if _, err := tx.Exec(ctx,
			`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
			t.Fatalf("to picking: %v", err)
		}
	}
	if _, err := tx.Exec(ctx, `SELECT award_loyalty_points($1)`, orderID); err != nil {
		t.Fatalf("award: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, orderID, variantID
}

func fulfillmentOf(t *testing.T, orderID uuid.UUID) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(t.Context(),
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return status
}

func TestPaidOrderCannotBeCancelledDirectly(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	h := adminHandlerOver(pool, s)

	for _, picking := range []bool{false, true} {
		t.Run(fmt.Sprintf("picking=%t", picking), func(t *testing.T) {
			number, orderID, _ := paidUnshippedOrder(t, 500000, 0, picking)

			_, err := s.Advance(ctx, number, pages.FulfillmentCancelled, uuid.NullUUID{})
			if constraintFrom(err) != "orders_paid_cancel_needs_refund" {
				t.Fatalf("Advance cancelled a paid order: %v", err)
			}
			err = asAdmin(ctx, t, `UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
			                       WHERE order_number = '`+number+`'`)
			if constraintFrom(err) != "orders_paid_cancel_needs_refund" {
				t.Fatalf("the admin role cancelled a paid order directly: %v", err)
			}

			form := url.Values{"status": {"cancelled"}}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost,
				"/admin/orders/"+number+"/status", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("number", number)
			rec := httptest.NewRecorder()
			h.AdvanceOrder(rec, req)
			if rec.Code != http.StatusSeeOther ||
				rec.Header().Get("Location") != "/admin/orders/"+number+"?paidcancel=1" {
				t.Fatalf("paid cancel = %d %s, want the refund notice", rec.Code, rec.Header().Get("Location"))
			}
			if got := fulfillmentOf(t, orderID); got == "cancelled" {
				t.Fatal("the paid order was cancelled")
			}

			view, err := s.Order(ctx, number)
			if err != nil {
				t.Fatalf("read order: %v", err)
			}
			for _, n := range view.Next {
				if n.Value == pages.FulfillmentCancelled {
					t.Error("the status form still offers cancelling a paid order")
				}
			}
			if !view.RefundOffered || view.RefundOpen {
				t.Errorf("refund offered/open = %t/%t, want true/false", view.RefundOffered, view.RefundOpen)
			}
		})
	}

	t.Run("unpaid cancel is unchanged", func(t *testing.T) {
		number, orderID, _ := pendingOrderHoldingStock(t)
		view, err := s.Order(ctx, number)
		if err != nil {
			t.Fatalf("read order: %v", err)
		}
		if view.RefundOffered {
			t.Error("an unpaid order offers a refund")
		}
		if _, err := s.Advance(ctx, number, pages.FulfillmentCancelled, uuid.NullUUID{}); err != nil {
			t.Fatalf("cancel unpaid order: %v", err)
		}
		if got := fulfillmentOf(t, orderID); got != "cancelled" {
			t.Fatalf("unpaid order is %s after cancel", got)
		}
	})

	// The database admits the cancel once a refund before shipment has settled
	// and the invoice is resolved; the status form still must not finish it.
	t.Run("settled refund still goes through Resume", func(t *testing.T) {
		number, orderID, _ := paidUnshippedOrder(t, 500000, 0, true)
		if _, err := pool.Exec(ctx, `
			INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
			VALUES ($1, 'invoice', $2, 500000)`, orderID, uuid.NewString()[:32]); err != nil {
			t.Fatalf("issue invoice: %v", err)
		}
		if _, err := s.RefundBeforeShipment(ctx, number, "顧客取消"); constraintFrom(err) != "orders_cancel_invoice_resolved" {
			t.Fatalf("refund with a live invoice = %v", err)
		}
		if _, err := pool.Exec(ctx, `
			UPDATE invoice_documents SET status = 'voided', voided_at = now()
			WHERE order_id = $1`, orderID); err != nil {
			t.Fatalf("void invoice: %v", err)
		}
		if _, err := s.Advance(ctx, number, pages.FulfillmentCancelled, uuid.NullUUID{}); !errors.Is(err, admin.ErrPaidCancel) {
			t.Fatalf("status form cancelled a refunded order: %v", err)
		}
		if got := fulfillmentOf(t, orderID); got != "picking" {
			t.Fatalf("order is %s, want picking until Resume", got)
		}
		if _, err := s.RefundBeforeShipment(ctx, number, ""); err != nil {
			t.Fatalf("resume: %v", err)
		}
		if got := fulfillmentOf(t, orderID); got != "cancelled" {
			t.Fatalf("order is %s after Resume", got)
		}
	})
}

func TestRefundBeforeShipmentPaysEveryLegAndCancels(t *testing.T) {
	ctx, _ := staffContext(t)
	var sent atomic.Int64
	s := admin.NewStore(pool, fakeRefunder{sent: &sent}, nil, nil)
	number, orderID, variantID := paidUnshippedOrder(t, 900000, 300000, true)

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
	assertTerminalNotice(t, orderID, ordernotice.CancelledByStaff, true)

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

	if got := fulfillmentOf(t, orderID); got != "cancelled" || returnStatus != "completed" {
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

func TestRefundBeforeShipmentStaysOpenUntilRefundSettles(t *testing.T) {
	ctx, staff := staffContext(t)
	actor := uuid.NullUUID{UUID: staff, Valid: true}

	for _, tc := range []struct {
		name    string
		picking bool
		first   fakeRefunder
		want    error
	}{
		{name: "pending at Stripe", first: fakeRefunder{state: admin.RefundPending}, want: admin.ErrRefundUnsettled},
		{name: "refused by Stripe", picking: true, first: fakeRefunder{state: admin.RefundFailed}, want: admin.ErrRefundIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			number, orderID, _ := paidUnshippedOrder(t, 500000, 0, tc.picking)
			s := admin.NewStore(pool, tc.first, nil, nil)
			if _, err := s.RefundBeforeShipment(ctx, number, "顧客取消"); !errors.Is(err, tc.want) {
				t.Fatalf("first press = %v, want %v", err, tc.want)
			}
			if got := fulfillmentOf(t, orderID); got == "cancelled" {
				t.Fatal("the order was cancelled before its refund settled")
			}

			if tc.picking {
				err := s.Ship(ctx, number, admin.Dispatch{Carrier: "black_cat", Tracking: "TW-BS-" + number}, actor)
				if constraintFrom(err) != "orders_refunded_before_shipment" {
					t.Fatalf("shipped an order being refunded: %v", err)
				}
			} else {
				_, err := s.Advance(ctx, number, pages.FulfillmentPicking, actor)
				if constraintFrom(err) != "orders_refunded_before_shipment" {
					t.Fatalf("picked an order being refunded: %v", err)
				}
			}
			view, err := s.Order(ctx, number)
			if err != nil {
				t.Fatalf("read order: %v", err)
			}
			if !view.RefundOpen || view.CanShip || len(view.Next) != 0 {
				t.Errorf("open/ship/next = %t/%t/%v, want only Resume", view.RefundOpen, view.CanShip, view.Next)
			}

			if _, err := admin.NewStore(pool, fakeRefunder{}, nil, nil).RefundBeforeShipment(ctx, number, ""); err != nil {
				t.Fatalf("resume: %v", err)
			}
			if got := fulfillmentOf(t, orderID); got != "cancelled" {
				t.Fatalf("order is %s after Resume, want cancelled", got)
			}
		})
	}
}

func TestRefundBeforeShipmentWaitsForInvoiceCorrection(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	// invoicedRefund is a paid, unshipped order whose 統一發票 is live when its
	// refund settles, so the first press stops at the invoice.
	invoicedRefund := func(t *testing.T) (string, uuid.UUID) {
		t.Helper()
		number, orderID, _ := paidUnshippedOrder(t, 500000, 0, true)
		if _, err := pool.Exec(ctx, `
			INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
			VALUES ($1, 'invoice', $2, 500000)`, orderID, uuid.NewString()[:32]); err != nil {
			t.Fatalf("issue invoice: %v", err)
		}
		if _, err := s.RefundBeforeShipment(ctx, number, "顧客取消"); constraintFrom(err) != "orders_cancel_invoice_resolved" {
			t.Fatalf("refund with a live invoice = %v", err)
		}
		if got := fulfillmentOf(t, orderID); got != "picking" {
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
			if err != nil || fulfillmentOf(t, orderID) != "cancelled" {
				t.Fatalf("resolved invoice: %v, order %s", err, fulfillmentOf(t, orderID))
			}
			return
		}
		if constraintFrom(err) != "orders_cancel_invoice_resolved" || fulfillmentOf(t, orderID) == "cancelled" {
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

// waitBlockedBehind waits until a statement like pattern is waiting on holder's lock.
func waitBlockedBehind(t *testing.T, holder int, pattern string, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatalf("%s finished before it waited on backend %d", pattern, holder)
		default:
		}
		var blocked bool
		if err := pool.QueryRow(t.Context(), `
			SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			               WHERE query LIKE $2 AND $1 = ANY (pg_blocking_pids(pid)))`,
			holder, pattern).Scan(&blocked); err != nil {
			t.Fatalf("observe lock wait: %v", err)
		}
		if blocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never waited on backend %d", pattern, holder)
}

// TestRefundBeforeShipmentAndDispatchSerialize holds the order lock both
// writers take: whichever commits first, the other sees it.
func TestRefundBeforeShipmentAndDispatchSerialize(t *testing.T) {
	ctx, staff := staffContext(t)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	for _, doorFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("door-first=%t", doorFirst), func(t *testing.T) {
			number, orderID, _ := paidUnshippedOrder(t, 500000, 0, true)
			first, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = first.Rollback(context.WithoutCancel(ctx)) }()
			var firstPID int
			if pidErr := first.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&firstPID); pidErr != nil {
				t.Fatalf("read backend: %v", pidErr)
			}
			pattern := "%open_refund_before_shipment%"
			if doorFirst {
				_, err = first.Exec(ctx, `SELECT open_refund_before_shipment($1, '顧客取消', $2, 'serialize')`, number, staff)
				pattern = "%INSERT INTO order_shipments%"
			} else {
				_, err = first.Exec(ctx, `
					INSERT INTO order_shipments (order_id, carrier, tracking_number)
					VALUES ($1, 'black_cat', $2)`, orderID, "TW-FIRST-"+number)
			}
			if err != nil {
				t.Fatalf("first writer: %v", err)
			}

			result := make(chan error, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				if doorFirst {
					result <- s.Ship(ctx, number, admin.Dispatch{Carrier: "black_cat", Tracking: "TW-SECOND-" + number}, actor)
				} else {
					_, refundErr := s.RefundBeforeShipment(ctx, number, "顧客取消")
					result <- refundErr
				}
			}()
			waitBlockedBehind(t, firstPID, pattern, done)
			if err := first.Commit(ctx); err != nil {
				t.Fatalf("commit first writer: %v", err)
			}
			var second error
			select {
			case second = <-result:
			case <-time.After(10 * time.Second):
				t.Fatal("the second writer never finished")
			}

			want := "return_before_shipment_eligible"
			if doorFirst {
				want = "orders_refunded_before_shipment"
			}
			if constraintFrom(second) != want {
				t.Fatalf("second writer = %v, want %s", second, want)
			}
			var shipments, refunds int
			if err := pool.QueryRow(ctx, `
				SELECT (SELECT count(*) FROM order_shipments WHERE order_id = $1),
				       (SELECT count(*) FROM return_requests WHERE order_id = $1)`,
				orderID).Scan(&shipments, &refunds); err != nil {
				t.Fatalf("read outcome: %v", err)
			}
			if doorFirst && (shipments != 0 || refunds != 1) || !doorFirst && (shipments != 1 || refunds != 0) {
				t.Errorf("shipments/refunds = %d/%d; exactly the first writer's must stand", shipments, refunds)
			}
		})
	}
}

func TestRefundBeforeShipmentConfirmsBeforeItPays(t *testing.T) {
	ctx, _ := staffContext(t)
	var sent atomic.Int64
	s := admin.NewStore(pool, fakeRefunder{sent: &sent}, nil, nil)
	h := adminHandlerOver(pool, s)
	number, orderID, _ := paidUnshippedOrder(t, 400000, 0, true)

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
	if got := fulfillmentOf(t, orderID); got != "cancelled" || sent.Load() != 1 {
		t.Fatalf("order %s after %d provider calls, want cancelled after one", got, sent.Load())
	}
}

// TestRefundBeforeShipmentNoticesNameTheNextStep follows each redirect to the
// order page in both languages.
func TestRefundBeforeShipmentNoticesNameTheNextStep(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	h := adminHandlerOver(pool, s)
	number, orderID, _ := paidUnshippedOrder(t, 300000, 0, true)
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
		VALUES ($1, 'invoice', $2, 300000)`, orderID, uuid.NewString()[:32]); err != nil {
		t.Fatalf("issue invoice: %v", err)
	}
	form := url.Values{"confirm": {"refund"}, "total": {"300000"}, "reason": {"顧客取消"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/orders/"+number+"/refund", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("number", number)
	rec := httptest.NewRecorder()
	h.RefundBeforeShipment(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/orders/"+number+"?cancelinvoice=1" {
		t.Fatalf("refund with a live invoice = %d %s", rec.Code, rec.Header().Get("Location"))
	}

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		local := i18n.WithLocale(ctx, locale)
		get := httptest.NewRequestWithContext(local, http.MethodGet, rec.Header().Get("Location"), nil)
		get.SetPathValue("number", number)
		page := httptest.NewRecorder()
		h.Order(page, get)
		body := html.UnescapeString(page.Body.String())
		if page.Code != http.StatusOK ||
			!strings.Contains(body, i18n.T(local, i18n.KeyAdminNoticeCancelInvoice)) ||
			!strings.Contains(body, i18n.T(local, i18n.KeyAdminRefundResume)) {
			t.Fatalf("%s order page lacks the invoice step or Resume: %d", locale, page.Code)
		}
	}
}
