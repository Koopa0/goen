//go:build integration

package refunds_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/shoptime"
)

// fakeECPay stands in for ECPay's B2C invoice API: what it issues is dated
// issuedAt, and Invalid voids it.
type fakeECPay struct {
	t        *testing.T
	gateway  *invoice.Gateway
	issuedAt time.Time

	mu      sync.Mutex
	byOrder map[string]*fakeInvoice
	calls   map[string]int
}

// fakeInvoiceNumbers keeps the fake's invoice numbers unique in the package's
// database.
var fakeInvoiceNumbers atomic.Int64

type fakeInvoice struct {
	number  string
	amount  int64
	items   json.RawMessage
	invalid bool
}

func newFakeECPay(t *testing.T, issuedAt time.Time) (*fakeECPay, *invoice.Store) {
	t.Helper()
	f := &fakeECPay{t: t, issuedAt: issuedAt, byOrder: map[string]*fakeInvoice{}, calls: map[string]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	g, err := invoice.NewGateway("2000132", "ejCk326UnaZWKisg", "q9jcZX8Ib9LM8wYk", srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	f.gateway = g
	return f, invoice.NewStore(pool, g)
}

func (f *fakeECPay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	plain, err := f.gateway.OpenRequest(r)
	if err != nil {
		f.t.Errorf("open %s: %v", r.URL.Path, err)
		return
	}
	var req struct {
		RelateNumber string
		InvoiceNo    string
		SalesAmount  int64
		Items        json.RawMessage
	}
	if err := json.Unmarshal(plain, &req); err != nil {
		f.t.Errorf("decode %s: %v", r.URL.Path, err)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[r.URL.Path]++
	var reply any
	switch r.URL.Path {
	case "/B2CInvoice/Issue":
		inv := &fakeInvoice{
			number: fmt.Sprintf("RF%08d", fakeInvoiceNumbers.Add(1)), amount: req.SalesAmount, items: req.Items,
		}
		f.byOrder[req.RelateNumber] = inv
		reply = map[string]any{"RtnCode": 1, "RtnMsg": "ok", "InvoiceNo": inv.number,
			"InvoiceDate": shoptime.Second(f.issuedAt), "RandomNumber": "2468"}
	case "/B2CInvoice/GetIssue":
		inv, ok := f.byOrder[req.RelateNumber]
		if !ok {
			reply = map[string]any{"RtnCode": 2, "RtnMsg": "not found"}
			break
		}
		invalid := 0
		if inv.invalid {
			invalid = 1
		}
		reply = map[string]any{"RtnCode": 1, "RtnMsg": "ok", "IIS_Number": inv.number,
			"IIS_Relate_Number": req.RelateNumber, "IIS_Sales_Amount": inv.amount,
			"IIS_Create_Date":  shoptime.Second(f.issuedAt),
			"IIS_Issue_Status": 1 - invalid, "IIS_Invalid_Status": invalid,
			"IIS_Random_Number": "2468", "Items": inv.items}
	case "/B2CInvoice/Invalid":
		for _, inv := range f.byOrder {
			if inv.number == req.InvoiceNo {
				inv.invalid = true
			}
		}
		reply = map[string]any{"RtnCode": 1, "RtnMsg": "ok", "InvoiceNo": req.InvoiceNo}
	default:
		f.t.Errorf("unexpected provider path %s", r.URL.Path)
		return
	}
	if err := f.gateway.SealReply(w, reply); err != nil {
		f.t.Errorf("reply to %s: %v", r.URL.Path, err)
	}
}

func (f *fakeECPay) called(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
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

func issueThroughReconciler(t *testing.T, ctx context.Context, invoices *invoice.Store, orderID uuid.UUID) {
	t.Helper()
	var operationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT id FROM invoice_operations WHERE order_id = $1 AND kind = 'issue'`,
		orderID).Scan(&operationID); err != nil {
		t.Fatalf("read the system issue: %v", err)
	}
	// Every other operation waits, so the one the reconciler takes is this one.
	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations SET available_at = now() + interval '1 day'
		WHERE status = 'pending' AND id <> $1`, operationID); err != nil {
		t.Fatalf("park other operations: %v", err)
	}
	if res, err := invoices.ReconcileOnce(ctx); err != nil || res.OperationID != operationID {
		t.Fatalf("ReconcileOnce = %+v, %v; want operation %s issued", res, err, operationID)
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
			fake, invoices := newFakeECPay(t, time.Now())
			var sent atomic.Int64
			s := refunds.NewStore(pool, admintest.Refunder{Sent: &sent}, invoices)
			number, orderID := capturedOrderOwingAnInvoice(t, ctx, invoices)
			if !inFlight {
				issueThroughReconciler(t, ctx, invoices, orderID)
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
			if voids != 1 || !byStaff || fake.called("/B2CInvoice/Invalid") != 1 {
				t.Fatalf("%d void operations (by staff: %v), %d Invalid calls; want one by the staff member",
					voids, byStaff, fake.called("/B2CInvoice/Invalid"))
			}
			if voided == nil || refunded == nil || !voided.Before(*refunded) {
				t.Errorf("void settled at %v, card refund at %v; want the void first", voided, refunded)
			}
			if inFlight && fake.called("/B2CInvoice/Issue") != 1 {
				t.Errorf("the issue in flight was sent %d times, want once before its void",
					fake.called("/B2CInvoice/Issue"))
			}
		})
	}
}

// TestRefundBeforeShipmentLeavesAnInvoicePastTheVoidWindow: once the period is
// filed ECPay refuses a void, so none is asked for; the refund still goes out.
func TestRefundBeforeShipmentLeavesAnInvoicePastTheVoidWindow(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	fake, invoices := newFakeECPay(t, time.Now().AddDate(0, -4, 0))
	var sent atomic.Int64
	s := refunds.NewStore(pool, admintest.Refunder{Sent: &sent}, invoices)
	number, orderID := capturedOrderOwingAnInvoice(t, ctx, invoices)
	issueThroughReconciler(t, ctx, invoices, orderID)

	if got := pressRefund(t, ctx, s, number); got != "/admin/orders/"+number+"?cancelinvoice=1" {
		t.Fatalf("press redirected to %s, want the invoice left to staff", got)
	}
	var voids int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM invoice_operations WHERE order_id = $1 AND kind = 'void'`,
		orderID).Scan(&voids); err != nil {
		t.Fatalf("count voids: %v", err)
	}
	if voids != 0 || fake.called("/B2CInvoice/Invalid") != 0 || sent.Load() != 1 {
		t.Errorf("%d voids claimed, %d Invalid calls, %d card refunds; want no void and the refund",
			voids, fake.called("/B2CInvoice/Invalid"), sent.Load())
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
