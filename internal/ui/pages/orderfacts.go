package pages

import (
	"context"
	"fmt"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/components"
)

func (v *OrderView) now() time.Time {
	if v.Now.IsZero() {
		return time.Now()
	}
	return v.Now
}

func (v *OrderView) IsCancelled() bool { return v.Status == order.FulfillmentCancelled }

func (v *OrderView) cancelledAt() time.Time {
	for _, e := range v.Timeline {
		if e.Kind == order.EventCancelled {
			return e.At
		}
	}
	return time.Time{}
}

// SimpleLines is an order whose lines are listed as bought: nothing is counting from a delivery.
func (v *OrderView) SimpleLines() bool {
	return v.IsCancelled() || v.Returned != nil || len(v.Shipments) == 0
}

// SeveralParcels is true for an order cut into several parcels, each with its own delivery.
func (v *OrderView) SeveralParcels() bool { return len(v.Shipments) > 1 }

// Facts are what the page opens with. A cancelled order has no delivery, so it states the cancellation instead.
func (v *OrderView) Facts(ctx context.Context) []components.Stat {
	now := v.now()
	placed := dateStat(ctx, i18n.T(ctx, i18n.KeyOrderFactPlaced), shoptime.DateOf(v.PlacedAt, now), shoptime.ClockText(v.PlacedAt))
	if v.IsCancelled() {
		facts := []components.Stat{placed, timeStat(ctx, i18n.T(ctx, i18n.KeyOrderFactCancelled), v.cancelledAt(), now)}
		if v.PaymentState() == PaymentRefunded {
			facts = append(facts, components.Stat{Label: i18n.T(ctx, i18n.KeyOrderFactRefund), Value: components.StatMoney(v.TotalCents())})
		}
		return facts
	}
	facts := []components.Stat{placed, {Label: i18n.T(ctx, i18n.KeyOrderGrandTotal), Value: components.StatMoney(v.TotalCents())}}
	if len(v.Shipments) == 1 && v.Shipments[0].Delivered() {
		s := v.Shipments[0]
		label := i18n.T(ctx, i18n.KeyOrderFactDelivered)
		if v.Pickup {
			label = i18n.T(ctx, i18n.KeyOrderFactCollected)
		}
		facts = append(facts, timeStat(ctx, label, s.DeliveredAt, now, i18n.CarrierName(ctx, s.Carrier)))
	}
	return facts
}

// ReturnedFacts are what an order says once every unit has come back.
func (v *OrderView) ReturnedFacts(ctx context.Context) []components.Stat {
	now := v.now()
	return []components.Stat{
		dateStat(ctx, i18n.T(ctx, i18n.KeyOrderFactReturned), shoptime.DateOf(v.Returned.At, now), ""),
		{Label: i18n.T(ctx, i18n.KeyOrderFactRefund), Value: components.StatMoney(v.Returned.RefundCents)},
	}
}

// ParcelTitle names a parcel only where there is more than one to tell apart.
func (v *OrderView) ParcelTitle(ctx context.Context, i int) string {
	if !v.SeveralParcels() {
		return i18n.T(ctx, i18n.KeyOrderRescissionTitle)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyOrderParcelOf), i+1, len(v.Shipments))
}

// ParcelsNote tells a reader that the days are counted for each parcel on its own.
func (v *OrderView) ParcelsNote(ctx context.Context) string {
	if !v.SeveralParcels() {
		return ""
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyOrderParcelsNote), len(v.Shipments))
}

// ReturnFacts are the right to cancel as the database counted it for this parcel.
func (v *OrderView) ReturnFacts(ctx context.Context, s OrderShipment) []components.Stat {
	now := v.now()
	last, end := shoptime.DateOf(s.RescissionEnds, now), shoptime.DateOf(s.GoodwillEnds, now)
	// On the last day the grid says so; a count of zero would read as the right already gone.
	left := int64(shoptime.DaysBetween(shoptime.DateOf(now, now), last))
	if left == 0 {
		left = -1
	}
	return []components.Stat{
		dateStat(ctx, i18n.T(ctx, i18n.KeyOrderLastDay), last, ""),
		{Label: i18n.T(ctx, i18n.KeyOrderDaysLeft), Value: components.StatCount(left, i18n.T(ctx, i18n.KeyFactUnitDays))},
		dateStat(ctx, i18n.T(ctx, i18n.KeyOrderUnusedUntil), end, ""),
	}
}

// ReturnGrid draws the days from the parcel's delivery to the end of unused returns.
func (v *OrderView) ReturnGrid(ctx context.Context, s OrderShipment) (components.PeriodSpec, bool) {
	now := v.now()
	return components.ReturnPeriod(ctx,
		shoptime.DateOf(s.DeliveredAt, now), shoptime.DateOf(s.RescissionEnds, now), shoptime.DateOf(s.GoodwillEnds, now),
		shoptime.DateOf(now, now), v.Pickup)
}

// ReturnPending is what a parcel that has not arrived says in place of dates.
func (v *OrderView) ReturnPending(ctx context.Context) string {
	if v.Pickup {
		return i18n.Count(ctx, i18n.KeyOrderRescissionAwaitsPickup, RescissionDays, RescissionDays)
	}
	return i18n.Count(ctx, i18n.KeyOrderRescissionAwaits, RescissionDays, RescissionDays)
}

// PickupCounted explains where the days start, for an order collected from a store.
func (v *OrderView) PickupCounted(ctx context.Context) string {
	if !v.Pickup {
		return ""
	}
	return i18n.T(ctx, i18n.KeyOrderPickupCounted)
}

// WarrantyFacts are a line's warranty counted from the parcel that carries it; delivered says whether that
// parcel has arrived. Before registration only the months are stated, and the cover's end once a unit is registered.
func (v *OrderView) WarrantyFacts(ctx context.Context, l OrderLine, delivered bool) []components.Stat {
	counted, later := i18n.KeyOrderWarrantyFromDelivery, i18n.KeyOrderWarrantyAfterDelivery
	if v.Pickup {
		counted, later = i18n.KeyOrderWarrantyFromCollection, i18n.KeyOrderWarrantyAfterCollection
	}
	note := i18n.T(ctx, counted)
	switch {
	case l.Registered > 0:
	case !delivered:
		note = fmt.Sprintf(i18n.T(ctx, i18n.KeyOrderWarrantyNote), note, i18n.T(ctx, later))
	case !v.ShowWarrantyLink:
		note = fmt.Sprintf(i18n.T(ctx, i18n.KeyOrderWarrantyNote), note, i18n.T(ctx, i18n.KeyOrderWarrantyNeedsAccount))
	}
	facts := []components.Stat{{
		Label: i18n.T(ctx, i18n.KeyOrderWarranty),
		Value: components.StatCount(int64(l.WarrantyMonths), countUnit(ctx, i18n.KeyUnitMonths, int64(l.WarrantyMonths))),
		Note:  note,
	}}
	if l.Registered > 0 {
		facts = append(facts, dateStat(ctx, i18n.T(ctx, i18n.KeyOrderWarrantyUntil), shoptime.DateOf(l.WarrantyUntil, v.now()), ""))
	}
	return facts
}

// CanRegister is offered from delivery on, to the account that owns the order, while a unit of the share is unregistered.
func (v *OrderView) CanRegister(l OrderLine, delivered bool) bool {
	return v.ShowWarrantyLink && delivered && l.Registered < int(l.Quantity)
}

// Stamp is the short date and time of an event, with the form a time element reads.
func (e OrderEvent) Stamp(ctx context.Context) string { return shoptime.StampText(ctx, e.At) }

func (e OrderEvent) Datetime() string { return shoptime.ISOStamp(e.At) }

// dateStat is a day as a stat, with an optional note under it.
func dateStat(ctx context.Context, label string, d shoptime.Date, note string) components.Stat {
	return components.Stat{
		Label: label,
		Value: components.StatDate(shoptime.DateText(ctx, d), "").WithDatetime(d.ISO()),
		Note:  note,
	}
}

// timeStat is a moment as a stat: its day, with the clock and any further notes beneath.
func timeStat(ctx context.Context, label string, t, now time.Time, notes ...string) components.Stat {
	if t.IsZero() {
		return components.Stat{Label: label}
	}
	note := shoptime.ClockText(t)
	for _, n := range notes {
		note = fmt.Sprintf(i18n.T(ctx, i18n.KeyOrderFactNote), note, n)
	}
	return dateStat(ctx, label, shoptime.DateOf(t, now), note)
}

func (s OrderShipment) DeliveredStamp(ctx context.Context) string {
	return shoptime.StampText(ctx, s.DeliveredAt)
}

func (s OrderShipment) ShippedStamp(ctx context.Context) string {
	return shoptime.StampText(ctx, s.ShippedAt)
}

// RescissionLastDay is the day the statutory sentence names, as the database counted it for the parcel.
func (v *OrderView) RescissionLastDay(ctx context.Context, s OrderShipment) string {
	return shoptime.DateText(ctx, shoptime.DateOf(s.RescissionEnds, v.now()))
}
