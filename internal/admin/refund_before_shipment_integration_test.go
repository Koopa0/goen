//go:build integration

package admin_test

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/admin/refundstate"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestPaidOrderCannotBeCancelledDirectly(t *testing.T) {
	ctx, _ := staffContext(t)
	refunder := admintest.Refunder{}
	s := admin.NewStore(pool, refunder, nil, nil)
	refund := refunds.NewStore(pool, refunder)
	h := adminHandlerOver(s)

	for _, picking := range []bool{false, true} {
		t.Run(fmt.Sprintf("picking=%t", picking), func(t *testing.T) {
			number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, picking)

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
			if got := admintest.FulfillmentOf(t, pool, orderID); got == "cancelled" {
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
		if got := admintest.FulfillmentOf(t, pool, orderID); got != "cancelled" {
			t.Fatalf("unpaid order is %s after cancel", got)
		}
	})

	// The database admits the cancel once a refund before shipment has settled
	// and the invoice is resolved; the status form still must not finish it.
	t.Run("settled refund still goes through Resume", func(t *testing.T) {
		number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, true)
		if _, err := pool.Exec(ctx, `
			INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
			VALUES ($1, 'invoice', $2, 500000)`, orderID, uuid.NewString()[:32]); err != nil {
			t.Fatalf("issue invoice: %v", err)
		}
		if _, err := refund.RefundBeforeShipment(ctx, number, "顧客取消"); constraintFrom(err) != "orders_cancel_invoice_resolved" {
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
		if got := admintest.FulfillmentOf(t, pool, orderID); got != "picking" {
			t.Fatalf("order is %s, want picking until Resume", got)
		}
		if _, err := refund.RefundBeforeShipment(ctx, number, ""); err != nil {
			t.Fatalf("resume: %v", err)
		}
		if got := admintest.FulfillmentOf(t, pool, orderID); got != "cancelled" {
			t.Fatalf("order is %s after Resume", got)
		}
	})
}

// A pending order store credit paid in full is not committed in the database,
// yet cancelling it by status would return the credit with no confirmation.
func TestAPendingOrderPaidWholeWithCreditIsNotCancelledByStatus(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, admintest.Refunder{}, nil, nil)
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 0, 500000, false)

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("read order: %v", err)
	}
	if !view.Funded || view.Committed {
		t.Fatalf("Funded=%t Committed=%t, want a funded order the database has not committed", view.Funded, view.Committed)
	}
	if view.CreditCents != 500000 || view.OwedCents != 0 {
		t.Errorf("credit %d owed %d, want 500000 and 0", view.CreditCents, view.OwedCents)
	}
	if want := i18n.T(ctx, i18n.KeyAdminPayMethodCredit); view.Payment.Method != want {
		t.Errorf("payment = %q, want %q", view.Payment.Method, want)
	}
	for _, n := range view.Next {
		if n.Value == pages.FulfillmentCancelled {
			t.Error("the status menu offers to cancel a funded order")
		}
	}

	if _, err := s.Advance(ctx, number, pages.FulfillmentCancelled, uuid.NullUUID{}); !errors.Is(err, admin.ErrPaidCancel) {
		t.Fatalf("status cancel of a credit-funded order = %v, want ErrPaidCancel", err)
	}
	if got := admintest.FulfillmentOf(t, pool, orderID); got != "pending" {
		t.Errorf("order is %s, want pending", got)
	}
}

func TestRefundBeforeShipmentStaysOpenUntilRefundSettles(t *testing.T) {
	ctx, staff := staffContext(t)
	actor := uuid.NullUUID{UUID: staff, Valid: true}

	for _, tc := range []struct {
		name    string
		picking bool
		first   admintest.Refunder
		want    error
	}{
		{name: "pending at Stripe", first: admintest.Refunder{State: refundstate.Pending}, want: refunds.ErrUnsettled},
		{name: "refused by Stripe", picking: true, first: admintest.Refunder{State: refundstate.Failed}, want: refundstate.ErrIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, tc.picking)
			s := admin.NewStore(pool, tc.first, nil, nil)
			refund := refunds.NewStore(pool, tc.first)
			if _, err := refund.RefundBeforeShipment(ctx, number, "顧客取消"); !errors.Is(err, tc.want) {
				t.Fatalf("first press = %v, want %v", err, tc.want)
			}
			if got := admintest.FulfillmentOf(t, pool, orderID); got == "cancelled" {
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

			if _, err := refunds.NewStore(pool, admintest.Refunder{}).RefundBeforeShipment(ctx, number, ""); err != nil {
				t.Fatalf("resume: %v", err)
			}
			if got := admintest.FulfillmentOf(t, pool, orderID); got != "cancelled" {
				t.Fatalf("order is %s after Resume, want cancelled", got)
			}
		})
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
	refunder := admintest.Refunder{}
	s := admin.NewStore(pool, refunder, nil, nil)
	refund := refunds.NewStore(pool, refunder)

	for _, doorFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("door-first=%t", doorFirst), func(t *testing.T) {
			number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, true)
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
					_, refundErr := refund.RefundBeforeShipment(ctx, number, "顧客取消")
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
			var shipments, requests int
			if err := pool.QueryRow(ctx, `
				SELECT (SELECT count(*) FROM order_shipments WHERE order_id = $1),
				       (SELECT count(*) FROM return_requests WHERE order_id = $1)`,
				orderID).Scan(&shipments, &requests); err != nil {
				t.Fatalf("read outcome: %v", err)
			}
			if doorFirst && (shipments != 0 || requests != 1) || !doorFirst && (shipments != 1 || requests != 0) {
				t.Errorf("shipments/refunds = %d/%d; exactly the first writer's must stand", shipments, requests)
			}
		})
	}
}

// TestRefundBeforeShipmentNoticesNameTheNextStep follows each redirect to the
// order page in both languages.
func TestRefundBeforeShipmentNoticesNameTheNextStep(t *testing.T) {
	ctx, _ := staffContext(t)
	refunder := admintest.Refunder{}
	h := adminHandlerOver(admin.NewStore(pool, refunder, nil, nil))
	door := refunds.NewHandler(refunds.NewStore(pool, refunder), nil, slog.New(slog.DiscardHandler))
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 300000, 0, true)
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
	door.RefundBeforeShipment(rec, req)
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
