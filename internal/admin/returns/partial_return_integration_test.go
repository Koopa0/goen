//go:build integration

package returns_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/returns"
	"github.com/koopa0/goen/internal/pgtx"
)

type orderLine struct {
	priceCents int64
	quantity   int32
}

type discountedOrder struct {
	lines         []orderLine
	discountCents int64
	deliveryCents int64
	creditCents   int64
}

// wholeDollars reports whether every price, the discount and the delivery fee
// are whole NT$, which is when every part of a return is refunded whole NT$ too.
func (o discountedOrder) wholeDollars() bool {
	for _, l := range o.lines {
		if l.priceCents%100 != 0 {
			return false
		}
	}
	return o.discountCents%100 == 0 && o.deliveryCents%100 == 0
}

type placedOrder struct {
	id      uuid.UUID
	number  string
	lineIDs []uuid.UUID
}

func TestAnOrderReturnedInPartsIsRefundedWhatItWouldBeAtOnce(t *testing.T) {
	tests := []struct {
		name  string
		order discountedOrder
		// parts holds, per return in approval order, the units of each order line.
		parts      [][]int32
		want       []int64
		wantCard   int64
		wantCredit int64
	}{
		{
			name:     "3 × NT$100, NT$100 off",
			order:    discountedOrder{lines: []orderLine{{priceCents: 10000, quantity: 3}}, discountCents: 10000},
			parts:    [][]int32{{1}, {1}, {1}},
			want:     []int64{6700, 6700, 6600},
			wantCard: 20000,
		},
		{
			name:     "7 × NT$99 on one line, 15% off rounded up to NT$104",
			order:    discountedOrder{lines: []orderLine{{priceCents: 9900, quantity: 7}}, discountCents: 10400},
			parts:    [][]int32{{2}, {2}, {3}},
			want:     []int64{16900, 16800, 25200},
			wantCard: 58900,
		},
		{
			name: "NT$125 + NT$125 + NT$499, NT$50 off",
			order: discountedOrder{lines: []orderLine{
				{priceCents: 12500, quantity: 1},
				{priceCents: 12500, quantity: 1},
				{priceCents: 49900, quantity: 1},
			}, discountCents: 5000},
			parts:    [][]int32{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}},
			want:     []int64{11700, 11700, 46500},
			wantCard: 69900,
		},
		{
			name: "3 × NT$333, NT$100 off, NT$80 delivery, NT$300 paid in store credit",
			order: discountedOrder{
				lines:         []orderLine{{priceCents: 33300, quantity: 3}},
				discountCents: 10000, deliveryCents: 8000, creditCents: 30000,
			},
			parts:      [][]int32{{1}, {1}, {1}},
			want:       []int64{30000, 30000, 37900},
			wantCard:   67900,
			wantCredit: 30000,
		},
		{
			// No rule keeps a price whole. On an order whose price is not, the
			// rounded-up share would have the completing part claim more than
			// was paid but for the cap.
			name:     "2 × 75 cents, nothing off",
			order:    discountedOrder{lines: []orderLine{{priceCents: 75, quantity: 2}}},
			parts:    [][]int32{{1}, {1}},
			want:     []int64{100, 50},
			wantCard: 150,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := admintest.StaffContext(t, pool)
			s := storeOver(pool, admintest.Refunder{})

			inParts := shippedDiscountedOrder(t, tt.order)
			var partsTotal int64
			for i, units := range tt.parts {
				offered, frozen := approvedReturn(t, ctx, s, inParts, units)
				if frozen != tt.want[i] {
					t.Errorf("part %d froze a refund of %d, want %d", i+1, frozen, tt.want[i])
				}
				if offered != frozen {
					t.Errorf("part %d offered %d while requested but froze %d on approval", i+1, offered, frozen)
				}
				if tt.order.wholeDollars() && frozen%100 != 0 {
					t.Errorf("part %d refund %d is not a whole NT$", i+1, frozen)
				}
				partsTotal += frozen
			}

			atOnce := shippedDiscountedOrder(t, tt.order)
			all := make([]int32, len(tt.order.lines))
			for i, l := range tt.order.lines {
				all[i] = l.quantity
			}
			offered, wholeRefund := approvedReturn(t, ctx, s, atOnce, all)
			if offered != wholeRefund {
				t.Errorf("the whole return offered %d while requested but froze %d", offered, wholeRefund)
			}
			if partsTotal != wholeRefund {
				t.Errorf("returned in parts the order refunds %d, at once %d; want them equal", partsTotal, wholeRefund)
			}

			// order_refunds is what the allowance floors to whole NT$, so the two
			// returns file the same allowance only if they paid back the same.
			partsCard, partsCredit := refundedOn(t, inParts)
			onceCard, onceCredit := refundedOn(t, atOnce)
			if partsCard != tt.wantCard || partsCredit != tt.wantCredit {
				t.Errorf("in parts the card got %d and store credit %d, want %d and %d",
					partsCard, partsCredit, tt.wantCard, tt.wantCredit)
			}
			if onceCard != tt.wantCard || onceCredit != tt.wantCredit {
				t.Errorf("at once the card got %d and store credit %d, want %d and %d",
					onceCard, onceCredit, tt.wantCard, tt.wantCredit)
			}
		})
	}
}

// shippedDiscountedOrder places o, pays it with the store credit first and the
// card for the rest, and ships every unit.
func shippedDiscountedOrder(t *testing.T, o discountedOrder) placedOrder {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer pgtx.Rollback(ctx, tx)

	var userID uuid.NullUUID
	if o.creditCents > 0 {
		if err := tx.QueryRow(ctx, `
			INSERT INTO users (email, role, full_name)
			VALUES ('parts-'||gen_random_uuid()||'@goen.invalid', 'customer', '分次退貨')
			RETURNING id`).Scan(&userID); err != nil {
			t.Fatalf("create user: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT post_store_credit($1, $2, '測試發放', NULL, $3, NULL)`,
			userID, o.creditCents, "grant:"+userID.UUID.String()); err != nil {
			t.Fatalf("grant credit: %v", err)
		}
	}

	var placed placedOrder
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents, discount_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, $2, $3
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID, o.deliveryCents, o.discountCents).
		Scan(&placed.id, &placed.number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	total := o.deliveryCents - o.discountCents
	for i, l := range o.lines {
		var lineID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, 'PARTS-SKU', '分次退貨商品', $2, $3, $4) RETURNING id`,
			placed.id, l.priceCents, l.quantity, i).Scan(&lineID); err != nil {
			t.Fatalf("create line %d: %v", i, err)
		}
		placed.lineIDs = append(placed.lineIDs, lineID)
		total += l.priceCents * int64(l.quantity)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'parts@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		placed.id); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if o.creditCents > 0 {
		if _, err := tx.Exec(ctx, `SELECT post_store_credit($1, $2, '訂單折抵', $3, $4, NULL)`,
			userID, -o.creditCents, placed.id, "spend:"+placed.id.String()); err != nil {
			t.Fatalf("spend credit: %v", err)
		}
	}
	session := "cs_parts_" + placed.number
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`,
		placed.id, session, total-o.creditCents); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`,
		session, total-o.creditCents); err != nil {
		t.Fatalf("capture: %v", err)
	}

	admintest.MoveOrderToShipped(t, tx, placed.id)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'black_cat', 'PARTS-'||$2) RETURNING id`,
		placed.id, placed.number).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	for i, l := range o.lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
			VALUES ($1, $2, $3, $4)`, placed.id, shipmentID, placed.lineIDs[i], l.quantity); err != nil {
			t.Fatalf("ship line %d: %v", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return placed
}

// approvedReturn opens a return of units (one count per order line), reads the
// amount the request offers, approves it and reads the refund it froze.
func approvedReturn(
	t *testing.T, ctx context.Context, s *returns.Store, o placedOrder, units []int32,
) (offered, frozen int64) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer pgtx.Rollback(ctx, tx)

	var requestID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason) VALUES ($1, '分次退貨') RETURNING id`,
		o.id).Scan(&requestID); err != nil {
		t.Fatalf("open return: %v", err)
	}
	for i, n := range units {
		if n == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
			VALUES ($1, $2, $3, $4)`, o.id, requestID, o.lineIDs[i], n); err != nil {
			t.Fatalf("return line %d: %v", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit return: %v", err)
	}

	if err := pool.QueryRow(ctx,
		`SELECT return_refundable_amount($1)`, requestID).Scan(&offered); err != nil {
		t.Fatalf("read the requested amount: %v", err)
	}
	if err := s.Decide(ctx, requestID.String(), "approved", "已收到退貨", "", uuid.NullUUID{}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT goods_refund_cents + shipping_refund_cents FROM return_requests WHERE id = $1`,
		requestID).Scan(&frozen); err != nil {
		t.Fatalf("read the frozen refund: %v", err)
	}
	return offered, frozen
}

func refundedOn(t *testing.T, o placedOrder) (card, credit int64) {
	t.Helper()
	if err := pool.QueryRow(t.Context(), `
		SELECT card_cents, credit_cents FROM order_refunds WHERE order_id = $1`,
		o.id).Scan(&card, &credit); err != nil {
		t.Fatalf("read refunds of %s: %v", o.number, err)
	}
	return card, credit
}
