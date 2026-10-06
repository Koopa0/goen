//go:build integration

package refunds_test

import (
	"encoding/json"
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

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/admin/refundstate"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// creditPaidEffects is what cancelling a pending order store credit paid
// leaves behind, counted so a replay that repeats any of it shows.
type creditPaidEffects struct {
	reversals, reversedCents, balance                 int64
	held, released, cancelledEvents, notedEvents      int64
	audits, voidsDue, terminalNotices, returnRequests int64
	staffEvents                                       int64
	auditNote, auditRequest, voidTrigger              string
}

func readCreditPaidEffects(t *testing.T, orderID, staff uuid.UUID, number string) creditPaidEffects {
	t.Helper()
	var e creditPaidEffects
	var voidPayload []byte
	if err := pool.QueryRow(t.Context(), `
		SELECT
		  (SELECT count(*) FROM store_credit_entries r
		   JOIN store_credit_entries s ON s.id = r.reverses_id WHERE s.order_id = $1),
		  (SELECT coalesce(sum(r.amount_cents), 0) FROM store_credit_entries r
		   JOIN store_credit_entries s ON s.id = r.reverses_id WHERE s.order_id = $1),
		  (SELECT coalesce(sum(b.amount_cents), 0) FROM store_credit_entries b
		   WHERE b.account_id = (SELECT account_id FROM store_credit_entries
		                         WHERE order_id = $1 AND amount_cents < 0 LIMIT 1)),
		  (SELECT count(*) FROM inventory_reservations WHERE order_id = $1 AND state = 'held'),
		  (SELECT count(*) FROM inventory_reservations WHERE order_id = $1 AND state = 'released'),
		  (SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'cancelled'),
		  (SELECT count(*) FROM order_events
		   WHERE order_id = $1 AND kind = 'cancelled' AND coalesce(note, '') <> ''),
		  (SELECT count(*) FROM audit_events WHERE action = 'order.advance' AND entity_id = $1),
		  (SELECT count(*) FROM outbox_messages WHERE topic = $3 AND dedupe_key = $4),
		  (SELECT count(*) FROM outbox_messages WHERE topic = $5 AND dedupe_key LIKE ($1::uuid)::text || ':%'),
		  (SELECT count(*) FROM return_requests WHERE order_id = $1),
		  (SELECT count(*) FROM order_events
		   WHERE order_id = $1 AND kind = 'cancelled' AND actor_user_id = $2),
		  coalesce((SELECT after->>'note' FROM audit_events
		            WHERE action = 'order.advance' AND entity_id = $1 LIMIT 1), ''),
		  coalesce((SELECT request_id FROM audit_events
		            WHERE action = 'order.advance' AND entity_id = $1 LIMIT 1), ''),
		  (SELECT payload FROM outbox_messages WHERE topic = $3 AND dedupe_key = $4)`,
		orderID, staff, outbox.TopicInvoiceVoidDue.Name(), number, outbox.TopicOrderTerminal.Name()).Scan(
		&e.reversals, &e.reversedCents, &e.balance, &e.held, &e.released,
		&e.cancelledEvents, &e.notedEvents, &e.audits, &e.voidsDue, &e.terminalNotices,
		&e.returnRequests, &e.staffEvents, &e.auditNote, &e.auditRequest, &voidPayload); err != nil {
		t.Fatalf("read the cancellation of %s: %v", number, err)
	}
	if voidPayload != nil {
		var due outbox.InvoiceVoidDue
		if err := json.Unmarshal(voidPayload, &due); err != nil {
			t.Fatalf("decode the void due of %s: %v", number, err)
		}
		e.voidTrigger = due.Trigger
	}
	return e
}

// A pending order store credit alone paid is not committed until packing, and
// store_credit_guard refuses to pay its credit back through a return. Its
// refund before shipment cancels it as its customer could, once, on the admin
// role's own grants.
func TestRefundBeforeShipmentCancelsACreditPaidPendingOrder(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := refunds.NewStore(admintest.AdminRolePool(t, pool), admintest.Refunder{}, nil)
	h := refunds.NewHandler(s, nil, slog.New(slog.DiscardHandler))
	number, orderID, variantID := admintest.PaidUnshippedOrder(t, pool, 0, 500000, false)

	var stockHeld int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, variantID).Scan(&stockHeld); err != nil {
		t.Fatalf("read stock: %v", err)
	}
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

	preview := post(url.Values{})
	body := html.UnescapeString(preview.Body.String())
	if preview.Code != http.StatusOK ||
		!strings.Contains(body, fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRefundCreditReturn), pages.TWD(500000))) ||
		!strings.Contains(body, `value="500000"`) {
		t.Fatalf("preview = %d, want the store credit cancellation: %s", preview.Code, body)
	}
	if got := admintest.FulfillmentOf(t, pool, orderID); got != "pending" {
		t.Fatalf("the preview moved the order to %s", got)
	}

	confirm := url.Values{"confirm": {"refund"}, "total": {"500000"}, "reason": {"顧客來電取消"}}
	done := post(confirm)
	if done.Code != http.StatusSeeOther || done.Header().Get("Location") != "/admin/orders/"+number+"?refunded=1" {
		t.Fatalf("confirmed POST = %d %s", done.Code, done.Header().Get("Location"))
	}

	check := func(t *testing.T, when string) {
		t.Helper()
		if got := admintest.FulfillmentOf(t, pool, orderID); got != "cancelled" {
			t.Errorf("%s: order is %s, want cancelled", when, got)
		}
		e := readCreditPaidEffects(t, orderID, staff, number)
		if e.reversals != 1 || e.reversedCents != 500000 || e.balance != 500000 {
			t.Errorf("%s: %d spend reversals of %d, balance %d; want the 500000 spent back once",
				when, e.reversals, e.reversedCents, e.balance)
		}
		var stock int32
		if err := pool.QueryRow(ctx,
			`SELECT stock_quantity FROM product_variants WHERE id = $1`, variantID).Scan(&stock); err != nil {
			t.Fatalf("read stock: %v", err)
		}
		if e.held != 0 || e.released != 1 || stock != stockHeld+1 {
			t.Errorf("%s: holds held/released = %d/%d, stock %d -> %d; want the hold released once",
				when, e.held, e.released, stockHeld, stock)
		}
		if e.voidsDue != 1 || e.voidTrigger != web.RequestID(ctx) {
			t.Errorf("%s: %d invoice voids due with trigger %q, want one with the request id %q",
				when, e.voidsDue, e.voidTrigger, web.RequestID(ctx))
		}
		if e.cancelledEvents != 1 || e.staffEvents != 1 || e.notedEvents != 0 {
			t.Errorf("%s: cancelled events %d (%d by the staff member, %d with a note), want one by them with no note the customer reads",
				when, e.cancelledEvents, e.staffEvents, e.notedEvents)
		}
		if e.audits != 1 || e.auditNote != "顧客來電取消" || e.auditRequest != web.RequestID(ctx) {
			t.Errorf("%s: %d audit rows noting %q under request %q, want one with the note under %q",
				when, e.audits, e.auditNote, e.auditRequest, web.RequestID(ctx))
		}
		if e.terminalNotices != 1 || e.returnRequests != 0 {
			t.Errorf("%s: terminal notices %d, return requests %d; want 1 and none", when, e.terminalNotices, e.returnRequests)
		}
	}
	check(t, "after the cancellation")
	admintest.AssertTerminalNotice(t, pool, orderID, email.TerminalCancelledByStaff, false)

	if replay := post(confirm); replay.Code != http.StatusSeeOther ||
		replay.Header().Get("Location") != "/admin/orders/"+number+"?refundcancelled=1" {
		t.Errorf("replayed POST = %d %s, want the cancelled sentence", replay.Code, replay.Header().Get("Location"))
	}
	if _, err := s.RefundBeforeShipment(ctx, number, "顧客來電取消"); !errors.Is(err, refundstate.ErrRefused) {
		t.Errorf("a second cancellation = %v, want ErrRefused", err)
	}
	check(t, "after the replays")
}

// Whoever moved the order while the cancellation waited on its lock, a staff
// member starting to pack it or its customer cancelling it, wins: the database
// refuses the cancellation (orders_paid_cancel_needs_refund,
// orders_history_frozen) and it records nothing. A credit spend reversed in the
// meantime leaves the order pending, so the cancellation itself reads how the
// order was paid again under the lock and refuses.
func TestACreditPaidCancellationLosesToWhoeverMovedTheOrderFirst(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := refunds.NewStore(pool, admintest.Refunder{}, nil)
	for _, tc := range []struct {
		name            string
		moves           []string
		status          string
		constraint      string
		changed         bool
		reversals, held int64
	}{
		{
			name:       "packing started",
			constraint: "orders_paid_cancel_needs_refund",
			moves:      []string{`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`},
			status:     "picking", held: 1,
		},
		{
			name:       "customer cancelled",
			constraint: "orders_history_frozen",
			moves: []string{
				`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now() WHERE id = $1`,
				`SELECT release_reservation(id) FROM inventory_reservations WHERE order_id = $1 AND state = 'held'`,
				`SELECT reverse_order_credit($1)`,
				`INSERT INTO order_events (order_id, kind) VALUES ($1, 'cancelled')`,
			},
			status: "cancelled", reversals: 1,
		},
		{
			// Triggers off because store_credit_guard reverses a spend only after
			// the order has cancelled, and the order here has not.
			name:    "credit spend reversed",
			changed: true,
			moves: []string{
				`SET LOCAL session_replication_role = replica`,
				`INSERT INTO store_credit_entries (account_id, amount_cents, reason, reverses_id, idempotency_key)
				 SELECT account_id, -amount_cents, 'spend reversed', id, 'reverse:' || id
				 FROM store_credit_entries WHERE order_id = $1 AND amount_cents < 0`,
			},
			status: "pending", reversals: 1, held: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 0, 500000, false)
			first, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer pgtx.Rollback(ctx, first)
			if _, err := first.Exec(ctx, `SELECT id FROM orders WHERE id = $1 FOR UPDATE`, orderID); err != nil {
				t.Fatalf("lock the order: %v", err)
			}

			result := make(chan error, 1)
			go func() {
				_, cancelErr := s.RefundBeforeShipment(ctx, number, "顧客來電取消")
				result <- cancelErr
			}()
			deadline := time.Now().Add(10 * time.Second)
			for waiting := false; !waiting; {
				if time.Now().After(deadline) {
					t.Fatal("the cancellation never waited on the order lock")
				}
				select {
				case err := <-result:
					t.Fatalf("the cancellation finished while the order was locked: %v", err)
				default:
				}
				if err := pool.QueryRow(ctx, `
					SELECT EXISTS (SELECT 1 FROM pg_stat_activity
					               WHERE wait_event_type = 'Lock' AND query LIKE '%name: LockOrderByNumber%')`).
					Scan(&waiting); err != nil {
					t.Fatalf("observe the cancellation: %v", err)
				}
				time.Sleep(10 * time.Millisecond)
			}
			for _, move := range tc.moves {
				if _, err := first.Exec(ctx, move, orderID); err != nil {
					t.Fatalf("%s: %v", move, err)
				}
			}
			if err := first.Commit(ctx); err != nil {
				t.Fatalf("commit: %v", err)
			}

			select {
			case err := <-result:
				if !errors.Is(err, refundstate.ErrRefused) ||
					(tc.changed && !errors.Is(err, refunds.ErrOrderChanged)) ||
					(!tc.changed && !pgerr.IsConstraint(err, tc.constraint)) {
					t.Errorf("cancellation after %s = %v, want ErrRefused from %s", tc.name, err, tc.constraint)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the cancellation did not finish after the lock was released")
			}
			if got := admintest.FulfillmentOf(t, pool, orderID); got != tc.status {
				t.Errorf("order is %s, want %s", got, tc.status)
			}
			e := readCreditPaidEffects(t, orderID, staff, number)
			if e.reversals != tc.reversals || e.held != tc.held || e.staffEvents != 0 ||
				e.audits != 0 || e.voidsDue != 0 || e.terminalNotices != 0 {
				t.Errorf("reversals/held/staff events/audits/voids due/notices = %d/%d/%d/%d/%d/%d, want %d/%d/0/0/0/0",
					e.reversals, e.held, e.staffEvents, e.audits, e.voidsDue, e.terminalNotices, tc.reversals, tc.held)
			}
		})
	}
}

// An order a card paid, or one packing has committed, keeps the refund through
// a return: the credit spend is never reversed beside it.
func TestACommittedOrderIsRefundedThroughAReturn(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := refunds.NewStore(pool, admintest.Refunder{}, nil)
	for _, tc := range []struct {
		name                   string
		cardCents, creditCents int64
		picking                bool
	}{
		{name: "card paid, pending", cardCents: 500000},
		{name: "store credit paid, picking", creditCents: 500000, picking: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, tc.cardCents, tc.creditCents, tc.picking)
			if _, err := s.RefundBeforeShipment(ctx, number, "顧客來電取消"); err != nil {
				t.Fatalf("refund before shipment: %v", err)
			}
			if got := admintest.FulfillmentOf(t, pool, orderID); got != "cancelled" {
				t.Errorf("order is %s, want cancelled", got)
			}
			if e := readCreditPaidEffects(t, orderID, staff, number); e.returnRequests != 1 || e.reversals != 0 || e.voidsDue != 0 {
				t.Errorf("returns/reversals/voids due = %d/%d/%d, want the refund through one return",
					e.returnRequests, e.reversals, e.voidsDue)
			}
		})
	}
}
