//go:build integration

package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
)

// The unsent refund (#1965) is reachable only through open_unsent_refund and
// settle_unsent_refund until the back office calls them, so these drive the
// two doors as the admin role, the way the back office will, and read the
// results as the owner. Every fixture commits under fresh identities: the race
// needs two real transactions, and the ledgers it writes are append-only.

// unsentOrder is a paid order of two lines whose first parcel has gone. Line a
// (one unit at NT$1,200) went whole; line b (three units at NT$500) sent one,
// so two units of b are still owed and still held. A NT$200 discount and a
// NT$80 delivery fee make the discount share and the delivery rule visible.
type unsentOrder struct {
	number             string
	id, user, staff    uuid.UUID
	lineA, lineB       uuid.UUID
	variantA, variantB uuid.UUID
}

// unsentOrderTotal is 120000 + 3 × 50000 − 20000 discount + 8000 delivery.
const unsentOrderTotal = 258000

// unsentClaim is one line of an unsent refund.
type unsentClaim struct {
	line     uuid.UUID
	quantity int32
}

// newUnsentOrder pays cardCents by card and the rest of the total with store
// credit, and ships the first parcel.
func newUnsentOrder(t *testing.T, cardCents int64) unsentOrder {
	t.Helper()
	return newPaidTwoLineOrder(t, cardCents, true)
}

// newPaidTwoLineOrder is newUnsentOrder, with or without its first parcel.
func newPaidTwoLineOrder(t *testing.T, cardCents int64, shipped bool) unsentOrder {
	t.Helper()
	ctx := t.Context()
	suffix := uuid.NewString()[:8]
	var o unsentOrder
	creditCents := unsentOrderTotal - cardCents

	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	exec := func(stmt string, args ...any) {
		t.Helper()
		if _, execErr := tx.Exec(ctx, stmt, args...); execErr != nil {
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}
	row := func(dest any, stmt string, args ...any) {
		t.Helper()
		if scanErr := tx.QueryRow(ctx, stmt, args...).Scan(dest); scanErr != nil {
			t.Fatalf("%s: %v", stmt, scanErr)
		}
	}

	row(&o.user, `INSERT INTO users (email, role, full_name)
	              VALUES ('unsent-customer-' || $1 || '@goen.invalid', 'customer', '未寄出顧客')
	              RETURNING id`, suffix)
	if creditCents > 0 {
		exec(`SELECT post_store_credit($1, $2, '測試發放', NULL, $3, NULL)`,
			o.user, creditCents, "grant:"+o.user.String())
	}
	row(&o.staff, `INSERT INTO users (email, role, full_name)
	               VALUES ('unsent-staff-' || $1 || '@goen.invalid', 'staff', '出貨人員')
	               RETURNING id`, suffix)

	var brand, category uuid.UUID
	row(&brand, `INSERT INTO brands (slug, name) VALUES ('unsent-' || $1, 'Unsent') RETURNING id`, suffix)
	// Roots share one position space (categories_position_key). The fixtures
	// take 0 and 1 inside transactions this one cannot see, so start well
	// clear of them.
	row(&category, `INSERT INTO categories (slug, name, position)
	                SELECT 'unsent-' || $1, '未寄出', greatest(max(position), 10000) + 1
	                FROM categories WHERE parent_id IS NULL RETURNING id`, suffix)

	variant := func(name string, price int64, stock int) (uuid.UUID, string) {
		t.Helper()
		slug := "unsent-" + name + "-" + suffix
		var product, id uuid.UUID
		row(&product, `INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		               VALUES ($1, $2, $3, '未寄出測試', 'active', now()) RETURNING id`,
			brand, category, slug)
		row(&id, `INSERT INTO product_variants (product_id, sku, price_cents, safety_stock, position)
		          VALUES ($1, upper($2), $3, 0, 0) RETURNING id`, product, slug, price)
		exec(`SELECT record_inventory_movement($1, $2, 'receipt', 'seed:' || $3, 'admin', NULL, NULL)`,
			id, stock, slug)
		return id, slug
	}
	var slugA, slugB string
	o.variantA, slugA = variant("a", 120000, 1)
	o.variantB, slugB = variant("b", 50000, 3)

	exec(`INSERT INTO shipping_methods (id, code)
	      VALUES ('5e5e0001-0000-4000-8000-000000000001', 'unsent_home') ON CONFLICT DO NOTHING`)
	exec(`INSERT INTO shipping_method_versions (id, method_id, name, fee_cents)
	      VALUES ('5e5e0002-0000-4000-8000-000000000001', '5e5e0001-0000-4000-8000-000000000001',
	              '宅配', 8000) ON CONFLICT DO NOTHING`)
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents, discount_cents)
		VALUES (next_order_number(), $1, '5e5e0002-0000-4000-8000-000000000001', 'unsent_home',
		        '宅配', 8000, 20000)
		RETURNING id, order_number`, o.user).Scan(&o.id, &o.number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	line := func(variantID uuid.UUID, slug string, price int64, quantity, position int) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		row(&id, `INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents,
		                                   quantity, position)
		          VALUES ($1, $2, upper($3), '未寄出測試', $4, $5, $6) RETURNING id`,
			o.id, variantID, slug, price, quantity, position)
		exec(`SELECT hold_inventory($1, $2, $3, interval '30 minutes', 'hold:' || $4)`,
			o.id, variantID, quantity, slug)
		return id
	}
	o.lineA = line(o.variantA, slugA, 120000, 1, 0)
	o.lineB = line(o.variantB, slugB, 50000, 3, 1)
	exec(`INSERT INTO order_private_data (order_id, email, recipient_name, phone,
	                                      postal_code, city, district, street)
	      VALUES ($1, 'unsent@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		o.id)

	if creditCents > 0 {
		exec(`SELECT spend_store_credit($1, $2)`, o.id, -creditCents)
	}
	if cardCents > 0 {
		exec(`SELECT open_payment($1, 'cs_unsent_' || $2, $3)`, o.id, suffix, cardCents)
		exec(`SELECT capture_payment('cs_unsent_' || $1, $2, NULL, NULL)`, suffix, cardCents)
	}
	exec(`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, o.id)
	exec(`SELECT award_loyalty_points($1)`, o.id)

	if shipped {
		var parcel uuid.UUID
		row(&parcel, `INSERT INTO order_shipments (order_id, carrier, tracking_number)
		              VALUES ($1, 'black_cat', 'UNSENT-' || $2) RETURNING id`, o.id, suffix)
		for _, sent := range []struct {
			line, variant uuid.UUID
		}{{o.lineA, o.variantA}, {o.lineB, o.variantB}} {
			exec(`INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
			      VALUES ($1, $2, $3, 1)`, o.id, parcel, sent.line)
			exec(`SELECT consume_reservation_partial(r.id, 1) FROM inventory_reservations r
			      WHERE r.order_id = $1 AND r.variant_id = $2 AND r.state = 'held'`, o.id, sent.variant)
		}
		exec(`UPDATE orders SET fulfillment_status = 'shipped' WHERE id = $1`, o.id)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the unsent fixture: %v", err)
	}
	return o
}

// asAdminRole runs one statement as the back office's database role and
// commits it; every other statement here runs as the owner, who is subject to
// no REVOKE.
func asAdminRole(t *testing.T, dest any, stmt string, args ...any) error {
	t.Helper()
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, roleErr := tx.Exec(ctx, `SET LOCAL ROLE admin`); roleErr != nil {
		t.Fatalf("set role admin: %v", roleErr)
	}
	if dest == nil {
		_, err = tx.Exec(ctx, stmt, args...)
	} else {
		err = tx.QueryRow(ctx, stmt, args...).Scan(dest)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const unsentNote = "倉庫點貨時發現外盒破損"

// open presses the door as the order's staff member.
func (o unsentOrder) open(t *testing.T, reason string, claims ...unsentClaim) (uuid.UUID, error) {
	t.Helper()
	ids := make([]string, len(claims))
	quantities := make([]int32, len(claims))
	for i, c := range claims {
		ids[i], quantities[i] = c.line.String(), c.quantity
	}
	var id uuid.UUID
	err := asAdminRole(t, &id,
		`SELECT open_unsent_refund($1, $2::uuid[], $3::integer[], $4, $5, $6, $7)`,
		o.number, ids, quantities, reason, unsentNote, o.staff, "open-"+uuid.NewString())
	return id, err
}

func (o unsentOrder) mustOpen(t *testing.T, reason string, claims ...unsentClaim) uuid.UUID {
	t.Helper()
	id, err := o.open(t, reason, claims...)
	if err != nil {
		t.Fatalf("open an unsent refund of order %s: %v", o.number, err)
	}
	return id
}

// pay does what the back office's return payout does for an approved return:
// the card half through a provider attempt in the given state, the credit
// half, and — once both have landed — the customer's refunded event and the
// points clawback.
func (o unsentOrder) pay(t *testing.T, returnID uuid.UUID, cardState string) {
	t.Helper()
	var card, credit int64
	if err := schemaPool(t).QueryRow(t.Context(),
		`SELECT card_refund_cents, credit_refund_cents FROM return_requests WHERE id = $1`,
		returnID).Scan(&card, &credit); err != nil {
		t.Fatalf("read the frozen split: %v", err)
	}
	if card > 0 {
		var refundID uuid.UUID
		if err := asAdminRole(t, &refundID, `SELECT claim_return_refund_execution($1, $2, $3)`,
			returnID, o.staff, uuid.NewString()); err != nil {
			t.Fatalf("claim the card refund: %v", err)
		}
		if err := asAdminRole(t, nil, `SELECT record_refund_`+cardState+`($1, $2, $3, $4)`,
			refundID, "re_"+refundID.String(), o.staff, uuid.NewString()); err != nil {
			t.Fatalf("record the card refund %s: %v", cardState, err)
		}
	}
	if credit > 0 {
		if err := asAdminRole(t, nil, `SELECT compensate_return_with_credit($1, $2, $3)`,
			returnID, credit, o.staff); err != nil {
			t.Fatalf("post the credit half: %v", err)
		}
	}
	if card > 0 && cardState != "succeeded" {
		return
	}
	if card+credit > 0 {
		if err := asAdminRole(t, nil, `INSERT INTO order_events (order_id, kind, actor_user_id, return_request_id)
		                               VALUES ($1, 'refunded', $2, $3)`, o.id, o.staff, returnID); err != nil {
			t.Fatalf("record the refunded event: %v", err)
		}
		if err := asAdminRole(t, nil, `SELECT reverse_return_points($1)`, returnID); err != nil {
			t.Fatalf("claw back the points: %v", err)
		}
	}
}

func (o unsentOrder) settle(t *testing.T, returnID uuid.UUID) error {
	t.Helper()
	return asAdminRole(t, nil, `SELECT settle_unsent_refund($1, $2, $3)`,
		returnID, o.staff, "settle-"+uuid.NewString())
}

// complete moves the order to completed as the status form does.
func (o unsentOrder) complete(t *testing.T) error {
	t.Helper()
	return asAdminRole(t, nil, `UPDATE orders SET fulfillment_status = 'completed', completed_at = now()
	                            WHERE id = $1`, o.id)
}

// shipB sends n more units of line b in a new parcel, as the admin role.
func (o unsentOrder) shipB(t *testing.T, n int32) error {
	t.Helper()
	var parcel uuid.UUID
	if err := asAdminRole(t, &parcel, `INSERT INTO order_shipments (order_id, carrier, tracking_number)
	                                   VALUES ($1, 'black_cat', 'UNSENT-B-' || $2) RETURNING id`,
		o.id, uuid.NewString()[:8]); err != nil {
		t.Fatalf("record the parcel: %v", err)
	}
	return asAdminRole(t, nil, `INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
	                            VALUES ($1, $2, $3, $4)`, o.id, parcel, o.lineB, n)
}

func (o unsentOrder) heldB(t *testing.T) int {
	t.Helper()
	return unsentInt(t, `SELECT coalesce(sum(quantity), 0)::int FROM inventory_reservations
	                     WHERE order_id = $1 AND variant_id = $2 AND state = 'held'`, o.id, o.variantB)
}

func (o unsentOrder) stockB(t *testing.T) int {
	t.Helper()
	return unsentInt(t, `SELECT stock_quantity FROM product_variants WHERE id = $1`, o.variantB)
}

func unsentInt(t *testing.T, stmt string, args ...any) int {
	t.Helper()
	var n int
	if err := schemaPool(t).QueryRow(t.Context(), stmt, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
	return n
}

func requireRule(t *testing.T, what string, err error, rule string) {
	t.Helper()
	if got := db.ConstraintName(err); got != rule {
		t.Fatalf("%s: refused by %q (%v), want %q", what, got, err, rule)
	}
}

// TestAnUnsentRefundPaysBackWhatDidNotShip is the whole path in the schema: the
// two owed units of line b are refunded at their discounted price, card first,
// with no delivery; once paid, settling puts them back on sale and completes
// the refund, and only then may the order complete.
func TestAnUnsentRefundPaysBackWhatDidNotShip(t *testing.T) {
	o := newUnsentOrder(t, 208000)
	spendBefore := unsentInt(t, `SELECT member_spend($1)::int`, o.user)
	if spendBefore != unsentOrderTotal {
		t.Fatalf("member_spend before = %d, want the order's %d", spendBefore, unsentOrderTotal)
	}

	returnID := o.mustOpen(t, "customer_withdrew", unsentClaim{o.lineB, 2})

	// Goods: 2 × 50000 of a 270000 subtotal less 20000, rounded up to a whole
	// NT$: ceil(100000 × 250000 / 270000 / 100) × 100 = 92600. Card first: the
	// 208000 capture covers it, so no credit.
	var status, reason, resolution string
	var unsent bool
	var goods, shipping, card, credit int64
	if err := schemaPool(t).QueryRow(t.Context(), `
		SELECT status, unsent, reason, resolution, goods_refund_cents, shipping_refund_cents,
		       card_refund_cents, credit_refund_cents
		FROM return_requests WHERE id = $1`, returnID).Scan(
		&status, &unsent, &reason, &resolution, &goods, &shipping, &card, &credit); err != nil {
		t.Fatalf("read the refund: %v", err)
	}
	if status != "approved" || !unsent || reason != "customer_withdrew" || resolution != unsentNote {
		t.Errorf("refund is %s unsent=%t reason %q note %q", status, unsent, reason, resolution)
	}
	if goods != 92600 || shipping != 0 || card != 92600 || credit != 0 {
		t.Errorf("goods/delivery/card/credit = %d/%d/%d/%d, want 92600/0/92600/0",
			goods, shipping, card, credit)
	}
	if n := unsentInt(t, `SELECT count(*)::int FROM audit_events
	                      WHERE action = 'return.refund_unsent' AND entity_id = $1
	                        AND actor_user_id = $2 AND after->>'reason' = 'customer_withdrew'
	                        AND after->>'order_number' = $3
	                        AND after->>'card_refund_cents' = '92600'
	                        AND after->>'credit_refund_cents' = '0'`, returnID, o.staff, o.number); n != 1 {
		t.Errorf("%d return.refund_unsent audit rows, want 1", n)
	}

	o.pay(t, returnID, "succeeded")
	stock := o.stockB(t)
	if err := o.settle(t, returnID); err != nil {
		t.Fatalf("settle: %v", err)
	}

	if got := unsentInt(t, `SELECT count(*)::int FROM return_requests WHERE id = $1 AND status = 'completed'`,
		returnID); got != 1 {
		t.Error("the refund is not completed after settling")
	}
	if got := unsentInt(t, `SELECT count(*)::int FROM return_request_lines
	                        WHERE return_request_id = $1 AND received_quantity = 0 AND restocked_quantity = 0`,
		returnID); got != 1 {
		t.Error("the refund's line is not closed as nothing received")
	}
	if held := o.heldB(t); held != 0 {
		t.Errorf("order still holds %d of b", held)
	}
	if got := o.stockB(t); got != stock+2 {
		t.Errorf("stock of b = %d, want %d: the withdrawn units go back on sale", got, stock+2)
	}
	if n := unsentInt(t, `SELECT count(*)::int FROM inventory_movements m
	                      JOIN inventory_reservations r ON r.id = m.source_id
	                      WHERE r.order_id = $1 AND m.reason = 'release' AND m.delta = 2
	                        AND m.actor_user_id = $2`, o.id, o.staff); n != 1 {
		t.Errorf("%d release movements of the two units by the staff member, want 1", n)
	}
	if n := unsentInt(t, `SELECT count(*)::int FROM audit_events
	                      WHERE action = 'return.complete' AND entity_id = $1`, returnID); n != 1 {
		t.Errorf("%d return.complete audit rows, want 1", n)
	}
	if got := unsentInt(t, `SELECT member_spend($1)::int`, o.user); got != spendBefore-92600 {
		t.Errorf("member_spend = %d, want %d less the refund", got, spendBefore)
	}

	// A second settle press finds it done and moves nothing.
	if err := o.settle(t, returnID); err != nil {
		t.Errorf("a repeated settle: %v", err)
	}
	if got := o.stockB(t); got != stock+2 {
		t.Errorf("a repeated settle moved stock to %d", got)
	}

	if err := o.complete(t); err != nil {
		t.Fatalf("an order whose unsent units are refunded and settled cannot complete: %v", err)
	}
}

// TestReturningTheRestAfterAnUnsentRefundRefundsDelivery: the unsent refund
// keeps the delivery fee, because a parcel arrived; a later return of all that
// arrived completes the whole order's rescission and takes the fee back.
func TestReturningTheRestAfterAnUnsentRefundRefundsDelivery(t *testing.T) {
	o := newUnsentOrder(t, 208000)
	unsentID := o.mustOpen(t, "lost", unsentClaim{o.lineB, 2})
	o.pay(t, unsentID, "succeeded")
	if err := o.settle(t, unsentID); err != nil {
		t.Fatalf("settle: %v", err)
	}

	var customer uuid.UUID
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE store`); err != nil {
		t.Fatalf("set role store: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO return_requests (order_id, requested_by_user_id, reason)
	                            VALUES ($1, $2, '不需要了') RETURNING id`, o.id, o.user).Scan(&customer); err != nil {
		t.Fatalf("the customer returns what arrived: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
	                           VALUES ($1, $2, $3, 1), ($1, $2, $4, 1)`, o.id, customer, o.lineA, o.lineB); err != nil {
		t.Fatalf("the customer returns what arrived: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the customer's return: %v", err)
	}
	if err := asAdminRole(t, nil, `UPDATE return_requests SET status = 'approved', decided_at = now()
	                               WHERE id = $1`, customer); err != nil {
		t.Fatalf("approve the customer's return: %v", err)
	}

	// Goods: everything is now returned, so the cumulative share is the whole
	// 250000 less the 92600 the unsent refund froze. Delivery 8000 rides on this
	// approval. Card: 208000 captured less 92600 reserved; credit: the 50000 spent.
	var goods, shipping, card, credit int64
	if err := schemaPool(t).QueryRow(ctx, `
		SELECT goods_refund_cents, shipping_refund_cents, card_refund_cents, credit_refund_cents
		FROM return_requests WHERE id = $1`, customer).Scan(&goods, &shipping, &card, &credit); err != nil {
		t.Fatalf("read the customer's return: %v", err)
	}
	if goods != 157400 || shipping != 8000 || card != 115400 || credit != 50000 {
		t.Errorf("goods/delivery/card/credit = %d/%d/%d/%d, want 157400/8000/115400/50000",
			goods, shipping, card, credit)
	}
}

// TestAnUnsentRefundIsBoundedByWhatDidNotShip covers the door's refusals and
// its one resume.
func TestAnUnsentRefundIsBoundedByWhatDidNotShip(t *testing.T) {
	o := newUnsentOrder(t, unsentOrderTotal)
	lineB := []string{o.lineB.String()}

	_, err := o.open(t, "damaged", unsentClaim{o.lineA, 1})
	requireRule(t, "a unit that shipped", err, "return_within_shipment")
	_, err = o.open(t, "damaged", unsentClaim{o.lineB, 3})
	requireRule(t, "three of a line that owes two", err, "return_within_shipment")
	_, err = o.open(t, "broken", unsentClaim{o.lineB, 1})
	requireRule(t, "an unknown reason", err, "return_requests_unsent_reason_known")
	_, err = o.open(t, "damaged", unsentClaim{o.lineB, 0})
	requireRule(t, "a zero quantity", err, "return_request_lines_quantity_positive")
	_, err = o.open(t, "damaged", unsentClaim{o.lineB, 1}, unsentClaim{o.lineB, 1})
	requireRule(t, "a line named twice", err, "return_unsent_lines")
	_, err = o.open(t, "damaged", unsentClaim{uuid.New(), 1})
	requireRule(t, "a line of no order", err, "return_unsent_lines")
	_, err = o.open(t, "damaged")
	requireRule(t, "no lines", err, "return_unsent_lines")
	err = asAdminRole(t, nil, `SELECT open_unsent_refund($1, $2::uuid[], ARRAY[1], 'damaged', ' ', $3, 'blank-note')`,
		o.number, lineB, o.staff)
	requireRule(t, "a blank note", err, "return_unsent_note")
	err = asAdminRole(t, nil, `SELECT open_unsent_refund($1, $2::uuid[], ARRAY[1], 'damaged', 'x', $3, 'customer')`,
		o.number, lineB, o.user)
	requireRule(t, "a customer as the actor", err, "return_unsent_actor")
	err = asAdminRole(t, nil, `SELECT open_unsent_refund($1, $2::uuid[], ARRAY[1], 'damaged', 'x', $3, ' ')`,
		o.number, lineB, o.staff)
	requireRule(t, "a blank request id", err, "return_unsent_request")
	err = asAdminRole(t, nil, `SELECT open_unsent_refund('GO-NO-SUCH-ORDER', $1::uuid[], ARRAY[1], 'damaged', 'x', $2, 'none')`,
		lineB, o.staff)
	requireRule(t, "an order that does not exist", err, "return_unsent_eligible")

	// Nothing has shipped: the refund before shipment is that order's door.
	unshipped := newPaidTwoLineOrder(t, unsentOrderTotal, false)
	_, err = unshipped.open(t, "damaged", unsentClaim{unshipped.lineB, 1})
	requireRule(t, "an order that shipped nothing", err, "return_unsent_eligible")

	// The customer's undecided return is decided first.
	asked := newUnsentOrder(t, unsentOrderTotal)
	if _, askErr := schemaPool(t).Exec(t.Context(), `
		INSERT INTO return_requests (order_id, requested_by_user_id, reason) VALUES ($1, $2, '')`,
		asked.id, asked.user); askErr != nil {
		t.Fatalf("the customer asks for a return: %v", askErr)
	}
	_, err = asked.open(t, "damaged", unsentClaim{asked.lineB, 1})
	requireRule(t, "an order with an undecided return", err, "return_unsent_undecided_return")

	// One unsettled refund at a time: a second press resumes it.
	first := o.mustOpen(t, "damaged", unsentClaim{o.lineB, 1})
	if again := o.mustOpen(t, "lost", unsentClaim{o.lineB, 2}); again != first {
		t.Errorf("a second press opened %s beside the unsettled %s", again, first)
	}
	if n := unsentInt(t, `SELECT count(*)::int FROM return_requests WHERE order_id = $1`, o.id); n != 1 {
		t.Errorf("%d return requests after a repeated press, want 1", n)
	}
	if n := unsentInt(t, `SELECT count(*)::int FROM audit_events
	                      WHERE action = 'return.refund_unsent' AND entity_id = $1`, first); n != 1 {
		t.Errorf("%d audit rows after a repeated press, want 1", n)
	}

	// Settled, its unit counts against the next claim: one unit is left.
	o.pay(t, first, "succeeded")
	if settleErr := o.settle(t, first); settleErr != nil {
		t.Fatalf("settle the first claim: %v", settleErr)
	}
	_, err = o.open(t, "damaged", unsentClaim{o.lineB, 2})
	requireRule(t, "a second claim past what is owed", err, "return_within_shipment")
	o.mustOpen(t, "damaged", unsentClaim{o.lineB, 1})
}

// TestNoParcelCarriesAUnitRefundedUnsent: from approval, before any money has
// moved, the refunded unit is no longer the shop's to send.
func TestNoParcelCarriesAUnitRefundedUnsent(t *testing.T) {
	o := newUnsentOrder(t, unsentOrderTotal)
	o.mustOpen(t, "supplier_failed", unsentClaim{o.lineB, 1})

	requireRule(t, "a parcel carrying the refunded unit", o.shipB(t, 2), "shipment_within_purchase")
	if err := o.shipB(t, 1); err != nil {
		t.Fatalf("the unit still owed cannot ship: %v", err)
	}
}

// TestTheShippedUnitStaysReturnable: the unsent half of line b and the half
// that shipped are counted apart, and the money of the two never adds up to
// more than the units' share.
func TestTheShippedUnitStaysReturnable(t *testing.T) {
	o := newUnsentOrder(t, unsentOrderTotal)
	o.mustOpen(t, "damaged", unsentClaim{o.lineB, 2})

	customerReturn := func(quantity int) (uuid.UUID, error) {
		t.Helper()
		ctx := t.Context()
		var id uuid.UUID
		tx, err := schemaPool(t).Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE store`); err != nil {
			t.Fatalf("set role store: %v", err)
		}
		if err := tx.QueryRow(ctx, `INSERT INTO return_requests (order_id, requested_by_user_id, reason)
		                            VALUES ($1, $2, '') RETURNING id`, o.id, o.user).Scan(&id); err != nil {
			return id, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		                           VALUES ($1, $2, $3, $4)`, o.id, id, o.lineB, quantity); err != nil {
			return id, err
		}
		return id, tx.Commit(ctx)
	}

	returned, err := customerReturn(1)
	if err != nil {
		t.Fatalf("the customer cannot return the unit of b that arrived: %v", err)
	}
	if approveErr := asAdminRole(t, nil, `UPDATE return_requests SET status = 'approved', decided_at = now()
	                                      WHERE id = $1`, returned); approveErr != nil {
		t.Fatalf("approve the customer's return: %v", approveErr)
	}
	// Three units of b returned in all: ceil(150000 × 250000 / 270000 / 100) × 100
	// = 138900, less the 92600 the unsent refund froze.
	if goods := unsentInt(t, `SELECT goods_refund_cents::int FROM return_requests WHERE id = $1`,
		returned); goods != 46300 {
		t.Errorf("the returned unit refunds %d, want 46300", goods)
	}
	_, err = customerReturn(1)
	requireRule(t, "returning the shipped unit twice", err, "return_within_shipment")
}

// TestAnUnpaidUnsentRefundNeitherSettlesNorCompletes: completion waits for
// the money, and the refund closes only by settling, which takes its units
// out of the hold.
func TestAnUnpaidUnsentRefundNeitherSettlesNorCompletes(t *testing.T) {
	o := newUnsentOrder(t, 208000)
	returnID := o.mustOpen(t, "damaged", unsentClaim{o.lineB, 2})

	requireRule(t, "settling before any money moved", o.settle(t, returnID), "return_unsent_payout_outstanding")
	requireRule(t, "completing the order before any money moved", o.complete(t), "orders_finished_when_shipped")

	// Stripe accepted the card refund and has not settled it.
	o.pay(t, returnID, "pending")
	requireRule(t, "settling a pending card refund", o.settle(t, returnID), "return_unsent_payout_outstanding")
	requireRule(t, "completing the order on a pending refund", o.complete(t), "orders_finished_when_shipped")

	// The returns desk cannot close it in its own way either: nothing comes
	// back, and completing needs the money and then the hold settled.
	requireRule(t, "receiving a unit that never left",
		asAdminRole(t, nil, `UPDATE return_request_lines SET received_quantity = 1, restocked_quantity = 0
		                     WHERE return_request_id = $1`, returnID),
		"return_unsent_nothing_received")
	if err := asAdminRole(t, nil, `UPDATE return_request_lines SET received_quantity = 0, restocked_quantity = 0
	                               WHERE return_request_id = $1`, returnID); err != nil {
		t.Fatalf("inspect as nothing received: %v", err)
	}
	desk := func() error {
		return asAdminRole(t, nil, `UPDATE return_requests SET status = 'completed' WHERE id = $1`, returnID)
	}
	requireRule(t, "the desk closing an unpaid refund", desk(), "return_requests_completed_money_settled")

	o.pay(t, returnID, "succeeded")
	requireRule(t, "the desk closing a paid refund whose units are held", desk(), "return_unsent_completes_released")
	requireRule(t, "completing the order before settling", o.complete(t), "orders_finished_when_shipped")
	if held := o.heldB(t); held != 2 {
		t.Fatalf("order holds %d of b before settling, want 2", held)
	}

	if err := o.settle(t, returnID); err != nil {
		t.Fatalf("settle the paid refund: %v", err)
	}
	if err := o.complete(t); err != nil {
		t.Fatalf("complete the order once its unsent units are settled: %v", err)
	}
	if err := o.settle(t, uuid.New()); db.ConstraintName(err) != "return_unsent_settle_state" {
		t.Errorf("settling no refund: %v", err)
	}
	if err := asAdminRole(t, nil, `SELECT settle_unsent_refund($1, $2, 'customer')`,
		returnID, o.user); db.ConstraintName(err) != "return_unsent_actor" {
		t.Errorf("a customer settling: %v", err)
	}
	if err := asAdminRole(t, nil, `SELECT settle_unsent_refund($1, $2, '')`,
		returnID, o.staff); db.ConstraintName(err) != "return_unsent_request" {
		t.Errorf("settling with no request id: %v", err)
	}
}

// TestAWrittenOffUnitStaysOffSaleAndAWithdrawnOneComesBack: a unit that no
// longer exists stays out of stock and wakes no one waiting for it; one the
// customer withdrew from goes back on sale.
func TestAWrittenOffUnitStaysOffSaleAndAWithdrawnOneComesBack(t *testing.T) {
	for _, tt := range []struct {
		reason   string
		back     int
		state    string
		notified bool
	}{
		{"damaged", 0, "consumed", false},
		{"lost", 0, "consumed", false},
		{"supplier_failed", 0, "consumed", false},
		{"customer_withdrew", 2, "released", true},
	} {
		t.Run(tt.reason, func(t *testing.T) {
			o := newUnsentOrder(t, unsentOrderTotal)
			// Every unit of b is spoken for, so b reads sold out and a shopper waits.
			if _, err := schemaPool(t).Exec(t.Context(), `
				INSERT INTO stock_notifications (variant_id, email)
				VALUES ($1, 'waiting-' || gen_random_uuid() || '@example.com')`, o.variantB); err != nil {
				t.Fatalf("ask for a restock notice: %v", err)
			}
			stock := o.stockB(t)
			if stock != 0 {
				t.Fatalf("b has %d on sale before the refund, want 0", stock)
			}

			returnID := o.mustOpen(t, tt.reason, unsentClaim{o.lineB, 2})
			o.pay(t, returnID, "succeeded")
			if err := o.settle(t, returnID); err != nil {
				t.Fatalf("settle: %v", err)
			}

			if got := o.stockB(t); got != stock+tt.back {
				t.Errorf("b has %d on sale, want %d", got, stock+tt.back)
			}
			if held := o.heldB(t); held != 0 {
				t.Errorf("order still holds %d of b", held)
			}
			if n := unsentInt(t, `SELECT count(*)::int FROM inventory_reservations
			                      WHERE order_id = $1 AND variant_id = $2 AND quantity = 2 AND state = $3`,
				o.id, o.variantB, tt.state); n != 1 {
				t.Errorf("%d settled holds of the two units in state %s, want 1", n, tt.state)
			}
			notified := unsentInt(t, `SELECT count(*)::int FROM stock_notifications
			                          WHERE variant_id = $1 AND notified_at IS NOT NULL`, o.variantB) > 0
			if notified != tt.notified {
				t.Errorf("restock notice sent = %t, want %t", notified, tt.notified)
			}
		})
	}
}

// TestAParcelAndAnUnsentRefundCannotTakeTheSameUnit: a parcel's line and the
// door both lock the order before they count, so whichever commits second
// sees the first.
func TestAParcelAndAnUnsentRefundCannotTakeTheSameUnit(t *testing.T) {
	for _, parcelFirst := range []bool{true, false} {
		name := "refund first"
		if parcelFirst {
			name = "parcel first"
		}
		t.Run(name, func(t *testing.T) {
			o := newUnsentOrder(t, unsentOrderTotal)
			var parcel uuid.UUID
			if err := schemaPool(t).QueryRow(t.Context(), `
				INSERT INTO order_shipments (order_id, carrier, tracking_number)
				VALUES ($1, 'black_cat', 'UNSENT-RACE-' || $2) RETURNING id`,
				o.id, uuid.NewString()[:8]).Scan(&parcel); err != nil {
				t.Fatalf("record the parcel: %v", err)
			}
			ship := `SET LOCAL ROLE admin;
				INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
				VALUES ('` + o.id.String() + `', '` + parcel.String() + `', '` + o.lineB.String() + `', 2);`
			refund := `SET LOCAL ROLE admin;
				SELECT open_unsent_refund('` + o.number + `', ARRAY['` + o.lineB.String() + `']::uuid[],
				       ARRAY[1], 'lost', '` + unsentNote + `', '` + o.staff.String() + `', 'race');`

			first, second, loser := refund, ship, "shipment_within_purchase"
			if parcelFirst {
				first, second, loser = ship, refund, "return_within_shipment"
			}
			err1, err2 := raceOutcome(t, first, second)
			requireExactlyOne(t, "the two units of b still owed", err1, err2)
			if err1 != nil {
				t.Fatalf("the first writer failed: %v", err1)
			}
			requireRule(t, "the second writer", err2, loser)

			if n := unsentInt(t, `
				SELECT (coalesce((SELECT sum(quantity) FROM order_shipment_lines WHERE order_line_id = $1), 0)
				      + coalesce((SELECT sum(rl.quantity) FROM return_request_lines rl
				                  JOIN return_requests r ON r.id = rl.return_request_id
				                  WHERE rl.order_line_id = $1 AND r.unsent), 0))::int`, o.lineB); n > 3 {
				t.Errorf("line b ordered 3 and is shipped or refunded unsent %d times over", n)
			}
		})
	}
}

// TestOnlyTheDoorsWriteAnUnsentRefund: the back office writes no claim of its
// own, and no other role can press either door.
func TestOnlyTheDoorsWriteAnUnsentRefund(t *testing.T) {
	assertRoleStatementDenied(t, "admin", `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000',
		        '66660001-0000-4000-8000-000000000000', 1)`)
	assertRoleStatementDenied(t, "admin", `
		UPDATE return_requests SET unsent = true WHERE id = '88880001-0000-4000-8000-000000000000'`)
	for _, role := range []string{"store", "reporting", "maintenance"} {
		assertRoleStatementDenied(t, role, `
			SELECT open_unsent_refund('GO-260721-000387', ARRAY['66660001-0000-4000-8000-000000000000']::uuid[],
			       ARRAY[1], 'damaged', 'x', '55555555-5555-4555-8555-555555555555', 'denied')`)
		assertRoleStatementDenied(t, role, `
			SELECT settle_unsent_refund('88880001-0000-4000-8000-000000000000',
			       '55555555-5555-4555-8555-555555555555', 'denied')`)
	}
}

// TestSettlingRefusesAHoldThatNoLongerCoversItsUnits: a line whose hold some
// earlier writer settled has nothing left to release or write off, and
// settling says so instead of completing over it.
func TestSettlingRefusesAHoldThatNoLongerCoversItsUnits(t *testing.T) {
	o := newUnsentOrder(t, unsentOrderTotal)
	returnID := o.mustOpen(t, "customer_withdrew", unsentClaim{o.lineB, 2})
	o.pay(t, returnID, "succeeded")
	// consume_reservation_partial asks for no parcel: an admin-role caller can
	// settle a hold without one.
	if err := asAdminRole(t, nil, `SELECT consume_reservation_partial(r.id, 1) FROM inventory_reservations r
	                               WHERE r.order_id = $1 AND r.variant_id = $2 AND r.state = 'held'`,
		o.id, o.variantB); err != nil {
		t.Fatalf("consume part of the hold: %v", err)
	}
	requireRule(t, "settling over a hold of one", o.settle(t, returnID), "return_unsent_hold_short")
	if held := o.heldB(t); held != 1 {
		t.Errorf("a refused settle left %d held, want 1", held)
	}
}
