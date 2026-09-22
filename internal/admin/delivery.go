package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/pages"
)

// ErrTooLateToCorrect is a delivery address that can no longer be changed.
var ErrTooLateToCorrect = errors.New("admin: this order no longer accepts delivery corrections")

// Delivery is the correction a staff member typed; which half applies follows
// from the ORDER's shipping method, never from the form.
type Delivery struct {
	Email     string
	Recipient string
	Phone     string

	PostalCode string
	City       string
	District   string
	Street     string

	PickupBrand     pickup.Brand
	PickupStoreCode string
	PickupStoreName string
}

// ErrDeliveryZoneUnavailable means a postal source cannot be priced safely.
var ErrDeliveryZoneUnavailable = errors.New("admin: cannot resolve delivery surcharge")

// DeliverySurchargeError preserves the original charged shipping while naming
// the current destination surcharge difference that prevents this correction.
type DeliverySurchargeError struct {
	ShippingCents int64
	DeltaCents    int64
}

func (e *DeliverySurchargeError) Error() string { return "admin: delivery surcharge changes" }

// CorrectDelivery changes private delivery data only when the original charge
// can stand. The order lock serializes this decision with shipment/cancellation.
func (s *Store) CorrectDelivery(ctx context.Context, number string, d *Delivery) error {
	after := map[string]any{"order_number": number}
	return s.audited(ctx, Event{
		Action: actionCorrectDelivery, Table: "order_private_data",
		Before: nil, After: after,
	}, func(ctx context.Context, q *db.Queries) error {
		row, err := q.LockOrderDelivery(ctx, number)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock order delivery: %w", err)
		}
		state := pages.FulfillmentStatus(row.FulfillmentStatus)
		if state != pages.FulfillmentPending && state != pages.FulfillmentPicking {
			return ErrTooLateToCorrect
		}
		to, ok := cart.DestinationFor(row.DestinationKind)
		if !ok {
			return ErrDeliveryZoneUnavailable
		}
		after["destination"] = string(to)
		addr, err := validatedDelivery(d, to)
		if err != nil {
			return err
		}
		if err := checkDeliverySurcharge(ctx, q, &row, addr); err != nil {
			return err
		}

		n, err := q.UpdateOrderDelivery(ctx, db.UpdateOrderDeliveryParams{
			OrderNumber: number,
			Email:       text(addr.Email), RecipientName: text(addr.Name), Phone: text(addr.Phone),
			PostalCode: addr.PostalCode, City: addr.City, District: addr.District, Street: addr.Street,
			PickupBrand: string(addr.PickupBrand), PickupStoreCode: addr.PickupStoreCode,
			PickupStoreName: addr.PickupStoreName,
		})
		if err != nil {
			return fmt.Errorf("%w: %w", ErrRefused, err)
		}
		if n == 0 {
			return ErrTooLateToCorrect
		}
		return nil
	})
}

func validatedDelivery(d *Delivery, to cart.Destination) (*cart.Address, error) {
	addr := &cart.Address{
		To: to, Email: d.Email, Name: d.Recipient, Phone: d.Phone,
		PostalCode: d.PostalCode, City: d.City, District: d.District, Street: d.Street,
		PickupBrand: d.PickupBrand, PickupStoreCode: d.PickupStoreCode,
		PickupStoreName: d.PickupStoreName,
	}
	addr.Trim()
	if errs := addr.Validate(); len(errs) > 0 {
		for _, fieldErr := range errs {
			if fieldErr.Field == "postal_code" {
				return nil, ErrDeliveryZoneUnavailable
			}
		}
		return nil, fmt.Errorf("%w: %s (%s)", ErrInvalid, errs[0].Field, errs[0].MessageKey)
	}
	addr.ForDestination()
	return addr, nil
}

// Read the old postcode only after acquiring the order lock. A waiter must
// compare against the preceding correction, not its earlier snapshot.
func checkDeliverySurcharge(ctx context.Context, q *db.Queries, order *db.LockOrderDeliveryRow, addr *cart.Address) error {
	if addr.To != cart.ToAddress {
		return nil
	}
	comparison, err := q.DeliverySurchargeComparison(ctx, db.DeliverySurchargeComparisonParams{
		VersionID: order.ShippingVersionID, OrderID: order.ID, NewPostalCode: addr.PostalCode,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDeliveryZoneUnavailable
	}
	if err != nil {
		return fmt.Errorf("compare delivery surcharges: %w", err)
	}
	if comparison.Erased {
		return ErrTooLateToCorrect
	}
	if !comparison.Resolved {
		return ErrDeliveryZoneUnavailable
	}
	if delta := comparison.NewSurcharge - comparison.OldSurcharge; delta != 0 {
		return &DeliverySurchargeError{ShippingCents: order.ShippingCents, DeltaCents: delta}
	}
	return nil
}

func deliveryFormOf(values func(string) string) *Delivery {
	get := func(k string) string { return strings.TrimSpace(values(k)) }
	return &Delivery{
		Email: get("email"), Recipient: get("recipient"), Phone: get("phone"),
		PostalCode: get("postal_code"), City: get("city"),
		District: get("district"), Street: get("street"),
		PickupBrand: pickup.Brand(get("pickup_brand")), PickupStoreCode: get("pickup_store_code"),
		PickupStoreName: get("pickup_store_name"),
	}
}
