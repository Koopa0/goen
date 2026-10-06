package cart

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestCutParcelsCountsEachParcelFromItsOwnDelivery(t *testing.T) {
	t.Parallel()
	phone, buds, stand := uuid.New(), uuid.New(), uuid.New()
	first, second := uuid.New(), uuid.New()
	delivered := time.Date(2026, 10, 6, 7, 20, 0, 0, time.UTC)
	lastDay := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	goodwill := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	today := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)

	byID := map[uuid.UUID]pages.OrderLine{
		phone: {Name: "phone", Quantity: 1, WarrantyMonths: 24},
		buds:  {Name: "buds", Quantity: 3, WarrantyMonths: 12},
		stand: {Name: "case", Quantity: 2},
	}
	shipments := []db.OrderTrackingRow{
		{ID: first, Carrier: "black_cat", TrackingNumber: "A", DeliveredAt: pgtype.Timestamptz{Time: delivered, Valid: true}, RescissionEnds: lastDay, GoodwillEnds: goodwill},
		// The database reads shop_today() for a parcel that has not arrived.
		{ID: second, Carrier: "black_cat", TrackingNumber: "B", RescissionEnds: today, GoodwillEnds: today},
	}
	parcelLines := []db.OrderParcelLinesRow{
		{ShipmentID: first, OrderLineID: phone, Quantity: 1},
		{ShipmentID: first, OrderLineID: buds, Quantity: 2},
		{ShipmentID: second, OrderLineID: buds, Quantity: 1},
	}
	// Unit 2 of the buds went in the first parcel, unit 3 in the second.
	registrations := []db.OrderWarrantyRegistrationsRow{
		{OrderLineID: buds, UnitNo: 2, ExpiresOn: time.Date(2027, 10, 6, 0, 0, 0, 0, time.UTC)},
		{OrderLineID: buds, UnitNo: 3, ExpiresOn: time.Date(2027, 10, 12, 0, 0, 0, 0, time.UTC)},
	}

	parcels, unshipped := cutParcels(shipments, parcelLines, registrations, []uuid.UUID{phone, buds, stand}, byID)

	if len(parcels) != 2 {
		t.Fatalf("cutParcels returned %d parcels, want 2", len(parcels))
	}
	a, b := parcels[0], parcels[1]
	if !a.Delivered() || !a.RescissionEnds.Equal(lastDay) || !a.GoodwillEnds.Equal(goodwill) {
		t.Errorf("first parcel = delivered %v, last day %v, goodwill %v; want its own dates", a.DeliveredAt, a.RescissionEnds, a.GoodwillEnds)
	}
	if b.Delivered() || !b.RescissionEnds.IsZero() || !b.GoodwillEnds.IsZero() {
		t.Errorf("an undelivered parcel carries days %v and %v, want none", b.RescissionEnds, b.GoodwillEnds)
	}
	if len(a.Lines) != 2 || a.Lines[1].Quantity != 2 || a.Lines[1].Registered != 1 || a.Lines[1].WarrantyUntil.Day() != 6 {
		t.Errorf("first parcel's buds = %+v, want 2 units with unit 2 registered until the 6th", a.Lines)
	}
	if len(b.Lines) != 1 || b.Lines[0].Quantity != 1 || b.Lines[0].Registered != 1 || b.Lines[0].WarrantyUntil.Day() != 12 {
		t.Errorf("second parcel's buds = %+v, want 1 unit registered until the 12th", b.Lines)
	}
	if len(unshipped) != 1 || unshipped[0].Name != "case" || unshipped[0].Quantity != 2 {
		t.Errorf("unshipped = %+v, want the two cases", unshipped)
	}
}
