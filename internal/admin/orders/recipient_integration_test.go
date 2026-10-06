//go:build integration

package orders_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ordernotice"
	"github.com/koopa0/goen/internal/pgtx"
)

func TestTerminalRecipientNeverRecoversErasedAddress(t *testing.T) {
	_, id, _ := admintest.PendingOrderHoldingStock(t, pool)
	q := db.New(pool)
	if _, err := q.TerminalOrderRecipient(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE order_private_data SET email=NULL,recipient_name=NULL,phone=NULL,postal_code=NULL,city=NULL,district=NULL,street=NULL,erased_at=now() WHERE order_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := q.TerminalOrderRecipient(t.Context(), id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("erased recipient resolved: %v", err)
	}
}

func shippedParcel(t *testing.T) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	_, id := admintest.PaidPickingOrderForUser(t, pool, admintest.Customer(t, pool), 10000)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pgtx.Rollback(ctx, tx)
	var shipmentID uuid.UUID
	if _, err := tx.Exec(ctx, `UPDATE orders SET fulfillment_status = 'shipped' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'black_cat', 'RESCISSION-' || $1) RETURNING id`, id).Scan(&shipmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		SELECT order_id, $2, id, quantity FROM order_lines WHERE order_id = $1`, id, shipmentID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

// Before any delivery the rescission day is shop_today(), which a reader must
// not use: the notice and the order page say no day, and the row still scans.
func TestAnUndeliveredParcelHasNoRescissionDay(t *testing.T) {
	id := shippedParcel(t)
	to, ok, err := ordernotice.NewRecipients(pool).Of(t.Context(), id)
	if err != nil || !ok {
		t.Fatalf("Recipients.Of = %v, %t", err, ok)
	}
	if !to.RescissionEnds.IsZero() {
		t.Errorf("an undelivered order's notice carries the day %s", to.RescissionEnds)
	}
	rows, err := db.New(pool).OrderTracking(t.Context(), id)
	if err != nil || len(rows) != 1 {
		t.Fatalf("OrderTracking = %d rows, %v", len(rows), err)
	}
	if rows[0].DeliveredAt.Valid {
		t.Error("the parcel reads as delivered")
	}
}

func TestADeliveredParcelNamesTheShopDayPlusSeven(t *testing.T) {
	id := shippedParcel(t)
	var want time.Time
	if err := pool.QueryRow(t.Context(), `
		UPDATE order_shipments SET delivered_at = now() WHERE order_id = $1
		RETURNING return_window_ends(delivered_at)`, id).Scan(&want); err != nil {
		t.Fatal(err)
	}
	to, ok, err := ordernotice.NewRecipients(pool).Of(t.Context(), id)
	if err != nil || !ok {
		t.Fatalf("Recipients.Of = %v, %t", err, ok)
	}
	if !to.RescissionEnds.Equal(want) {
		t.Errorf("notice day = %s, want %s", to.RescissionEnds, want)
	}
	rows, err := db.New(pool).OrderTracking(t.Context(), id)
	if err != nil || len(rows) != 1 {
		t.Fatalf("OrderTracking = %d rows, %v", len(rows), err)
	}
	if !rows[0].DeliveredAt.Valid || !rows[0].RescissionEnds.Equal(want) {
		t.Errorf("tracking day = %s, want %s", rows[0].RescissionEnds, want)
	}
}

// An order with no parcel at all, as a payment-deadline cancellation is: the
// recipient reads, and carries no day for the cancellation mail to mention.
func TestAnOrderWithNoParcelHasNoRescissionDay(t *testing.T) {
	_, id, _ := admintest.PendingOrderHoldingStock(t, pool)
	to, ok, err := ordernotice.NewRecipients(pool).Of(t.Context(), id)
	if err != nil || !ok {
		t.Fatalf("Recipients.Of = %v, %t", err, ok)
	}
	if !to.RescissionEnds.IsZero() {
		t.Errorf("an order with no parcel carries the day %s", to.RescissionEnds)
	}
}
