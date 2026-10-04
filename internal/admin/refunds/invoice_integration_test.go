//go:build integration

package refunds_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/outbox"
)

func fakeECPay(t *testing.T, issuedAt time.Time) (*invoice.FakeECPay, *invoice.Store) {
	t.Helper()
	fake, err := invoice.NewFakeECPay(issuedAt)
	if err != nil {
		t.Fatalf("fake ECPay: %v", err)
	}
	t.Cleanup(fake.Close)
	return fake, invoice.NewStore(pool, fake.Gateway())
}

// capturedOrderOwingAnInvoice is a card-captured, unshipped order whose
// 統一發票 the system has claimed and not yet sent.
func capturedOrderOwingAnInvoice(t *testing.T, ctx context.Context, invoices *invoice.Store) (string, uuid.UUID) {
	t.Helper()
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, true)
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_preferences (order_id, invoice_type, customer_name, customer_email)
		VALUES ($1, 'member_carrier', '買受人', 'buyer@example.com')`, orderID); err != nil {
		t.Fatalf("record invoice preference: %v", err)
	}
	if err := invoices.ClaimDue(ctx, &outbox.InvoiceDue{OrderNumber: number, Trigger: "evt_" + number}); err != nil {
		t.Fatalf("claim the invoice due: %v", err)
	}
	return number, orderID
}

func issueNow(t *testing.T, ctx context.Context, invoices *invoice.Store, orderID uuid.UUID) {
	t.Helper()
	var operationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT id FROM invoice_operations WHERE order_id = $1 AND kind = 'issue'`,
		orderID).Scan(&operationID); err != nil {
		t.Fatalf("read the system issue: %v", err)
	}
	if err := invoices.ProcessOperation(ctx, operationID); err != nil {
		t.Fatalf("issue the system claim: %v", err)
	}
}

func pressRefund(t *testing.T, ctx context.Context, s *refunds.Store, number string) string {
	t.Helper()
	h := refunds.NewHandler(s, nil, slog.New(slog.DiscardHandler))
	form := url.Values{"confirm": {"refund"}, "total": {"500000"}, "reason": {"顧客來電要求取消"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/orders/"+number+"/refund", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("number", number)
	rec := httptest.NewRecorder()
	h.RefundBeforeShipment(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("refund before shipment = %d, want 303", rec.Code)
	}
	return rec.Header().Get("Location")
}

// TestRefundBeforeShipmentVoidsTheInvoiceBeforeThePayout: inside ECPay's void
// window the staff member's press voids the system's invoice, under their own
// name, before any money moves, and cancels the order in the same press.
func TestRefundBeforeShipmentVoidsTheInvoiceBeforeThePayout(t *testing.T) {
	for _, inFlight := range []bool{false, true} {
		name := "issued"
		if inFlight {
			name = "issue in flight"
		}
		t.Run(name, func(t *testing.T) {
			ctx, staff := admintest.StaffContext(t, pool)
			fake, invoices := fakeECPay(t, time.Now())
			var sent atomic.Int64
			s := refunds.NewStore(pool, admintest.Refunder{Sent: &sent}, invoices)
			number, orderID := capturedOrderOwingAnInvoice(t, ctx, invoices)
			if !inFlight {
				issueNow(t, ctx, invoices, orderID)
			}

			if got := pressRefund(t, ctx, s, number); got != "/admin/orders/"+number+"?refunded=1" {
				t.Fatalf("one press redirected to %s, want the refund done", got)
			}
			if got := admintest.FulfillmentOf(t, pool, orderID); got != "cancelled" || sent.Load() != 1 {
				t.Fatalf("order %s after %d card refunds, want cancelled after one", got, sent.Load())
			}
			var voids int
			var byStaff bool
			var voided, refunded *time.Time
			if err := pool.QueryRow(ctx, `
				SELECT count(*),
				       coalesce(bool_and(op.status = 'succeeded' AND op.actor_kind = 'staff'
				                         AND op.actor_user_id = $2
				                         AND op.request_payload ->> 'reason' = '訂單取消'), false),
				       max(op.completed_at),
				       (SELECT min(f.created_at) FROM refunds f
				        JOIN return_requests r ON r.id = f.return_request_id WHERE r.order_id = $1)
				FROM invoice_operations op WHERE op.order_id = $1 AND op.kind = 'void'`,
				orderID, staff).Scan(&voids, &byStaff, &voided, &refunded); err != nil {
				t.Fatalf("read the void: %v", err)
			}
			if voids != 1 || !byStaff || fake.Calls("/B2CInvoice/Invalid") != 1 {
				t.Fatalf("%d void operations (by staff: %v), %d Invalid calls; want one by the staff member",
					voids, byStaff, fake.Calls("/B2CInvoice/Invalid"))
			}
			if voided == nil || refunded == nil || !voided.Before(*refunded) {
				t.Errorf("void settled at %v, card refund at %v; want the void first", voided, refunded)
			}
			if inFlight && fake.Calls("/B2CInvoice/Issue") != 1 {
				t.Errorf("the issue in flight was sent %d times, want once before its void",
					fake.Calls("/B2CInvoice/Issue"))
			}
		})
	}
}

// TestRefundBeforeShipmentLeavesAnInvoicePastTheVoidWindow: once the period is
// filed ECPay refuses a void, so none is asked for; the refund still goes out.
func TestRefundBeforeShipmentLeavesAnInvoicePastTheVoidWindow(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	fake, invoices := fakeECPay(t, time.Now().AddDate(0, -4, 0))
	var sent atomic.Int64
	s := refunds.NewStore(pool, admintest.Refunder{Sent: &sent}, invoices)
	number, orderID := capturedOrderOwingAnInvoice(t, ctx, invoices)
	issueNow(t, ctx, invoices, orderID)

	if got := pressRefund(t, ctx, s, number); got != "/admin/orders/"+number+"?cancelinvoice=1" {
		t.Fatalf("press redirected to %s, want the invoice left to staff", got)
	}
	var voids int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM invoice_operations WHERE order_id = $1 AND kind = 'void'`,
		orderID).Scan(&voids); err != nil {
		t.Fatalf("count voids: %v", err)
	}
	if voids != 0 || fake.Calls("/B2CInvoice/Invalid") != 0 || sent.Load() != 1 {
		t.Errorf("%d voids claimed, %d Invalid calls, %d card refunds; want no void and the refund",
			voids, fake.Calls("/B2CInvoice/Invalid"), sent.Load())
	}
	if got := admintest.FulfillmentOf(t, pool, orderID); got == "cancelled" {
		t.Errorf("order cancelled with its invoice live")
	}
}

// TestRefundBeforeShipmentWithdrawsAnIssueNothingWillSend: with no 加值中心 the
// system's claim would wait for ever and hold the cancellation with it.
func TestRefundBeforeShipmentWithdrawsAnIssueNothingWillSend(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	invoices := invoice.NewStore(pool, &invoice.Gateway{})
	s := refunds.NewStore(pool, admintest.Refunder{}, invoices)
	number, orderID := capturedOrderOwingAnInvoice(t, ctx, invoices)

	if got := pressRefund(t, ctx, s, number); got != "/admin/orders/"+number+"?refunded=1" {
		t.Fatalf("press redirected to %s, want the refund done", got)
	}
	var status, category string
	if err := pool.QueryRow(ctx, `
		SELECT status, coalesce(last_error, '') FROM invoice_operations
		WHERE order_id = $1 AND kind = 'issue'`, orderID).Scan(&status, &category); err != nil {
		t.Fatalf("read the issue: %v", err)
	}
	if status != "rejected" || category != "issue_withdrawn_order_cancelled" {
		t.Errorf("issue is %s (%s), want rejected as withdrawn", status, category)
	}
	if got := admintest.FulfillmentOf(t, pool, orderID); got != "cancelled" {
		t.Errorf("order is %s, want cancelled in one press", got)
	}
}
