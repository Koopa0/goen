//go:build integration

package cart_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/outbox"
)

// creditPaidOrder is an order store credit paid in full, whose 統一發票 the
// system has claimed on invoices and not yet sent.
func creditPaidOrder(t *testing.T, invoices *invoice.Store, slug string) string {
	t.Helper()
	ctx := t.Context()
	s := cart.NewStore(pool)
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	userID := creditedCustomer(t, 100000000)
	id := newCart(t, s)
	if err := s.Add(ctx, id, freshVariant(t, slug), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	number, err := placeOrder(t, s, ctx, id, uuid.NullUUID{UUID: userID, Valid: true}, shipID,
		&order.Delivery{
			Email: "void-due@example.com", RecipientName: "王小明", Phone: "0912345678",
			PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
		}, "", slug+"-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	dues := queuedDues(t, number)
	if len(dues) != 1 {
		t.Fatalf("checkout queued %d invoices for %s, want 1", len(dues), number)
	}
	if err := invoices.ClaimDue(ctx, &dues[0]); err != nil {
		t.Fatalf("claim the invoice due: %v", err)
	}
	return number
}

func fakeECPay(t *testing.T, issuedAt time.Time) (*invoice.FakeECPay, *invoice.Store) {
	t.Helper()
	fake, err := invoice.NewFakeECPay(issuedAt)
	if err != nil {
		t.Fatalf("fake ECPay: %v", err)
	}
	t.Cleanup(fake.Close)
	return fake, invoice.NewStore(adminRole(t), fake.Gateway())
}

func invoiceOperation(t *testing.T, number, kind string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		SELECT op.id FROM invoice_operations op JOIN orders o ON o.id = op.order_id
		WHERE o.order_number = $1 AND op.kind = $2
		ORDER BY op.created_at DESC LIMIT 1`, number, kind).Scan(&id); err != nil {
		t.Fatalf("read the %s operation of %s: %v", kind, number, err)
	}
	return id
}

// cancelAsCustomer cancels on the storefront's role, and returns the void the
// cancellation queued.
func cancelAsCustomer(t *testing.T, number string) outbox.InvoiceVoidDue {
	t.Helper()
	if err := cart.NewStore(storeRolePool(t)).CancelOrder(t.Context(), number); err != nil {
		t.Fatalf("cancel %s: %v", number, err)
	}
	dues := queuedVoidDues(t, number)
	want := outbox.InvoiceVoidDue{OrderNumber: number, Trigger: "cancel:" + number}
	if len(dues) != 1 || dues[0] != want {
		t.Fatalf("the cancellation of %s queued %+v, want %+v", number, dues, want)
	}
	return dues[0]
}

func queuedVoidDues(t *testing.T, number string) []outbox.InvoiceVoidDue {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT payload FROM outbox_messages WHERE topic = $1 AND dedupe_key = $2`,
		outbox.TopicInvoiceVoidDue.Name(), number)
	if err != nil {
		t.Fatalf("read invoice.void_due for %s: %v", number, err)
	}
	payloads, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		t.Fatalf("collect invoice.void_due for %s: %v", number, err)
	}
	out := make([]outbox.InvoiceVoidDue, len(payloads))
	for i, p := range payloads {
		if err := json.Unmarshal(p, &out[i]); err != nil {
			t.Fatalf("decode invoice.void_due: %v", err)
		}
	}
	return out
}

// TestACustomersCancelVoidsTheInvoiceTheirCreditPaidFor: the press is the
// buyer's consent, recorded as their own cancellation, and the void the system
// claims from it settles; an issue still in flight is issued first.
func TestACustomersCancelVoidsTheInvoiceTheirCreditPaidFor(t *testing.T) {
	for _, inFlight := range []bool{false, true} {
		name := "issued"
		if inFlight {
			name = "issue in flight"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			fake, invoices := fakeECPay(t, time.Now())
			number := creditPaidOrder(t, invoices, "void-due")
			issue := invoiceOperation(t, number, "issue")
			if !inFlight {
				if err := invoices.ProcessOperation(ctx, issue); err != nil {
					t.Fatalf("issue: %v", err)
				}
			}

			due := cancelAsCustomer(t, number)
			var bySystem bool
			var actor uuid.NullUUID
			if err := pool.QueryRow(ctx, `
				SELECT e.by_system, e.actor_user_id FROM order_events e JOIN orders o ON o.id = e.order_id
				WHERE o.order_number = $1 AND e.kind = 'cancelled'`, number).Scan(&bySystem, &actor); err != nil {
				t.Fatalf("read the cancellation: %v", err)
			}
			if bySystem || actor.Valid {
				t.Errorf("cancellation recorded by_system=%v actor=%v, want the customer's own", bySystem, actor)
			}

			if inFlight {
				if err := invoices.ClaimVoidDue(ctx, &due); !errors.Is(err, invoice.ErrPending) {
					t.Fatalf("void due with the issue in flight = %v, want it to wait", err)
				}
				if err := invoices.ProcessOperation(ctx, issue); err != nil {
					t.Fatalf("issue after the cancellation: %v", err)
				}
			}
			if err := invoices.ClaimVoidDue(ctx, &due); err != nil {
				t.Fatalf("claim the void due: %v", err)
			}
			if listed(t, number) {
				t.Errorf("cancelled %s is on the health page while its void is still being sent", number)
			}
			void := invoiceOperation(t, number, "void")
			if err := invoices.ProcessOperation(ctx, void); err != nil {
				t.Fatalf("send the void: %v", err)
			}
			var status, kind, requestID, document, audited string
			var operationActor uuid.NullUUID
			if err := pool.QueryRow(ctx, `
				SELECT op.status, op.actor_kind, op.actor_user_id, op.request_id, d.status,
				       (SELECT a.actor_kind FROM audit_events a
				        WHERE a.action = 'invoice.void' AND a.entity_id = d.id)
				FROM invoice_operations op JOIN invoice_documents d ON d.id = op.target_document_id
				WHERE op.id = $1`, void).Scan(&status, &kind, &operationActor, &requestID,
				&document, &audited); err != nil {
				t.Fatalf("read the void: %v", err)
			}
			if status != "succeeded" || kind != "system" || operationActor.Valid ||
				requestID != due.Trigger || document != "voided" || audited != "system" {
				t.Errorf("void %s by %s (user %v) for %q, invoice %s, audited as %s; "+
					"want a settled system void for %q", status, kind, operationActor, requestID,
					document, audited, due.Trigger)
			}
			if got := fake.Calls("/B2CInvoice/Invalid"); got != 1 {
				t.Errorf("Invalid called %d times, want once", got)
			}
		})
	}
}

// TestACustomersCancelWithdrawsAnIssueNoProviderWillSend: with no 加值中心 the
// claim would wait for ever; nothing is sent because nothing can be.
func TestACustomersCancelWithdrawsAnIssueNoProviderWillSend(t *testing.T) {
	ctx := t.Context()
	gateway, err := invoice.NewGateway("", "", "", "")
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	invoices := invoice.NewStore(adminRole(t), gateway)
	number := creditPaidOrder(t, invoices, "void-due-off")
	issue := invoiceOperation(t, number, "issue")

	due := cancelAsCustomer(t, number)
	if err := invoices.ClaimVoidDue(ctx, &due); err != nil {
		t.Fatalf("void due with no provider: %v", err)
	}
	var status, category string
	var sends, voids int
	if err := pool.QueryRow(ctx, `
		SELECT status, coalesce(last_error, ''), send_attempts,
		       (SELECT count(*) FROM invoice_operations v
		        WHERE v.order_id = op.order_id AND v.kind = 'void')
		FROM invoice_operations op WHERE id = $1`, issue).Scan(&status, &category, &sends, &voids); err != nil {
		t.Fatalf("read the issue: %v", err)
	}
	if status != "rejected" || category != "issue_withdrawn_order_cancelled" || sends != 0 || voids != 0 {
		t.Errorf("issue %s (%s) after %d sends, %d voids; want withdrawn unsent and no void",
			status, category, sends, voids)
	}
}

// TestACancelledOrdersInvoiceWaitsForAProviderToVoidIt: a void claimed with no
// 加值中心 would sit unsent; the health page names the live invoice instead.
func TestACancelledOrdersInvoiceWaitsForAProviderToVoidIt(t *testing.T) {
	ctx := t.Context()
	_, invoices := fakeECPay(t, time.Now())
	number := creditPaidOrder(t, invoices, "void-due-unset")
	if err := invoices.ProcessOperation(ctx, invoiceOperation(t, number, "issue")); err != nil {
		t.Fatalf("issue: %v", err)
	}
	due := cancelAsCustomer(t, number)

	gateway, err := invoice.NewGateway("", "", "", "")
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	if err := invoice.NewStore(adminRole(t), gateway).ClaimVoidDue(ctx, &due); err != nil {
		t.Fatalf("void due with no provider: %v", err)
	}
	var voids int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM invoice_operations op JOIN orders o ON o.id = op.order_id
		WHERE o.order_number = $1 AND op.kind = 'void'`, number).Scan(&voids); err != nil {
		t.Fatalf("count voids: %v", err)
	}
	if voids != 0 {
		t.Errorf("%d voids claimed with no provider to send them, want none", voids)
	}
}

// TestACustomersCancelPastTheVoidWindowLeavesTheInvoiceForStaff: the
// cancellation goes through, no void is asked of ECPay once the period is filed,
// and the back office's health page names the invoice.
func TestACustomersCancelPastTheVoidWindowLeavesTheInvoiceForStaff(t *testing.T) {
	ctx := t.Context()
	fake, invoices := fakeECPay(t, time.Now().AddDate(0, -4, 0))
	number := creditPaidOrder(t, invoices, "void-due-late")
	if err := invoices.ProcessOperation(ctx, invoiceOperation(t, number, "issue")); err != nil {
		t.Fatalf("issue: %v", err)
	}

	due := cancelAsCustomer(t, number)
	if err := invoices.ClaimVoidDue(ctx, &due); err != nil {
		t.Fatalf("void due past the window: %v", err)
	}
	var voids int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM invoice_operations op JOIN orders o ON o.id = op.order_id
		WHERE o.order_number = $1 AND op.kind = 'void'`, number).Scan(&voids); err != nil {
		t.Fatalf("count voids: %v", err)
	}
	if voids != 0 || fake.Calls("/B2CInvoice/Invalid") != 0 {
		t.Errorf("%d voids claimed and %d Invalid calls past the window, want none",
			voids, fake.Calls("/B2CInvoice/Invalid"))
	}
	if !listed(t, number) {
		t.Errorf("the live invoice of cancelled %s is not on the health page", number)
	}
}

// listed reports whether the health page names the live invoice of the
// cancelled order number, however recently it was cancelled.
func listed(t *testing.T, number string) bool {
	t.Helper()
	invoices, _, err := health.NewStore(adminRole(t)).CancelledOrderInvoices(t.Context(), 0)
	if err != nil {
		t.Fatalf("read the health page's list: %v", err)
	}
	for _, inv := range invoices {
		if inv.OrderNumber == number {
			return true
		}
	}
	return false
}

// TestOnlyACreditPaidOrdersCancelQueuesAVoid: an order nobody paid has no
// invoice to void.
func TestOnlyACreditPaidOrdersCancelQueuesAVoid(t *testing.T) {
	number := numberOf(t, heldOrder(t, freshVariant(t, "void-due-unpaid"), -time.Hour, false))
	if err := cart.NewStore(storeRolePool(t)).CancelOrder(t.Context(), number); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := queuedVoidDues(t, number); len(got) != 0 {
		t.Errorf("cancelling an unpaid order queued %+v, want nothing", got)
	}
}
