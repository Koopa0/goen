//go:build integration

package account_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/user"
)

// TestAccountOrderRowNamesALastDayOnlyWhenOneHolds holds that the history states a last day to cancel only when
// every unit is in a parcel, every parcel has arrived and all share one day.
func TestAccountOrderRowNamesALastDayOnlyWhenOneHolds(t *testing.T) {
	s := account.NewStore(pool)
	owner := register(t, s, "lastday-"+uuid.NewString()+"@example.invalid")
	ctx := user.NewContext(i18n.WithLocale(t.Context(), i18n.En), owner)

	// Each parcel is (units, days ago it arrived; nil for in transit). The order has two units of one line.
	type parcel struct {
		units     int
		deliverAt *string
	}
	days := func(s string) *string { return &s }
	for _, tc := range []struct {
		name    string
		parcels []parcel
		want    bool
	}{
		{"in transit", []parcel{{2, nil}}, false},
		{"one unit still unshipped", []parcel{{1, days("3 days")}}, false},
		{"two parcels on different days", []parcel{{1, days("3 days")}, {1, days("1 day")}}, false},
		{"every parcel on one day", []parcel{{1, days("3 days")}, {1, days("3 days")}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var orderID uuid.UUID
			if err := pool.QueryRow(ctx, `
				WITH o AS (
				  INSERT INTO orders (user_id, shipping_version_id, shipping_method_code, shipping_method_name, placed_at)
				  SELECT $1::uuid, v.id, sm.code, v.name, now() - interval '10 days'
				  FROM (SELECT id, method_id, name FROM shipping_method_versions ORDER BY effective_at LIMIT 1) v
				  JOIN shipping_methods sm ON sm.id = v.method_id RETURNING id
				), l AS (
				  INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
				  SELECT id, 'LASTDAY', 'Last day', 100, 2 FROM o
				), p AS (
				  INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
				  SELECT id, 'lastday@example.invalid', 'Recipient', '0912345678', '110', 'Taipei', 'District', 'Street' FROM o
				)
				SELECT id FROM o`, owner.ID).Scan(&orderID); err != nil {
				t.Fatalf("insert order: %v", err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer pgtx.Rollback(ctx, tx)
			// The order is still pending, which no shipment may be recorded against.
			if _, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
				t.Fatalf("relax triggers: %v", err)
			}
			for i, p := range tc.parcels {
				var shipmentID uuid.UUID
				if err = tx.QueryRow(ctx, `
					INSERT INTO order_shipments (order_id, carrier, tracking_number, shipped_at, delivered_at)
					VALUES ($1, 'black_cat', $2, now() - interval '5 days',
					        CASE WHEN $3::text IS NULL THEN NULL ELSE now() - $3::interval END) RETURNING id`,
					orderID, uuid.NewString()+string(rune('a'+i)), p.deliverAt).Scan(&shipmentID); err != nil {
					t.Fatalf("insert shipment: %v", err)
				}
				if _, err = tx.Exec(ctx, `
					INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
					SELECT order_id, $2, id, $3 FROM order_lines WHERE order_id = $1`, orderID, shipmentID, p.units); err != nil {
					t.Fatalf("insert shipment line: %v", err)
				}
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatalf("commit: %v", err)
			}

			v, err := s.Overview(ctx, owner)
			if err != nil {
				t.Fatalf("overview: %v", err)
			}
			row := v.Orders[0]
			if row.OneLastDay != tc.want {
				t.Fatalf("Overview(%s).Orders[0].OneLastDay = %v, want %v", tc.name, row.OneLastDay, tc.want)
			}
			if !tc.want {
				return
			}
			var lastDay time.Time
			if err = pool.QueryRow(ctx, `SELECT return_window_ends(delivered_at) FROM order_shipments WHERE order_id = $1 LIMIT 1`,
				orderID).Scan(&lastDay); err != nil {
				t.Fatalf("read the last day: %v", err)
			}
			if want := shoptime.DateOf(lastDay, time.Now()); row.LastDay != want {
				t.Errorf("Overview(%s).Orders[0].LastDay = %+v, want the database's %+v", tc.name, row.LastDay, want)
			}
		})
	}
}

// TestAccountOrderRowSaysRefundedOnceEveryUnitIsInAnApprovedReturn holds that the history reads the same fact the
// order page does: the refund is paid at approval, so a part of the order in a return leaves it as it was.
func TestAccountOrderRowSaysRefundedOnceEveryUnitIsInAnApprovedReturn(t *testing.T) {
	s := account.NewStore(pool)
	owner := register(t, s, "returned-"+uuid.NewString()+"@example.invalid")
	ctx := user.NewContext(i18n.WithLocale(t.Context(), i18n.En), owner)

	for _, tc := range []struct {
		name     string
		returned int
		want     bool
	}{
		{"one unit of two", 1, false},
		{"both units", 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer pgtx.Rollback(ctx, tx)
			if _, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
				t.Fatalf("relax triggers: %v", err)
			}
			var orderID uuid.UUID
			if err = tx.QueryRow(ctx, `
				INSERT INTO orders (user_id, shipping_version_id, shipping_method_code, shipping_method_name, placed_at,
				                    fulfillment_status)
				SELECT $1::uuid, v.id, sm.code, v.name, now(), 'delivered'
				FROM (SELECT id, method_id, name FROM shipping_method_versions ORDER BY effective_at LIMIT 1) v
				JOIN shipping_methods sm ON sm.id = v.method_id RETURNING id`, owner.ID).Scan(&orderID); err != nil {
				t.Fatalf("insert order: %v", err)
			}
			var lineID, returnID uuid.UUID
			if err = tx.QueryRow(ctx, `
				INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
				VALUES ($1, 'RETURNED', 'Returned', 100, 2) RETURNING id`, orderID).Scan(&lineID); err != nil {
				t.Fatalf("insert line: %v", err)
			}
			if err = tx.QueryRow(ctx, `
				INSERT INTO return_requests (order_id, status, reason, decided_at, goods_refund_cents, shipping_refund_cents,
				                             card_refund_cents, credit_refund_cents)
				VALUES ($1, 'approved', 'test', now(), $2, 0, $2, 0) RETURNING id`, orderID, 100*tc.returned).Scan(&returnID); err != nil {
				t.Fatalf("insert return: %v", err)
			}
			if _, err = tx.Exec(ctx, `
				INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
				VALUES ($1, $2, $3, $4)`, orderID, returnID, lineID, tc.returned); err != nil {
				t.Fatalf("insert return line: %v", err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatalf("commit: %v", err)
			}

			v, err := s.Overview(ctx, owner)
			if err != nil {
				t.Fatalf("overview: %v", err)
			}
			if got := v.Orders[0].Returned; got != tc.want {
				t.Errorf("Overview(%s).Orders[0].Returned = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
