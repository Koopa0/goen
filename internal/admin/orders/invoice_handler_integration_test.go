//go:build integration

package orders_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/order/lapse"
	"github.com/koopa0/goen/internal/outbox"
)

// invoiceDesk is the order desk over the admin role's pool and a real
// issuer.Store, wired as cmd/goen wires it.
func invoiceDesk(t *testing.T, fake *issuer.FakeECPay) (http.Handler, *issuer.Store) {
	t.Helper()
	adminPool := admintest.AdminRolePool(t, pool)
	writer := issuer.NewStore(adminPool, fake.Gateway())
	store := admintest.OrderStore(adminPool, admintest.HealthyGateway(t), writer)
	mux := http.NewServeMux()
	admintest.OrderDesk(store).Routes(mux, admintest.BackOffice)
	return mux, writer
}

func newFakeECPay(t *testing.T) *issuer.FakeECPay {
	t.Helper()
	fake, err := issuer.NewFakeECPay(time.Now())
	if err != nil {
		t.Fatalf("fake ECPay: %v", err)
	}
	t.Cleanup(fake.Close)
	return fake
}

func providerCalls(fake *issuer.FakeECPay) int {
	return fake.Calls("/B2CInvoice/Issue") + fake.Calls("/B2CInvoice/GetIssue")
}

// TestHandler_IssueInvoiceRefusals: an unpaid or lapsed order, which the
// database refuses to claim, answers with the refused notice, and an order that
// does not exist with a 404, as a malformed number does. Neither reaches the
// provider or leaves an operation behind.
func TestHandler_IssueInvoiceRefusals(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	fake := newFakeECPay(t)
	desk, _ := invoiceDesk(t, fake)

	unpaid := admintest.PlaceUnpaidOrder(t, pool)
	lapsed := admintest.PlaceUnpaidOrderHolding(t, pool, true)
	if _, err := pool.Exec(ctx, `
		UPDATE inventory_reservations
		SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 minute'
		WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`, lapsed); err != nil {
		t.Fatalf("expire the hold: %v", err)
	}
	if _, _, err := lapse.NewSweeper(pool).Sweep(ctx, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT fulfillment_status FROM orders WHERE order_number = $1`, lapsed).
		Scan(&status); err != nil || status != "cancelled" {
		t.Fatalf("lapsed order is %q (%v), want cancelled", status, err)
	}
	const missing = "GO-991229-ZZZZZZ"

	for _, tc := range []struct {
		name         string
		number       string
		wantStatus   int
		wantLocation string
	}{
		{"unpaid", unpaid, http.StatusSeeOther, "/admin/orders/" + unpaid + "?refused=1"},
		{"lapsed and cancelled", lapsed, http.StatusSeeOther, "/admin/orders/" + lapsed + "?refused=1"},
		{"no such order", missing, http.StatusNotFound, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+tc.number+"/invoice", nil)
			res := httptest.NewRecorder()
			desk.ServeHTTP(res, req)
			if res.Code != tc.wantStatus || res.Header().Get("Location") != tc.wantLocation {
				t.Errorf("Issue(%s) = %d %q, want %d %q",
					tc.name, res.Code, res.Header().Get("Location"), tc.wantStatus, tc.wantLocation)
			}
			var operations int
			if err := pool.QueryRow(ctx, `
				SELECT count(*) FROM invoice_operations op
				JOIN orders o ON o.id = op.order_id WHERE o.order_number = $1`, tc.number).Scan(&operations); err != nil {
				t.Fatalf("count invoice operations: %v", err)
			}
			if operations != 0 {
				t.Errorf("refused Issue(%s) left %d invoice operations, want 0", tc.name, operations)
			}
		})
	}
	if got := providerCalls(fake); got != 0 {
		t.Errorf("refused issues made %d provider calls, want 0", got)
	}
}

// TestHandler_IssueInvoiceResumes: an issue already claimed is resumed, not
// refused, even when its order is cancelled by now. The claim looks for the
// operation before it asks whether the order can be invoiced.
func TestHandler_IssueInvoiceResumes(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	fake := newFakeECPay(t)
	desk, writer := invoiceDesk(t, fake)
	number, _ := admintest.PlaceOrderPaidByCredit(t, pool, 0)
	var orderID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatalf("read order: %v", err)
	}
	claimInvoice(t, writer, number, orderID)
	if _, err := pool.Exec(ctx, `
		UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now() WHERE id = $1`, orderID); err != nil {
		t.Fatalf("cancel the order: %v", err)
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+number+"/invoice", nil)
	res := httptest.NewRecorder()
	desk.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/orders/"+number+"?invoiced=1" {
		t.Errorf("Issue(claimed) = %d %q, want 303 and the claimed invoice filed", res.Code, res.Header().Get("Location"))
	}
	var operations int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoice_operations WHERE order_id = $1 AND kind = 'issue'`,
		orderID).Scan(&operations); err != nil {
		t.Fatalf("count invoice operations: %v", err)
	}
	if operations != 1 {
		t.Errorf("Issue(claimed) left %d issue operations, want the one already claimed", operations)
	}
}

// claimInvoice records the invoice preference a buyer chose at checkout and
// claims the invoice the order owes, as the sale's outbox message does.
func claimInvoice(t *testing.T, invoices *issuer.Store, number string, orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_preferences (order_id, invoice_type, customer_name, customer_email)
		VALUES ($1, 'member_carrier', '買受人', 'buyer@example.com')`, orderID); err != nil {
		t.Fatalf("record invoice preference: %v", err)
	}
	if err := invoices.ClaimDue(ctx, &outbox.InvoiceDue{OrderNumber: number, Trigger: "evt_" + number}); err != nil {
		t.Fatalf("claim the invoice due: %v", err)
	}
}

func postInvoiceForm(ctx context.Context, desk http.Handler, number, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+number+path,
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	desk.ServeHTTP(res, req)
	return res
}

// TestHandler_InvoiceUnconfigured: a shop with no 加值中心 answers a stale page
// or a hand-made POST to issue, void or allow an invoice with the refused
// notice, and files nothing.
func TestHandler_InvoiceUnconfigured(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	desk := http.NewServeMux()
	admintest.OrderDesk(admintest.OrderStore(admintest.AdminRolePool(t, pool), admintest.HealthyGateway(t), nil)).
		Routes(desk, admintest.BackOffice)
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, true)

	for _, tc := range []struct {
		name string
		path string
		form url.Values
	}{
		{"issue", "/invoice", nil},
		{"void", "/invoice/void", url.Values{"reason": {"資料錯誤"}}},
		{"allowance", "/invoice/allowance", url.Values{"operation_id": {uuid.NewString()}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := postInvoiceForm(ctx, desk, number, tc.path, tc.form)
			if want := "/admin/orders/" + number + "?refused=1"; res.Code != http.StatusSeeOther ||
				res.Header().Get("Location") != want {
				t.Errorf("POST %s with no provider = %d %q, want 303 %q",
					tc.path, res.Code, res.Header().Get("Location"), want)
			}
		})
	}
	var operations int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM invoice_operations WHERE order_id = $1`,
		orderID).Scan(&operations); err != nil {
		t.Fatalf("count invoice operations: %v", err)
	}
	if operations != 0 {
		t.Errorf("a shop with no provider left %d invoice operations, want 0", operations)
	}
}

// TestHandler_VoidInvoiceClaimErrors: a void the claim refuses is a notice, not
// a server error: a blank reason, and an invoice that was already voided.
func TestHandler_VoidInvoiceClaimErrors(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	fake := newFakeECPay(t)
	desk, invoices := invoiceDesk(t, fake)
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, true)
	claimInvoice(t, invoices, number, orderID)
	if res := postInvoiceForm(ctx, desk, number, "/invoice", nil); res.Header().Get("Location") !=
		"/admin/orders/"+number+"?invoiced=1" {
		t.Fatalf("Issue = %d %q, want the invoice filed", res.Code, res.Header().Get("Location"))
	}

	for _, tc := range []struct {
		name   string
		reason string
		want   string
	}{
		{"blank reason", "", "?voidreason=1"},
		{"voided", "資料錯誤", "?voided=1"},
		{"already voided", "資料錯誤", "?noinvoice=1"},
	} {
		res := postInvoiceForm(ctx, desk, number, "/invoice/void", url.Values{"reason": {tc.reason}})
		if want := "/admin/orders/" + number + tc.want; res.Code != http.StatusSeeOther ||
			res.Header().Get("Location") != want {
			t.Errorf("Void(%s) = %d %q, want 303 %q", tc.name, res.Code, res.Header().Get("Location"), want)
		}
	}
}

// TestHandler_FileAllowanceIgnoresAmount: the allowance POST does not accept a
// caller-controlled amount. The allowance asked of ECPay is the refunded sum
// the database derives, whether the form carries a stale or forged amount or
// none.
func TestHandler_FileAllowanceIgnoresAmount(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	for _, tc := range []struct {
		name   string
		amount string
	}{
		{"amount omitted", ""},
		{"stale forged amount", "999999999999"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeECPay(t)
			// Issued past ECPay's void window, so the refund leaves only an allowance.
			fake.IssuedAt = time.Now().AddDate(0, -4, 0)
			desk, invoices := invoiceDesk(t, fake)
			number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 0, 500000, false)
			claimInvoice(t, invoices, number, orderID)
			var issueID uuid.UUID
			if err := pool.QueryRow(ctx, `
				SELECT id FROM invoice_operations WHERE order_id = $1 AND kind = 'issue'`,
				orderID).Scan(&issueID); err != nil {
				t.Fatalf("read the system issue: %v", err)
			}
			if err := invoices.ProcessOperation(ctx, issueID); err != nil {
				t.Fatalf("issue the system claim: %v", err)
			}
			if _, err := refunds.NewStore(pool, admintest.HealthyGateway(t), invoices).
				RefundBeforeShipment(ctx, number, "顧客取消"); err != nil {
				t.Fatalf("refund the order: %v", err)
			}

			operationID := uuid.New()
			form := url.Values{"operation_id": {operationID.String()}}
			if tc.amount != "" {
				form.Set("amount", tc.amount)
			}
			res := postInvoiceForm(ctx, desk, number, "/invoice/allowance", form)
			if want := "/admin/orders/" + number + "?allowsent=1"; res.Code != http.StatusSeeOther ||
				res.Header().Get("Location") != want {
				t.Fatalf("Allowance = %d %q, want 303 %q", res.Code, res.Header().Get("Location"), want)
			}
			var id, actor uuid.UUID
			var amount int64
			if err := pool.QueryRow(ctx, `
				SELECT id, amount_cents, actor_user_id FROM invoice_operations
				WHERE order_id = $1 AND kind = 'allowance'`, orderID).Scan(&id, &amount, &actor); err != nil {
				t.Fatalf("read the allowance: %v", err)
			}
			if id != operationID || amount != 500000 || actor != staff {
				t.Errorf("allowance %s is %d by %s, want the posted %s for the refunded 500000 by the staff member",
					id, amount, actor, operationID)
			}
			if got := fake.Calls("/B2CInvoice/AllowanceByCollegiate"); got != 1 {
				t.Errorf("%d allowances asked of ECPay, want one", got)
			}
		})
	}
}
