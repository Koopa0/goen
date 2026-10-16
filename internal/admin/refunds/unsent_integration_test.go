//go:build integration

package refunds_test

import (
	"encoding/json"
	"errors"
	"html"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/notice"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/refund"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// The refund of units a partly shipped order will not send (#1965), through
// the back office's door on the admin role. The fixture is
// admintest.PartlyShippedOrder: A (1 × NT$1,200) shipped, B (3 × NT$500) sent
// one and still holds two; NT$200 off and NT$80 delivery.

// unsentB is the two units of B that did not ship. Their refund is the goods'
// cumulative share of what was paid for the goods, rounded up to whole NT$:
// ceil(100000 × 250000 / 270000 / 100) × 100.
const unsentBRefund = 92600

const unsentNote = "倉庫點貨時發現外盒破損"

func unsentB(o *admintest.TwoLineOrder) map[uuid.UUID]int32 {
	return map[uuid.UUID]int32{o.LineB: 2}
}

func unsentRequest(reason returns.UnsentReason, units map[uuid.UUID]int32) *refunds.UnsentRequest {
	return &refunds.UnsentRequest{Units: units, Reason: string(reason), Note: unsentNote}
}

// unsentDesk is the refunds desk over the back office's own role, as cmd/goen
// builds it.
func unsentDesk(t *testing.T, refunder refunds.Refunder) (*refunds.Store, *pgxpool.Pool) {
	t.Helper()
	adminPool := admintest.AdminRolePool(t, pool)
	return refunds.NewStore(adminPool, refunder, nil), adminPool
}

// unsentFacts is everything one unsent refund moved, read as the owner.
type unsentFacts struct {
	id                                 uuid.UUID
	status                             string
	goods, delivery, card, credit      int64
	cardPaid, creditPaid, providerRows int64
	refundedEvents, clawbacks          int64
	clawed                             int64
	heldB, releasedB, consumedB        int64
	stockB                             int32
	mails, openAudits, completeAudits  int64
	payload                            []byte
	releaseMoves, writeOffAdjustments  int64
}

func readUnsent(t *testing.T, o *admintest.TwoLineOrder) unsentFacts {
	t.Helper()
	var f unsentFacts
	if err := pool.QueryRow(t.Context(), `
		SELECT id, status, coalesce(goods_refund_cents, 0), coalesce(shipping_refund_cents, 0),
		       coalesce(card_refund_cents, 0), coalesce(credit_refund_cents, 0)
		FROM return_requests WHERE order_id = $1 AND unsent`, o.ID).Scan(
		&f.id, &f.status, &f.goods, &f.delivery, &f.card, &f.credit); err != nil {
		t.Fatalf("read the unsent refund of %s: %v", o.Number, err)
	}
	if err := pool.QueryRow(t.Context(), `
		SELECT
		  (SELECT coalesce(sum(amount_cents), 0) FROM refunds WHERE return_request_id = $2 AND status = 'succeeded'),
		  (SELECT coalesce(sum(amount_cents), 0) FROM store_credit_entries WHERE idempotency_key = 'return-credit:' || $2::text),
		  (SELECT count(*) FROM refunds WHERE return_request_id = $2),
		  (SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'refunded' AND return_request_id = $2),
		  (SELECT count(*) FROM loyalty_entries WHERE return_request_id = $2 AND kind = 'clawback'),
		  (SELECT coalesce(sum(points), 0) FROM loyalty_entries WHERE return_request_id = $2 AND kind = 'clawback'),
		  (SELECT coalesce(sum(quantity), 0) FROM inventory_reservations WHERE order_id = $1 AND variant_id = $3 AND state = 'held'),
		  (SELECT coalesce(sum(quantity), 0) FROM inventory_reservations WHERE order_id = $1 AND variant_id = $3 AND state = 'released'),
		  (SELECT coalesce(sum(quantity), 0) FROM inventory_reservations WHERE order_id = $1 AND variant_id = $3 AND state = 'consumed'),
		  (SELECT stock_quantity FROM product_variants WHERE id = $3),
		  (SELECT count(*) FROM outbox_messages WHERE topic = $4 AND dedupe_key = $2::text),
		  (SELECT count(*) FROM audit_events WHERE action = 'return.refund_unsent' AND entity_id = $2),
		  (SELECT count(*) FROM audit_events WHERE action = 'return.complete' AND entity_id = $2),
		  (SELECT count(*) FROM inventory_movements m JOIN inventory_reservations r
		     ON m.source_type = 'reservation' AND m.source_id = r.id
		   WHERE r.order_id = $1 AND r.variant_id = $3 AND m.reason = 'release'),
		  (SELECT count(*) FROM inventory_movements WHERE variant_id = $3 AND reason = 'adjustment')`,
		o.ID, f.id, o.VariantB, outbox.TopicOrderPartRefunded.Name()).Scan(
		&f.cardPaid, &f.creditPaid, &f.providerRows, &f.refundedEvents, &f.clawbacks, &f.clawed,
		&f.heldB, &f.releasedB, &f.consumedB, &f.stockB, &f.mails, &f.openAudits, &f.completeAudits,
		&f.releaseMoves, &f.writeOffAdjustments); err != nil {
		t.Fatalf("read what the unsent refund of %s moved: %v", o.Number, err)
	}
	_ = pool.QueryRow(t.Context(), `SELECT payload FROM outbox_messages WHERE topic = $1 AND dedupe_key = $2`,
		outbox.TopicOrderPartRefunded.Name(), f.id.String()).Scan(&f.payload)
	return f
}

func memberSpend(t *testing.T, user uuid.UUID) int64 {
	t.Helper()
	var spend int64
	if err := pool.QueryRow(t.Context(), `SELECT member_spend($1)`, user).Scan(&spend); err != nil {
		t.Fatalf("read member_spend: %v", err)
	}
	return spend
}

func stockOf(t *testing.T, variant uuid.UUID) int32 {
	t.Helper()
	var stock int32
	if err := pool.QueryRow(t.Context(), `SELECT stock_quantity FROM product_variants WHERE id = $1`, variant).Scan(&stock); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return stock
}

// orderDesk is the order desk over the same role and refunder.
func orderDesk(adminPool *pgxpool.Pool, refunder refunds.Refunder) *orders.Store {
	return admintest.OrderStore(adminPool, refunder, nil, admintest.DisabledInvoiceWriter{})
}

// Family 1: a card order. The refund is the paid price less the discount
// share, the remaining hold is released, the refund completes, the order may
// complete, member_spend drops by it, the points clawback is recorded, and the
// customer gets one letter and the audit one row; a second press changes
// nothing.
func TestAnUnsentRefundPaysBackWhatDidNotShip(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	var sent atomic.Int64
	refunder := admintest.Refunder{Sent: &sent}
	s, adminPool := unsentDesk(t, refunder)
	o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
	spendBefore := memberSpend(t, o.User)
	stockBefore := stockOf(t, o.VariantB)

	if err := s.RefundUnsent(ctx, o.Number, unsentRequest(returns.UnsentCustomerWithdrew, unsentB(&o))); err != nil {
		t.Fatalf("refund: %v", err)
	}
	f := readUnsent(t, &o)
	if f.status != "completed" {
		t.Errorf("refund is %s, want completed", f.status)
	}
	if f.goods != unsentBRefund || f.delivery != 0 || f.card != unsentBRefund || f.credit != 0 {
		t.Errorf("froze goods/delivery/card/credit %d/%d/%d/%d, want %d/0/%d/0",
			f.goods, f.delivery, f.card, f.credit, unsentBRefund, unsentBRefund)
	}
	if f.cardPaid != unsentBRefund || f.providerRows != 1 || sent.Load() != 1 {
		t.Errorf("card refunded %d in %d rows after %d provider calls, want %d once",
			f.cardPaid, f.providerRows, sent.Load(), unsentBRefund)
	}
	if f.heldB != 0 || f.releasedB != 2 || f.releaseMoves != 1 || stockOf(t, o.VariantB) != stockBefore+2 {
		t.Errorf("B held/released/moves = %d/%d/%d, stock %d -> %d; want the two units released once",
			f.heldB, f.releasedB, f.releaseMoves, stockBefore, stockOf(t, o.VariantB))
	}
	if f.refundedEvents != 1 || f.clawbacks != 1 || f.clawed >= 0 {
		t.Errorf("refunded events %d, clawbacks %d of %d points; want one of each, negative",
			f.refundedEvents, f.clawbacks, f.clawed)
	}
	if got := memberSpend(t, o.User); got != spendBefore-unsentBRefund {
		t.Errorf("member_spend %d -> %d, want it lower by %d", spendBefore, got, unsentBRefund)
	}
	if f.mails != 1 || f.openAudits != 1 || f.completeAudits != 1 {
		t.Errorf("letters %d, return.refund_unsent rows %d, return.complete rows %d; want one each",
			f.mails, f.openAudits, f.completeAudits)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(f.payload, &keys); err != nil {
		t.Fatalf("read the letter's payload: %v", err)
	}
	if got := slices.Sorted(maps.Keys(keys)); !slices.Equal(got, []string{"order_id", "return_request_id"}) {
		t.Errorf("the letter carries %v, want the order and the refund only: %s", got, f.payload)
	}

	// Pressed again once it finished: those units are refunded, and nothing
	// more moves.
	if err := s.RefundUnsent(ctx, o.Number, unsentRequest(returns.UnsentCustomerWithdrew, unsentB(&o))); !db.IsConstraint(err, "return_within_shipment") {
		t.Fatalf("second press: %v, want the units refused as already refunded", err)
	}
	if again := readUnsent(t, &o); again.mails != 1 || again.providerRows != 1 || again.openAudits != 1 || again.releasedB != 2 {
		t.Errorf("the second press moved something: %+v", again)
	}

	// What the worker sends, read at delivery.
	var m email.OrderPartRefunded
	if err := json.Unmarshal(f.payload, &m); err != nil || m.OrderID != o.ID || m.ReturnRequestID != f.id {
		t.Fatalf("payload %+v, %v", m, err)
	}
	letter, ok, err := notice.NewRecipients(pool).PartRefund(ctx, &m)
	if err != nil || !ok {
		t.Fatalf("read the letter: %v, %t", err, ok)
	}
	if len(letter.Items) != 1 || letter.Items[0].Name != "點線筆記本" || letter.Items[0].Quantity != 2 ||
		letter.RefundCents != unsentBRefund || letter.CardCents != unsentBRefund || letter.CreditCents != 0 {
		t.Errorf("letter = %+v, want 點線筆記本 × 2 refunded %d to the card", letter, unsentBRefund)
	}

	// The order owes no parcel now, and completes through the order desk.
	if _, err := orderDesk(adminPool, refunder).Advance(ctx, o.Number, order.FulfillmentCompleted,
		uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("complete the order: %v", err)
	}
	if got := admintest.FulfillmentOf(t, pool, o.ID); got != "completed" {
		t.Errorf("order is %s, want completed", got)
	}
}

// Family 2: a refund larger than the card capture refunds the card first and
// the rest as credit; a credit-paid order makes no provider call.
func TestAnUnsentRefundPaysTheCardFirst(t *testing.T) {
	for _, tc := range []struct {
		name               string
		cardPaid           int64
		card, credit, sent int64
	}{
		{name: "card and credit", cardPaid: 50000, card: 50000, credit: unsentBRefund - 50000, sent: 1},
		{name: "credit only", cardPaid: 0, card: 0, credit: unsentBRefund, sent: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := admintest.StaffContext(t, pool)
			var sent atomic.Int64
			s, _ := unsentDesk(t, admintest.Refunder{Sent: &sent})
			o := admintest.PartlyShippedOrder(t, pool, tc.cardPaid)
			if err := s.RefundUnsent(ctx, o.Number, unsentRequest(returns.UnsentDamaged, unsentB(&o))); err != nil {
				t.Fatalf("refund: %v", err)
			}
			f := readUnsent(t, &o)
			if f.status != "completed" || f.card != tc.card || f.credit != tc.credit ||
				f.cardPaid != tc.card || f.creditPaid != tc.credit || sent.Load() != tc.sent {
				t.Errorf("%s card/credit froze %d/%d, paid %d/%d after %d provider calls; want %d/%d after %d",
					f.status, f.card, f.credit, f.cardPaid, f.creditPaid, sent.Load(), tc.card, tc.credit, tc.sent)
			}
		})
	}
}

// Family 3: what the door may refund is what did not ship, once; the units it
// refunds can no longer ship; the unit that shipped can still come back.
func TestAnUnsentRefundIsBoundedByWhatDidNotShip(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s, _ := unsentDesk(t, admintest.Refunder{})

	refused := func(t *testing.T, o *admintest.TwoLineOrder, units map[uuid.UUID]int32, rule string) {
		t.Helper()
		err := s.RefundUnsent(ctx, o.Number, unsentRequest(returns.UnsentLost, units))
		if !db.IsConstraint(err, rule) {
			t.Fatalf("refunding %v: %v, want refused by %s", units, err, rule)
		}
		var rows int
		if countErr := pool.QueryRow(ctx, `SELECT count(*) FROM return_requests WHERE order_id = $1 AND unsent AND status = 'approved'`,
			o.ID).Scan(&rows); countErr != nil || rows != 0 {
			t.Fatalf("a refused press left %d open refunds: %v", rows, countErr)
		}
	}
	t.Run("more than did not ship", func(t *testing.T) {
		o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
		refused(t, &o, map[uuid.UUID]int32{o.LineB: 3}, "return_within_shipment")
		refused(t, &o, map[uuid.UUID]int32{o.LineA: 1}, "return_within_shipment")
	})
	t.Run("nothing shipped", func(t *testing.T) {
		o := admintest.PaidTwoLineOrder(t, pool, admintest.TwoLineOrderTotal, false)
		refused(t, &o, unsentB(&o), "return_unsent_eligible")
	})
	t.Run("a second claim", func(t *testing.T) {
		o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
		if err := s.RefundUnsent(ctx, o.Number, unsentRequest(returns.UnsentLost, map[uuid.UUID]int32{o.LineB: 1})); err != nil {
			t.Fatalf("first claim: %v", err)
		}
		refused(t, &o, unsentB(&o), "return_within_shipment")
		if err := s.RefundUnsent(ctx, o.Number, unsentRequest(returns.UnsentLost, map[uuid.UUID]int32{o.LineB: 1})); err != nil {
			t.Fatalf("the last unit: %v", err)
		}
	})
	t.Run("ship after approval", func(t *testing.T) {
		pending := admintest.Refunder{State: refund.Pending}
		unpaid, adminPool := unsentDesk(t, pending)
		o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
		if err := unpaid.RefundUnsent(ctx, o.Number, unsentRequest(returns.UnsentLost, map[uuid.UUID]int32{o.LineB: 1})); !errors.Is(err, refunds.ErrUnsettled) {
			t.Fatalf("pending card refund: %v, want ErrUnsettled", err)
		}
		desk := orderDesk(adminPool, pending)
		view, err := desk.Order(ctx, o.Number)
		if err != nil {
			t.Fatalf("read the order page: %v", err)
		}
		if len(view.Shippable) != 1 || view.Shippable[0].Remaining != 1 {
			t.Fatalf("the ship form offers %+v, want B's one unit nobody refunded", view.Shippable)
		}
		err = desk.Ship(ctx, o.Number, orders.Dispatch{
			Carrier: "black_cat", Tracking: "AFTER-" + uuid.NewString()[:8],
			Lines: map[uuid.UUID]int32{o.LineB: 2},
		}, uuid.NullUUID{})
		if !errors.Is(err, orders.ErrQuantity) {
			t.Fatalf("shipping a refunded unit: %v, want the quantity notice", err)
		}
	})
	t.Run("the shipped unit comes back", func(t *testing.T) {
		o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
		if err := s.RefundUnsent(ctx, o.Number, unsentRequest(returns.UnsentDamaged, unsentB(&o))); err != nil {
			t.Fatalf("refund: %v", err)
		}
		if err := buyer.NewStore(admintest.StoreRolePool(t, pool)).OpenReturn(ctx, o.Number,
			uuid.NullUUID{UUID: o.User, Valid: true},
			&buyer.ReturnRequest{Lines: map[string]int32{o.LineB.String(): 1}}); err != nil {
			t.Fatalf("the customer returns the shipped unit of B: %v", err)
		}
	})
}

// Family 4: approved but unsettled. While the card refund is Stripe-pending
// the units are not given up, the order cannot complete, and settling is
// refused; the press that finds the money landed finishes it.
func TestAnUnpaidUnsentRefundNeitherSettlesNorCompletes(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	var sent atomic.Int64
	pending := admintest.Refunder{State: refund.Pending, Sent: &sent}
	s, adminPool := unsentDesk(t, pending)
	o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)

	err := s.RefundUnsent(ctx, o.Number, unsentRequest(returns.UnsentDamaged, unsentB(&o)))
	if !errors.Is(err, refunds.ErrUnsettled) {
		t.Fatalf("pending card refund: %v, want ErrUnsettled", err)
	}
	f := readUnsent(t, &o)
	if f.status != "approved" || f.heldB != 2 || f.mails != 0 || f.cardPaid != 0 {
		t.Fatalf("pending refund %s, B held %d, letters %d, card paid %d; want approved, 2 held, none, none",
			f.status, f.heldB, f.mails, f.cardPaid)
	}
	desk := orderDesk(adminPool, pending)
	if _, advanceErr := desk.Advance(ctx, o.Number, order.FulfillmentCompleted, uuid.NullUUID{UUID: staff, Valid: true}); !db.IsConstraint(advanceErr, "orders_finished_when_shipped") {
		t.Fatalf("completing with the refund unsettled: %v, want orders_finished_when_shipped", advanceErr)
	}
	if _, settleErr := adminPool.Exec(ctx, `SELECT settle_unsent_refund($1, $2, 'settle-early')`, f.id, staff); !db.IsConstraint(settleErr, "return_unsent_payout_outstanding") {
		t.Fatalf("settling with the refund unpaid: %v, want return_unsent_payout_outstanding", settleErr)
	}
	view, err := desk.Order(ctx, o.Number)
	if err != nil {
		t.Fatalf("read the order page: %v", err)
	}
	if !view.UnsentOpen || len(view.UnsentOpenLines) != 1 || view.UnsentOpenLines[0].Quantity != 2 {
		t.Errorf("order page: open %t with %+v, want Resume for B × 2", view.UnsentOpen, view.UnsentOpenLines)
	}
	for _, step := range view.Next {
		if step.Value == order.FulfillmentCompleted {
			t.Error("the order page offers completion while the refund is unsettled")
		}
	}

	// Stripe settles: the same request key now answers succeeded.
	settled, _ := unsentDesk(t, admintest.Refunder{Sent: &sent})
	if err := settled.RefundUnsent(ctx, o.Number, &refunds.UnsentRequest{Units: unsentB(&o)}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if f := readUnsent(t, &o); f.status != "completed" || f.providerRows != 1 || f.heldB != 0 || f.mails != 1 {
		t.Errorf("after resume: %s with %d provider rows, %d held, %d letters; want completed, 1, 0, 1",
			f.status, f.providerRows, f.heldB, f.mails)
	}
}

// Family 5: the remainder carries no delivery fee; the return that later takes
// back everything that arrived does.
func TestReturningTheRestAfterAnUnsentRefundRefundsDelivery(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	refunder := admintest.Refunder{}
	s, adminPool := unsentDesk(t, refunder)
	o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
	if err := s.RefundUnsent(ctx, o.Number, unsentRequest(returns.UnsentLost, unsentB(&o))); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if f := readUnsent(t, &o); f.delivery != 0 {
		t.Fatalf("the unsent refund froze %d of delivery, want none", f.delivery)
	}
	if err := buyer.NewStore(admintest.StoreRolePool(t, pool)).OpenReturn(ctx, o.Number,
		uuid.NullUUID{UUID: o.User, Valid: true},
		&buyer.ReturnRequest{Lines: map[string]int32{o.LineA.String(): 1, o.LineB.String(): 1}}); err != nil {
		t.Fatalf("return the rest: %v", err)
	}
	var returnID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM return_requests WHERE order_id = $1 AND NOT unsent`, o.ID).Scan(&returnID); err != nil {
		t.Fatalf("read the return: %v", err)
	}
	if err := admintest.ReturnDesk(adminPool, refunder).Decide(ctx, returnID.String(), string(returns.DecisionApprove), "", ""); err != nil {
		t.Fatalf("approve the return: %v", err)
	}
	var goods, delivery int64
	if err := pool.QueryRow(ctx, `SELECT goods_refund_cents, shipping_refund_cents FROM return_requests WHERE id = $1`,
		returnID).Scan(&goods, &delivery); err != nil {
		t.Fatalf("read the return's money: %v", err)
	}
	if goods != admintest.TwoLineOrderTotal-8000-unsentBRefund || delivery != 8000 {
		t.Errorf("the return of the rest froze goods %d, delivery %d; want %d and the 8000 delivery",
			goods, delivery, admintest.TwoLineOrderTotal-8000-unsentBRefund)
	}
}

// Family 6: a unit written off leaves stock where the hold put it, and tells
// nobody it is back; a unit the customer withdrew goes back on sale.
func TestAWrittenOffUnitStaysOffSaleAndAWithdrawnOneComesBack(t *testing.T) {
	for _, reason := range returns.UnsentReasons {
		t.Run(string(reason), func(t *testing.T) {
			ctx, _ := admintest.StaffContext(t, pool)
			s, _ := unsentDesk(t, admintest.Refunder{})
			o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
			before := stockOf(t, o.VariantB)
			waiting := admintest.SubscribeAtSafetyStock(t, pool, o.VariantB)
			if err := s.RefundUnsent(ctx, o.Number, unsentRequest(reason, unsentB(&o))); err != nil {
				t.Fatalf("refund: %v", err)
			}
			f := readUnsent(t, &o)
			var notified int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM stock_notifications WHERE id = ANY($1::uuid[]) AND notified_at IS NOT NULL`,
				waiting).Scan(&notified); err != nil {
				t.Fatalf("read restock notices: %v", err)
			}
			if reason == returns.UnsentCustomerWithdrew {
				if f.stockB != before+2 || f.releasedB != 2 || f.consumedB != 1 {
					t.Errorf("withdrawn: stock %d -> %d, released %d, consumed %d; want +2 on sale", before, f.stockB, f.releasedB, f.consumedB)
				}
				admintest.AssertRestockQueued(t, pool, waiting)
				return
			}
			if f.stockB != before || f.releasedB != 0 || f.consumedB != 3 || f.writeOffAdjustments != 0 {
				t.Errorf("written off: stock %d -> %d, released %d, consumed %d, adjustments %d; want unchanged, all consumed",
					before, f.stockB, f.releasedB, f.consumedB, f.writeOffAdjustments)
			}
			if notified != 0 {
				t.Errorf("%d restock notices for units that do not exist", notified)
			}
		})
	}
}

// A press naming other units than the refund already open is refused, before
// any money moves, instead of paying the open one.
func TestARepeatPressMustNameTheOpenRefundsUnits(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	var sent atomic.Int64
	pending := admintest.Refunder{State: refund.Pending, Sent: &sent}
	s, _ := unsentDesk(t, pending)
	o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
	if err := s.RefundUnsent(ctx, o.Number, unsentRequest(returns.UnsentLost, map[uuid.UUID]int32{o.LineB: 1})); !errors.Is(err, refunds.ErrUnsettled) {
		t.Fatalf("first press: %v", err)
	}
	calls := sent.Load()
	for name, units := range map[string]map[uuid.UUID]int32{
		"more units":  unsentB(&o),
		"other lines": {o.LineA: 1},
		"no units":    {},
	} {
		if err := s.RefundUnsent(ctx, o.Number, unsentRequest(returns.UnsentLost, units)); !errors.Is(err, refunds.ErrUnsentDiffers) {
			t.Errorf("%s: %v, want ErrUnsentDiffers", name, err)
		}
		if _, err := s.UnsentRefundPreview(ctx, o.Number, unsentRequest(returns.UnsentLost, units)); !errors.Is(err, refunds.ErrUnsentDiffers) {
			t.Errorf("%s preview: %v, want ErrUnsentDiffers", name, err)
		}
	}
	if sent.Load() != calls {
		t.Errorf("a refused press made %d provider calls", sent.Load()-calls)
	}
	preview, err := s.UnsentRefundPreview(ctx, o.Number, &refunds.UnsentRequest{Units: map[uuid.UUID]int32{o.LineB: 1}})
	if err != nil || !preview.Open || preview.RefundCents != 46300 {
		t.Errorf("preview of the open refund = %+v, %v; want Resume of 46300", preview, err)
	}
}

// The order page offers the door only on a paid order that sent a parcel and
// still owes units, and offers it for exactly those.
func TestTheOrderPageOffersTheUnsentRefundForWhatIsOwed(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	adminPool := admintest.AdminRolePool(t, pool)
	desk := orderDesk(adminPool, admintest.Refunder{})
	read := func(t *testing.T, number string) admin.OrderView {
		t.Helper()
		view, err := desk.Order(ctx, number)
		if err != nil {
			t.Fatalf("read the order page: %v", err)
		}
		return view
	}
	shipped := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
	if v := read(t, shipped.Number); !v.UnsentOffered || v.UnsentOpen || v.RefundOffered {
		t.Errorf("partly shipped: unsent offered %t, open %t, before-shipment offered %t; want only the unsent door",
			v.UnsentOffered, v.UnsentOpen, v.RefundOffered)
	}
	unshipped := admintest.PaidTwoLineOrder(t, pool, admintest.TwoLineOrderTotal, false)
	if v := read(t, unshipped.Number); v.UnsentOffered || !v.RefundOffered {
		t.Errorf("nothing shipped: unsent offered %t, before-shipment offered %t; want only the refund before shipment",
			v.UnsentOffered, v.RefundOffered)
	}
	s := refunds.NewStore(adminPool, admintest.Refunder{}, nil)
	if err := s.RefundUnsent(ctx, shipped.Number, unsentRequest(returns.UnsentLost, unsentB(&shipped))); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if v := read(t, shipped.Number); v.UnsentOffered || v.UnsentOpen || len(v.Shippable) != 0 {
		t.Errorf("all owed units refunded: offered %t, open %t, shippable %+v; want nothing left to offer",
			v.UnsentOffered, v.UnsentOpen, v.Shippable)
	}
}

// The returns queue shows an unsent refund as what it is, and sends staff to
// the order page rather than offering to inspect units that never left: not
// while its card refund is pending, and not as a shortfall once it closed.
func TestTheReturnsQueueSendsAnUnsentRefundToItsOrder(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s, adminPool := unsentDesk(t, admintest.Refunder{State: refund.Pending})
	o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
	// The parcel that carried B's other unit arrived today, which would put a
	// return of it inside the seven days.
	if _, err := pool.Exec(ctx, `UPDATE order_shipments SET delivered_at = now() WHERE order_id = $1`, o.ID); err != nil {
		t.Fatalf("deliver the parcel: %v", err)
	}
	if err := s.RefundUnsent(ctx, o.Number, unsentRequest(returns.UnsentSupplierFailed, unsentB(&o))); !errors.Is(err, refunds.ErrUnsettled) {
		t.Fatalf("refund with the card refund pending: %v", err)
	}
	f := readUnsent(t, &o)
	desk := admintest.ReturnDesk(adminPool, admintest.Refunder{})
	queueRow := func(t *testing.T) admin.Return {
		t.Helper()
		queue, err := desk.RequestQueue(ctx, f.id)
		if err != nil {
			t.Fatalf("read the returns queue: %v", err)
		}
		for i := range queue.Rows {
			if queue.Rows[i].ID == f.id.String() {
				return queue.Rows[i]
			}
		}
		t.Fatalf("the queue does not list unsent refund %s", f.id)
		return admin.Return{}
	}
	row := queueRow(t)
	if !row.Unsent || row.AwaitingGoods() || row.Window != returns.WindowUndelivered ||
		strings.Contains(row.Reason, "supplier_failed") {
		t.Errorf("approved queue row = unsent %t, awaiting goods %t, window %s, reason %q",
			row.Unsent, row.AwaitingGoods(), row.Window, row.Reason)
	}
	for _, l := range row.Lines {
		if l.Window != returns.WindowUndelivered {
			t.Errorf("line %s reads window %s, want undelivered: its units never left", l.Line(), l.Window)
		}
	}

	settled, _ := unsentDesk(t, admintest.Refunder{})
	if err := settled.RefundUnsent(ctx, o.Number, &refunds.UnsentRequest{Units: unsentB(&o)}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	row = queueRow(t)
	if row.StatusIntent() != components.IntentNeutral {
		t.Errorf("a closed unsent refund is coloured %v, want neutral like a cancellation: nothing came back", row.StatusIntent())
	}
	var page strings.Builder
	zh := i18n.WithLocale(ctx, i18n.ZhHant)
	if err := admin.Returns(layouts.Page{Title: "returns"}, admin.ReturnsView{Rows: []admin.Return{row}}).Render(zh, &page); err != nil {
		t.Fatalf("render the queue: %v", err)
	}
	door := `<a href="/admin/orders/` + o.Number + `">` + i18n.T(zh, i18n.KeyAdminUnsentTitle) + `</a>`
	if rendered := page.String(); strings.Contains(rendered, i18n.T(zh, i18n.KeyAdminRetShortfall)) ||
		!strings.Contains(rendered, i18n.T(zh, i18n.KeyAdminReturnUnsentRefunded)) || !strings.Contains(rendered, door) {
		t.Errorf("the closed unsent refund on the queue: shortfall shown %t, status %q, sent to its order page %t",
			strings.Contains(rendered, i18n.T(zh, i18n.KeyAdminRetShortfall)), row.StatusText, strings.Contains(rendered, door))
	}
}

// Family 9, through the handler: the first POST renders what the refund pays
// and moves nothing; a bad code or a blank note is refused before money
// moves; the confirmed POST refunds.
func TestTheUnsentRefundConfirmsBeforeItPays(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	var sent atomic.Int64
	s, _ := unsentDesk(t, admintest.Refunder{Sent: &sent})
	h := refunds.NewHandler(s, nil, slog.New(slog.DiscardHandler))
	o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
	qty := "qty_" + o.LineB.String()

	post := func(form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/admin/orders/"+o.Number+"/refund-unsent", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("number", o.Number)
		rec := httptest.NewRecorder()
		h.RefundUnsent(rec, req)
		return rec
	}
	nothingWritten := func(t *testing.T, step string) {
		t.Helper()
		var requests, audits int
		if err := pool.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM return_requests WHERE order_id = $1),
			       (SELECT count(*) FROM audit_events WHERE action = 'return.refund_unsent'
			          AND after->>'order_number' = $2)`, o.ID, o.Number).Scan(&requests, &audits); err != nil {
			t.Fatalf("count: %v", err)
		}
		if requests != 0 || audits != 0 || sent.Load() != 0 || stockOf(t, o.VariantB) != 0 {
			t.Fatalf("%s wrote %d refunds and %d audit rows, made %d provider calls, and left B at %d on sale",
				step, requests, audits, sent.Load(), stockOf(t, o.VariantB))
		}
	}
	redirects := func(t *testing.T, rec *httptest.ResponseRecorder, query string) {
		t.Helper()
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/orders/"+o.Number+query {
			t.Fatalf("POST = %d to %q, want 303 to %s", rec.Code, rec.Header().Get("Location"), query)
		}
	}

	first := post(url.Values{qty: {"2"}, "reason": {"customer_withdrew"}})
	body := html.UnescapeString(first.Body.String())
	if first.Code != http.StatusOK || !strings.Contains(body, `name="confirm"`) ||
		!strings.Contains(body, money.TWD(unsentBRefund)) || !strings.Contains(body, `value="92600"`) ||
		!strings.Contains(body, `name="`+qty+`" value="2"`) || !strings.Contains(body, "點線筆記本 × 2") {
		t.Fatalf("first POST = %d, want the confirmation of B × 2 for %s: %s", first.Code, money.TWD(unsentBRefund), body)
	}
	nothingWritten(t, "the first POST")

	redirects(t, post(url.Values{qty: {"2"}, "reason": {"broken"}}), "?unsentreason=1")
	nothingWritten(t, "an unknown reason")
	redirects(t, post(url.Values{qty: {"0"}, "reason": {"damaged"}}), "?unsentbounds=1")
	redirects(t, post(url.Values{qty: {"3"}, "reason": {"damaged"}}), "?unsentbounds=1")
	redirects(t, post(url.Values{qty: {"two"}, "reason": {"damaged"}}), "?unsentbounds=1")
	nothingWritten(t, "a bad quantity")

	stale := post(url.Values{qty: {"2"}, "reason": {"customer_withdrew"}, "confirm": {"refund-unsent"}, "total": {"1"}, "note": {unsentNote}})
	if stale.Code != http.StatusOK || !strings.Contains(stale.Body.String(), `value="92600"`) {
		t.Fatalf("stale total = %d, want the confirmation again", stale.Code)
	}
	blank := post(url.Values{qty: {"2"}, "reason": {"customer_withdrew"}, "confirm": {"refund-unsent"}, "total": {"92600"}, "note": {"  "}})
	if blank.Code != http.StatusUnprocessableEntity || !strings.Contains(blank.Body.String(), `aria-invalid="true"`) {
		t.Fatalf("blank note = %d, want 422 marking the note", blank.Code)
	}
	long := post(url.Values{qty: {"2"}, "reason": {"customer_withdrew"}, "confirm": {"refund-unsent"}, "total": {"92600"},
		"note": {strings.Repeat("損", returns.MaxResolutionRunes+1)}})
	if long.Code != http.StatusUnprocessableEntity {
		t.Fatalf("note of %d runes = %d, want 422", returns.MaxResolutionRunes+1, long.Code)
	}
	nothingWritten(t, "a refused confirmation")

	redirects(t, post(url.Values{qty: {"2"}, "reason": {"customer_withdrew"}, "confirm": {"refund-unsent"}, "total": {"92600"}, "note": {unsentNote}}),
		"?unsentrefunded=1")
	if f := readUnsent(t, &o); f.status != "completed" || sent.Load() != 1 || f.stockB != 2 {
		t.Fatalf("after confirming: %s, %d provider calls, B at %d; want completed, one, 2 back on sale", f.status, sent.Load(), f.stockB)
	}
}

// Family 10: once settled, the refund files a 折讓 against the live invoice
// for the whole dollars that went back, past ECPay's void window, under the
// presser's name.
func TestAnUnsentRefundFilesItsAllowance(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	fake, invoices := fakeECPay(t, time.Now().AddDate(0, -4, 0))
	s := refunds.NewStore(pool, admintest.Refunder{}, invoices)
	o := admintest.PartlyShippedOrder(t, pool, 100000)
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_preferences (order_id, invoice_type, customer_name, customer_email)
		VALUES ($1, 'member_carrier', '買受人', 'buyer@example.com')`, o.ID); err != nil {
		t.Fatalf("record invoice preference: %v", err)
	}
	if err := invoices.ClaimDue(ctx, &outbox.InvoiceDue{OrderNumber: o.Number, Trigger: "evt_" + o.Number}); err != nil {
		t.Fatalf("claim the invoice due: %v", err)
	}
	issueNow(t, ctx, invoices, o.ID)

	if err := s.RefundUnsent(ctx, o.Number, unsentRequest(returns.UnsentDamaged, unsentB(&o))); err != nil {
		t.Fatalf("refund: %v", err)
	}
	var status string
	var amount int64
	var actor uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT status, amount_cents, actor_user_id FROM invoice_operations
		WHERE order_id = $1 AND kind = 'allowance'`, o.ID).Scan(&status, &amount, &actor); err != nil {
		t.Fatalf("read the allowance: %v", err)
	}
	if status != "pending" || amount != unsentBRefund || actor != staff {
		t.Errorf("allowance is %s for %d by %s, want pending (the buyer agrees by mail) for %d by the presser",
			status, amount, actor, unsentBRefund)
	}
	if got := fake.Calls("/B2CInvoice/AllowanceByCollegiate"); got != 1 {
		t.Errorf("%d allowances asked of ECPay, want one", got)
	}
	if got := fake.Calls("/B2CInvoice/Invalid"); got != 0 {
		t.Errorf("%d voids: the partial sale stands", got)
	}
}

// An order with no live invoice yet refunds without a 折讓, which staff file
// from the order page once the invoice exists.
func TestAnUnsentRefundWithNoInvoiceYetStillRefunds(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	_, invoices := fakeECPay(t, time.Now())
	s := refunds.NewStore(pool, admintest.Refunder{}, invoices)
	o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
	if err := s.RefundUnsent(ctx, o.Number, unsentRequest(returns.UnsentLost, unsentB(&o))); err != nil {
		t.Fatalf("refund with no invoice: %v", err)
	}
	if f := readUnsent(t, &o); f.status != "completed" {
		t.Errorf("refund is %s, want completed", f.status)
	}
}
