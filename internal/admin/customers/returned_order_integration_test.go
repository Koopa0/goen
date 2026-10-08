//go:build integration

package customers_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/customers"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/warranty"
)

func TestCustomerOrderAndWarrantyStatusUseApprovedWholeOrderReturns(t *testing.T) {
	for _, tc := range []struct {
		name         string
		quantity     int32
		status       string
		wantReturned bool
	}{
		{name: "no return"},
		{name: "requested whole order", quantity: 2, status: "requested"},
		{name: "approved partial order", quantity: 1, status: "approved"},
		{name: "approved whole order", quantity: 2, status: "approved", wantReturned: true},
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
			if err := warranty.NewStore(pool).Register(t.Context(), lineID.String(), userID.String(), serial+"-2", 2); err != nil {
				t.Fatal(err)
			}
			if tc.quantity > 0 {
				var requestID uuid.UUID
				if err := pool.QueryRow(t.Context(), `INSERT INTO return_requests(order_id,reason) VALUES($1,'') RETURNING id`, orderID).Scan(&requestID); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), `INSERT INTO return_request_lines(order_id,return_request_id,order_line_id,quantity) VALUES($1,$2,$3,$4)`, orderID, requestID, lineID, tc.quantity); err != nil {
					t.Fatal(err)
				}
				if tc.status == "approved" {
					if _, err := pool.Exec(t.Context(), `UPDATE return_requests SET status='approved',decided_at=now(),goods_refund_cents=0,card_refund_cents=0,credit_refund_cents=0 WHERE id=$1`, requestID); err != nil {
						t.Fatal(err)
					}
				}
				if tc.status == "rejected" {
					if _, err := pool.Exec(t.Context(), `UPDATE return_requests SET status='rejected',decided_at=now() WHERE id=$1`, requestID); err != nil {
						t.Fatal(err)
					}
				}
			}
			staff, _ := admintest.StaffContext(t, pool)
			cfg := admintest.AdminRolePool(t, pool).Config().Copy()
			trace := &customerReturnReads{}
			cfg.ConnConfig.Tracer = trace
			reader, err := pgxpool.NewWithConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(reader.Close)
			store := customers.NewStore(reader)
			for _, locale := range i18n.Locales() {
				ctx := i18n.WithLocale(staff, locale)
				trace.identities.Store(0)
				trace.returns.Store(0)
				profile, err := store.Profile(ctx, userID.String())
				if err != nil {
					t.Fatal(err)
				}
				if trace.identities.Load() != 1 || trace.returns.Load() != 1 {
					t.Errorf("profile return reads=%d identities/%d batches, want 1/1", trace.identities.Load(), trace.returns.Load())
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
					trace.identities.Store(0)
					trace.returns.Store(0)
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
					if trace.identities.Load() != 1 || trace.returns.Load() != 1 {
						t.Errorf("warranty return reads=%d identities/%d batches, want 1/1", trace.identities.Load(), trace.returns.Load())
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

type customerReturnReads struct {
	identities atomic.Int32
	returns    atomic.Int32
}

func (tr *customerReturnReads) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "-- name: OrderIDByNumber ") {
		tr.identities.Add(1)
	}
	if strings.HasPrefix(data.SQL, "-- name: ReturnedOrders ") {
		tr.returns.Add(1)
	}
	return ctx
}

func (*customerReturnReads) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
