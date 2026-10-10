//go:build integration

package customers_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/customers"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/warranty"
)

// settlement is how far a return's refund has got.
type settlement int

const (
	unsettled settlement = iota // approved, the card refund failed and no refunded event was written
	settled                     // the card refund succeeded and the payout wrote the refunded event
	received                    // settled, the parcel inspected and the return completed
)

func TestCustomerOrderAndWarrantyStatusSayRefundedOnceTheWholeOrdersRefundHasSettled(t *testing.T) {
	for _, tc := range []struct {
		name         string
		quantity     int32
		status       string
		settlement   settlement
		wantReturned bool
	}{
		{name: "no return"},
		{name: "requested whole order", quantity: 2, status: "requested"},
		{name: "approved partial order, settled", quantity: 1, status: "approved", settlement: settled},
		{name: "approved whole order, refund not settled", quantity: 2, status: "approved", settlement: unsettled},
		{name: "approved whole order, refund settled", quantity: 2, status: "approved", settlement: settled, wantReturned: true},
		{name: "completed whole order", quantity: 2, status: "completed", settlement: received, wantReturned: true},
		{name: "rejected whole order", quantity: 2, status: "rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serial, number := registeredWarrantyQuantity(t, "SN-"+strings.ToUpper(uuid.NewString()[:8]), 2)
			var orderID, userID, lineID uuid.UUID
			if err := pool.QueryRow(t.Context(), `SELECT o.id,o.user_id,ol.id FROM orders o JOIN order_lines ol ON ol.order_id=o.id WHERE o.order_number=$1`, number).Scan(&orderID, &userID, &lineID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE orders SET fulfillment_status='delivered' WHERE id=$1`, orderID); err != nil {
				t.Fatal(err)
			}
			if err := warranty.NewStore(pool).Register(t.Context(), number, lineID.String(), userID.String(), serial+"-2", 2); err != nil {
				t.Fatal(err)
			}
			if tc.quantity > 0 {
				returnWithStatus(t, orderID, lineID, tc.quantity, tc.status, tc.settlement)
			}
			staff, _ := admintest.StaffContext(t, pool)
			store := customers.NewStore(admintest.AdminRolePool(t, pool))
			for _, locale := range i18n.Locales() {
				ctx := i18n.WithLocale(staff, locale)
				profile, err := store.Profile(ctx, userID.String())
				if err != nil {
					t.Fatal(err)
				}
				if len(profile.Recent) != 1 {
					t.Fatalf("recent orders=%d, want 1", len(profile.Recent))
				}
				wantText := i18n.T(ctx, i18n.KeyStatusDelivered)
				wantIntent := components.IntentDone
				if tc.wantReturned {
					wantText = i18n.T(ctx, i18n.KeyStatusRefunded)
					wantIntent = components.IntentNeutral
				}
				recent := profile.Recent[0]
				if recent.StatusText != wantText || recent.StatusIntent != wantIntent {
					t.Errorf("%s recent order status=%q intent=%q, want %q %q", locale.Tag(), recent.StatusText, recent.StatusIntent, wantText, wantIntent)
				}
				if recent.Status != order.FulfillmentDelivered {
					t.Errorf("stored fulfillment=%s, want delivered", recent.Status)
				}
				for _, term := range []string{serial, number} {
					warranties, err := store.Warranties(ctx, term)
					if err != nil {
						t.Fatal(err)
					}
					wantRows := 1
					if term == number {
						wantRows = 2
					}
					if len(warranties.Rows) != wantRows {
						t.Fatalf("warranties for %q=%d, want %d", term, len(warranties.Rows), wantRows)
					}
					for _, row := range warranties.Rows {
						if row.OrderStatus != wantText {
							t.Errorf("%s warranty order status=%q, want %q", locale.Tag(), row.OrderStatus, wantText)
						}
					}
				}
			}
		})
	}
}

// returnWithStatus files a return of quantity units of the line and takes it to status. The approval trigger
// allocates the refund amounts itself, so none are written here.
func returnWithStatus(t *testing.T, orderID, lineID uuid.UUID, quantity int32, status string, how settlement) {
	t.Helper()
	ctx := t.Context()
	var requestID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO return_requests(order_id,reason) VALUES($1,'') RETURNING id`, orderID).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO return_request_lines(order_id,return_request_id,order_line_id,quantity) VALUES($1,$2,$3,$4)`, orderID, requestID, lineID, quantity)
	switch status {
	case "rejected":
		exec(`UPDATE return_requests SET status='rejected',decided_at=now() WHERE id=$1`, requestID)
	case "approved", "completed":
		exec(`UPDATE return_requests SET status='approved',decided_at=now() WHERE id=$1`, requestID)
		cardRefund := "failed"
		if how != unsettled {
			cardRefund = "succeeded"
		}
		exec(`
			INSERT INTO refunds (payment_id, return_request_id, request_key, status, amount_cents, reason,
			                     provider_ref, succeeded_at, failed_at)
			SELECT p.id, r.id, 'return:' || r.id, $2::text, r.card_refund_cents, 'test', 're_' || r.id,
			       CASE WHEN $2::text = 'succeeded' THEN now() END,
			       CASE WHEN $2::text = 'failed' THEN now() END
			FROM return_requests r JOIN payments p ON p.order_id = r.order_id AND p.status = 'succeeded'
			WHERE r.id = $1`, requestID, cardRefund)
		if how != unsettled {
			exec(`INSERT INTO order_events (order_id, kind, return_request_id) VALUES ($1, 'refunded', $2)`, orderID, requestID)
		}
		if status == "completed" {
			exec(`UPDATE return_request_lines SET received_quantity=quantity, restocked_quantity=quantity WHERE return_request_id=$1`, requestID)
			exec(`UPDATE return_requests SET status='completed' WHERE id=$1`, requestID)
		}
	}
}
