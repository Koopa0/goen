package orders

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/web"
)

var ErrTooLateToCorrect = errors.New("orders: this order has already shipped")

// DeliveryCorrection is the correction a staff member typed; which half applies follows
// from the ORDER's shipping method, never from the form.
type DeliveryCorrection struct {
	Email     string
	Recipient string
	Phone     string

	PostalCode string
	City       string
	District   string
	Street     string

	PickupChain     pickup.Chain
	PickupStoreCode string
	PickupStoreName string
}

type DeliveryPostalError struct{ Key i18n.Key }

func (e *DeliveryPostalError) Error() string {
	return "admin: delivery postcode refused: " + string(e.Key)
}

type DeliveryValidationError struct{ Fields []web.FieldRefusal }

func (e *DeliveryValidationError) Error() string {
	return fmt.Sprintf("admin: delivery refused on %d fields", len(e.Fields))
}

func (e *DeliveryValidationError) Unwrap() error { return ErrInvalid }

// CorrectDelivery changes private delivery data only when the order's surcharge
// zone stands. The order lock serializes this decision with shipment and
// cancellation.
func (s *Store) CorrectDelivery(ctx context.Context, number string, d *DeliveryCorrection) error {
	after := map[string]any{"order_number": number}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionCorrectDelivery, Table: "order_private_data",
		Before: nil, After: after,
	}, func(ctx context.Context, q *db.Queries) error {
		row, err := q.LockOrderDelivery(ctx, number)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock order delivery: %w", err)
		}
		switch order.FulfillmentStatus(row.FulfillmentStatus) {
		case order.FulfillmentShipped, order.FulfillmentDelivered, order.FulfillmentCompleted:
			return ErrTooLateToCorrect
		case order.FulfillmentPending, order.FulfillmentPicking, order.FulfillmentCancelled:
			// Still correctable; UpdateOrderDelivery's WHERE clause is the authority.
		}
		to, ok := destination.For(row.DestinationKind)
		if !ok {
			return fmt.Errorf("order %s ships by a method with an unknown destination %q",
				number, row.DestinationKind)
		}
		after["destination"] = string(to)
		addr, err := validatedDelivery(d, to)
		if err != nil {
			return err
		}
		if zoneErr := checkDeliveryZone(ctx, q, row.ID, addr); zoneErr != nil {
			return zoneErr
		}

		n, err := q.UpdateOrderDelivery(ctx, db.UpdateOrderDeliveryParams{
			OrderNumber: number,
			Email:       text(addr.Email), RecipientName: text(addr.RecipientName), Phone: text(addr.Phone),
			PostalCode: addr.PostalCode, City: addr.City, District: addr.District, Street: addr.Street,
			PickupChain: string(addr.PickupChain), PickupStoreCode: addr.PickupStoreCode,
			PickupStoreName: addr.PickupStoreName,
		})
		if err != nil {
			return pgerr.WrapRefusal(err, ErrRefused)
		}
		if n == 0 {
			return ErrTooLateToCorrect
		}
		return nil
	})
}

func validatedDelivery(d *DeliveryCorrection, to destination.Kind) (*order.Delivery, error) {
	addr := &order.Delivery{
		To: to, Email: d.Email, RecipientName: d.Recipient, Phone: d.Phone,
		PostalCode: d.PostalCode, City: d.City, District: d.District, Street: d.Street,
		PickupChain: d.PickupChain, PickupStoreCode: d.PickupStoreCode,
		PickupStoreName: d.PickupStoreName,
	}
	addr.Trim()
	if errs := addr.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("%w: %s (%s)", ErrInvalid, errs[0].Field, errs[0].MessageKey)
	}
	addr.DropOtherDestination()
	return addr, nil
}

// checkDeliveryZone reads the saved postcode only after the order lock is held,
// so a waiter compares against the correction before it and not an earlier
// snapshot. A pickup order has no postcode and no zone to leave.
func checkDeliveryZone(ctx context.Context, q *db.Queries, orderID uuid.UUID, addr *order.Delivery) error {
	if addr.To != destination.Address {
		return nil
	}
	cmp, err := q.DeliveryZoneComparison(ctx, db.DeliveryZoneComparisonParams{
		OrderID: orderID, NewPostalCode: addr.PostalCode,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTooLateToCorrect
	}
	if err != nil {
		return fmt.Errorf("compare delivery zones: %w", err)
	}
	switch {
	case cmp.Erased:
		return ErrTooLateToCorrect
	case !cmp.OldResolved:
		return &DeliveryPostalError{Key: i18n.KeyDeliveryZoneUnknown}
	case !cmp.SameZone:
		return &DeliveryPostalError{Key: i18n.KeyDeliveryZoneChanged}
	}
	return nil
}

func deliveryFormOf(values func(string) string) *DeliveryCorrection {
	get := func(key string) string { return strings.TrimSpace(values(key)) }
	return &DeliveryCorrection{
		Email: get("email"), Recipient: get("recipient"), Phone: get("phone"),
		PostalCode: get("postal_code"), City: get("city"),
		District: get("district"), Street: get("street"),
		PickupChain: pickup.Chain(get("pickup_chain")), PickupStoreCode: get("pickup_store_code"),
		PickupStoreName: get("pickup_store_name"),
	}
}
