//go:build integration

package orders_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/frame"
)

// The order page names the staff member behind an event, an invoice operation
// or a refund by their current address. A display name is whatever its owner
// last typed on /account/profile, so naming by it would let a member of staff
// hand what they did on an order to a colleague. One test per query branch:
// each acts as staff A, renames A to staff B's name, renders the order page,
// and looks for A's address and for B's name.

// renamedStaff makes staff A and B and returns A, with B's name for A to take.
func renamedStaff(t *testing.T) (ctxA context.Context, idA uuid.UUID, addressA, colleague string) {
	t.Helper()
	suffix := uuid.NewString()[:8]
	ctxA, idA, addressA = admintest.NamedStaff(t, pool, "Actor "+suffix)
	colleague = "Colleague " + suffix
	admintest.NamedStaff(t, pool, colleague)
	return ctxA, idA, addressA, colleague
}

// assertNamedByAddress renders the order page and holds that it shows the
// address and not the name.
func assertNamedByAddress(t *testing.T, ctx context.Context, view *orders.OrderView, address, colleague string) {
	t.Helper()
	var html bytes.Buffer
	if err := orders.Order(frame.Page{}, view).Render(ctx, &html); err != nil {
		t.Fatalf("render the order page: %v", err)
	}
	if !strings.Contains(html.String(), address) {
		t.Errorf("the order page does not show %q", address)
	}
	if strings.Contains(html.String(), colleague) {
		t.Errorf("the order page shows the name %q the staff member took", colleague)
	}
}

// TestARenamedStaffIsNamedByAddressOnTheOrderEvents: the order_events branch of
// the timeline.
func TestARenamedStaffIsNamedByAddressOnTheOrderEvents(t *testing.T) {
	ctx, idA, address, colleague := renamedStaff(t)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number, _, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, false)
	if _, err := s.Advance(ctx, number, order.FulfillmentPicking, uuid.NullUUID{UUID: idA, Valid: true}); err != nil {
		t.Fatalf("A moves the order to picking: %v", err)
	}

	admintest.RenameUser(t, pool, idA, colleague)
	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order(%s): %v", number, err)
	}
	var found bool
	for _, e := range view.Timeline {
		if e.Label == i18n.KeyStatusPicking {
			found = true
			if e.Actor != address {
				t.Errorf("the picking event is by %q, want the address %q", e.Actor, address)
			}
		}
	}
	if !found {
		t.Fatalf("no picking event among %d timeline entries", len(view.Timeline))
	}
	assertNamedByAddress(t, ctx, &view, address, colleague)
}

// TestARenamedStaffIsNamedByAddressOnTheInvoiceOperations: the invoice_operations
// branch of the timeline.
func TestARenamedStaffIsNamedByAddressOnTheInvoiceOperations(t *testing.T) {
	ctx, idA, address, colleague := renamedStaff(t)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, false)
	if _, err := pool.Exec(ctx, `
		WITH invoice AS (
			INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
			VALUES ($1, 'invoice', 'GD-' || substr(replace(gen_random_uuid()::text, '-', ''), 1, 8), 100000)
			RETURNING id, order_id, number)
		INSERT INTO invoice_operations
		    (order_id, kind, target_document_id, provider_key, amount_cents, request_payload,
		     actor_user_id, actor_id_snapshot, request_id, send_attempts, last_send_at)
		SELECT order_id, 'allowance', id, number, 50000, '{}', $2, $2, $3, 1, now()
		FROM invoice`, orderID, idA, "renamed-allowance:"+number); err != nil {
		t.Fatalf("A sends an online allowance: %v", err)
	}

	admintest.RenameUser(t, pool, idA, colleague)
	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order(%s): %v", number, err)
	}
	var found bool
	for _, e := range view.Timeline {
		if e.Label == i18n.KeyAuditInvoiceAllowance {
			found = true
			if e.Actor != address {
				t.Errorf("the allowance is by %q, want the address %q", e.Actor, address)
			}
		}
	}
	if !found {
		t.Fatalf("no allowance among %d timeline entries", len(view.Timeline))
	}
	assertNamedByAddress(t, ctx, &view, address, colleague)
}

// TestARenamedStaffIsNamedByAddressOnACardRefund: the refund row whose staff
// member is read from the audit trail.
func TestARenamedStaffIsNamedByAddressOnACardRefund(t *testing.T) {
	ctx, idA, address, colleague := renamedStaff(t)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, false)
	var refundID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, reason, status, provider_ref, succeeded_at)
		SELECT p.id, 'renamed:' || ($1::uuid)::text, 120000, '顧客改變心意', 'succeeded',
		       're_renamed_' || ($1::uuid)::text, now()
		FROM payments p WHERE p.order_id = $1 AND p.status = 'succeeded'
		RETURNING id`, orderID).Scan(&refundID); err != nil {
		t.Fatalf("record a refund: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT record_audit_event($1, 'refund.succeeded', 'refunds', $2)`,
		idA, refundID); err != nil {
		t.Fatalf("record that A refunded it: %v", err)
	}

	admintest.RenameUser(t, pool, idA, colleague)
	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order(%s): %v", number, err)
	}
	if len(view.Refunds) != 1 {
		t.Fatalf("refunds = %+v, want one", view.Refunds)
	}
	if got := view.Refunds[0].Staff; got != address {
		t.Errorf("the card refund is by %q, want the address %q", got, address)
	}
	assertNamedByAddress(t, ctx, &view, address, colleague)
}

// TestARenamedStaffIsNamedByAddressOnACreditRefund: the refund row whose staff
// member is the store-credit entry's own actor.
func TestARenamedStaffIsNamedByAddressOnACreditRefund(t *testing.T) {
	ctx, idA, address, colleague := renamedStaff(t)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, false)
	customer := admintest.Customer(t, pool)
	if _, err := pool.Exec(ctx, `SELECT post_store_credit($1, 120000, NULL, $2, $3, $4)`,
		customer, orderID, "return-credit:"+orderID.String(), idA); err != nil {
		t.Fatalf("A refunds to store credit: %v", err)
	}

	admintest.RenameUser(t, pool, idA, colleague)
	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order(%s): %v", number, err)
	}
	if len(view.Refunds) != 1 {
		t.Fatalf("refunds = %+v, want one", view.Refunds)
	}
	if got := view.Refunds[0].Staff; got != address {
		t.Errorf("the credit refund is by %q, want the address %q", got, address)
	}
	// The posting has no reason of its own; its label comes from its kind.
	if got, want := view.Refunds[0].Reason, i18n.T(ctx, i18n.KeyAdminCreditReasonReturnPayout); got != want {
		t.Errorf("the credit refund reads %q, want the return-payout label %q", got, want)
	}
	assertNamedByAddress(t, ctx, &view, address, colleague)
}
