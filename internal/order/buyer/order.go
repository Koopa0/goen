package buyer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	invoicepkg "github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shipping"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
)

func (s *Store) Order(ctx context.Context, number string) (pages.OrderView, error) {
	o, err := s.q.OrderSummaryByNumber(ctx, db.OrderSummaryByNumberParams{
		Number: number, Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pages.OrderView{}, ErrNotFound
		}
		return pages.OrderView{}, fmt.Errorf("read order: %w", err)
	}
	lines, err := s.q.OrderPageLines(ctx, db.OrderPageLinesParams{OrderID: o.ID, Locale: i18n.FromContext(ctx).Tag()})
	if err != nil {
		return pages.OrderView{}, fmt.Errorf("read order lines: %w", err)
	}

	hold, err := s.q.OrderHoldSpan(ctx, o.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return pages.OrderView{}, fmt.Errorf("read order stock hold: %w", err)
	}

	now := time.Now()
	view := pages.OrderView{
		Number:       o.OrderNumber,
		Status:       order.FulfillmentStatus(o.FulfillmentStatus),
		Email:        o.Email,
		ShippingName: o.ShippingMethodName,
		Committed:    o.Committed,
		OwedCents:    o.OwedCents,
		DeliveryTo: pages.Delivery{
			PostalCode: o.PostalCode, City: o.City, District: o.District, Street: o.Street,
			PickupChain: shipping.Chain(o.PickupChain), PickupStoreCode: o.PickupStoreCode,
			PickupStoreName: o.PickupStoreName,
		}.Line(),
		SubtotalCents: o.SubtotalCents,
		ShippingCents: o.ShippingCents,
		DiscountCents: o.DiscountCents, DiscountReason: o.DiscountReason,
		TotalCents:  o.TotalCents,
		CreditCents: o.CreditCents,
		PlacedAt:    o.PlacedAt,
		Now:         now,
		HoldUntil:   hold.HeldUntil,
		Pickup:      o.PickupChain != "",
	}
	ids := make([]uuid.UUID, 0, len(lines))
	byID := make(map[uuid.UUID]pages.OrderLine, len(lines))
	for i := range lines {
		l := &lines[i]
		photo := assets.Image{Digest: l.ImageDigest, BundledKey: l.ImageBundledKey, Width: int(l.ImageWidth)}
		line := pages.OrderLine{
			SKU: l.SKU, Name: l.ProductName, Label: l.VariantLabel.String,
			UnitCents: l.UnitPriceCents, Quantity: l.Quantity,
			ImageURL:       photo.URL(),
			ImageSrcset:    photo.Srcset(),
			ImageAlt:       l.ImageAlt,
			WarrantyMonths: int(l.WarrantyMonths.Int32),
		}
		view.Lines = append(view.Lines, line)
		ids = append(ids, l.ID)
		byID[l.ID] = line
	}

	events, err := s.q.OrderTimeline(ctx, o.ID)
	if err != nil {
		return pages.OrderView{}, fmt.Errorf("read order timeline: %w", err)
	}
	for _, e := range events {
		view.Timeline = append(view.Timeline, pages.OrderEvent{
			Kind: order.EventKind(e.Kind), Note: e.Note,
			At: e.OccurredAt, Unsent: e.Unsent,
		})
	}

	if view.Invoice, err = s.orderInvoice(ctx, o.ID); err != nil {
		return pages.OrderView{}, err
	}
	if view.Shipments, view.Unshipped, err = s.orderParcels(ctx, o.ID, ids, byID); err != nil {
		return pages.OrderView{}, err
	}
	if view.Returned, err = s.orderReturned(ctx, o.ID); err != nil {
		return pages.OrderView{}, err
	}
	return view, nil
}

func (s *Store) orderParcels(
	ctx context.Context, orderID uuid.UUID, ids []uuid.UUID, byID map[uuid.UUID]pages.OrderLine,
) (parcels []pages.OrderShipment, unshipped []pages.OrderLine, err error) {
	shipments, err := s.q.OrderTracking(ctx, orderID)
	if err != nil {
		return nil, nil, fmt.Errorf("read order tracking: %w", err)
	}
	if len(shipments) == 0 {
		return nil, nil, nil
	}
	parcelLines, err := s.q.OrderParcelLines(ctx, orderID)
	if err != nil {
		return nil, nil, fmt.Errorf("read order parcels: %w", err)
	}
	registrations, err := s.q.OrderWarrantyRegistrations(ctx, orderID)
	if err != nil {
		return nil, nil, fmt.Errorf("read order warranties: %w", err)
	}
	unsent, err := s.q.OrderUnsentUnits(ctx, orderID)
	if err != nil {
		return nil, nil, fmt.Errorf("read order units refunded unsent: %w", err)
	}
	refunded := make(map[uuid.UUID]int32, len(unsent))
	for _, u := range unsent {
		refunded[u.OrderLineID] = u.Quantity
	}
	parcels, unshipped = cutParcels(shipments, parcelLines, registrations, refunded, ids, byID)
	return parcels, unshipped, nil
}

// cutParcels hands each parcel the units of the lines that went in it, and returns what no parcel carries yet and
// no unsent refund took back (refunded, by line). A line's units are numbered across its parcels in shipment order,
// as RegisterWarranty numbers them, so a registration belongs to the parcel its unit went in. A parcel not yet
// delivered carries no day: the database reads shop_today() there.
func cutParcels(
	shipments []db.OrderTrackingRow, parcelLines []db.OrderParcelLinesRow, registrations []db.OrderWarrantyRegistrationsRow,
	refunded map[uuid.UUID]int32, ids []uuid.UUID, byID map[uuid.UUID]pages.OrderLine,
) (parcels []pages.OrderShipment, unshipped []pages.OrderLine) {
	registered := make(map[uuid.UUID][]db.OrderWarrantyRegistrationsRow)
	for _, r := range registrations {
		registered[r.OrderLineID] = append(registered[r.OrderLineID], r)
	}
	shipped := make(map[uuid.UUID]int32, len(byID))
	for i := range shipments {
		sh := &shipments[i]
		parcel := pages.OrderShipment{
			Carrier: shipping.Carrier(sh.Carrier), Tracking: sh.TrackingNumber, ShippedAt: sh.ShippedAt,
			RescissionDays: sh.RescissionDays,
		}
		if sh.DeliveredAt.Valid {
			parcel.DeliveredAt, parcel.RescissionEnds, parcel.GoodwillEnds = sh.DeliveredAt.Time, sh.RescissionEnds, sh.GoodwillEnds
		}
		for _, pl := range parcelLines {
			if pl.ShipmentID != sh.ID {
				continue
			}
			share := byID[pl.OrderLineID]
			share.Quantity = pl.Quantity
			before := shipped[pl.OrderLineID]
			for _, r := range registered[pl.OrderLineID] {
				if int32(r.UnitNo) > before && int32(r.UnitNo) <= before+pl.Quantity {
					share.Registered++
					share.WarrantyUntil = r.ExpiresOn
				}
			}
			shipped[pl.OrderLineID] = before + pl.Quantity
			parcel.Lines = append(parcel.Lines, share)
		}
		parcels = append(parcels, parcel)
	}
	for _, id := range ids {
		if left := byID[id].Quantity - shipped[id] - refunded[id]; left > 0 {
			rest := byID[id]
			rest.Quantity = left
			unshipped = append(unshipped, rest)
		}
	}
	return parcels, unshipped
}

// orderReturned is nil until every unit the order sold is in a return whose refund has settled.
func (s *Store) orderReturned(ctx context.Context, orderID uuid.UUID) (*pages.OrderReturned, error) {
	returned, err := s.q.ReturnedOrders(ctx, []uuid.UUID{orderID})
	if err != nil {
		return nil, fmt.Errorf("read returned order: %w", err)
	}
	if len(returned) == 0 {
		return nil, nil //nolint:nilnil // an order not returned in full is not an error
	}
	returns, err := s.q.OrderReturns(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("read order returns: %w", err)
	}
	out := &pages.OrderReturned{}
	for _, r := range returns {
		out.At = r.RefundedAt
		out.RefundCents += r.RefundCents
	}
	return out, nil
}

func (s *Store) orderInvoice(ctx context.Context, orderID uuid.UUID) (*pages.OrderInvoice, error) {
	docs, err := s.q.OrderInvoiceDocuments(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("read order invoice: %w", err)
	}
	if len(docs) == 0 {
		return nil, nil //nolint:nilnil // no invoice filed yet is not an error
	}
	pref, err := s.q.OrderInvoicePreference(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("read order invoice preference: %w", err)
	}
	out := &pages.OrderInvoice{
		Type: invoicepkg.Preference(pref.InvoiceType), MobileBarcode: pref.CarrierCode,
		DonationCode: pref.DonationCode, TaxID: pref.TaxID,
	}
	for i := range docs {
		d := &docs[i]
		out.Documents = append(out.Documents, pages.OrderInvoiceDocument{
			Allowance: invoicepkg.DocumentKind(d.Kind) == invoicepkg.DocumentAllowance, Number: d.Number, RandomCode: d.ProviderRef,
			AmountCents: d.AmountCents, Voided: invoicepkg.DocumentStatus(d.Status) == invoicepkg.DocumentVoided,
			IssuedOn: shoptime.Day(d.IssuedAt),
		})
	}
	return out, nil
}
