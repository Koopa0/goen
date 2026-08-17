package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db"
)

// ErrTooLateToCorrect is a delivery address that can no longer be changed.
var ErrTooLateToCorrect = errors.New("admin: this order has already shipped")

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

	PickupBrand     string
	PickupStoreCode string
	PickupStoreName string
}

// CorrectDelivery rewrites an order's delivery details.
func (s *Store) CorrectDelivery(ctx context.Context, number string, d *Delivery) error {
	row, err := s.q.OrderDestinationKind(ctx, number)
	if err != nil {
		return ErrNotFound
	}
	to, ok := cart.DestinationFor(row.DestinationKind)
	if !ok {
		return fmt.Errorf("order %s ships by a method with an unknown destination %q",
			number, row.DestinationKind)
	}

	addr := &cart.Address{
		To: to, Email: d.Email, Name: d.Recipient, Phone: d.Phone,
		PostalCode: d.PostalCode, City: d.City, District: d.District, Street: d.Street,
		PickupBrand: d.PickupBrand, PickupStoreCode: d.PickupStoreCode,
		PickupStoreName: d.PickupStoreName,
	}
	addr.Trim()
	if errs := addr.Validate(); len(errs) > 0 {
		return fmt.Errorf("%w: %s (%s)", ErrInvalid, errs[0].Field, errs[0].MessageKey)
	}
	addr.ForDestination()

	return s.audited(ctx, Event{
		Action: ActionCorrectDelivery, Table: "order_private_data",
		Before: nil,
		After:  map[string]any{"order_number": number, "destination": string(to)},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, updErr := q.UpdateOrderDelivery(ctx, db.UpdateOrderDeliveryParams{
				OrderNumber: number,
				Email:       text(addr.Email), RecipientName: text(addr.Name),
				Phone:      text(addr.Phone),
				PostalCode: addr.PostalCode, City: addr.City,
				District: addr.District, Street: addr.Street,
				PickupBrand: addr.PickupBrand, PickupStoreCode: addr.PickupStoreCode,
				PickupStoreName: addr.PickupStoreName,
			})
			if updErr != nil {
				return fmt.Errorf("%w: %s", ErrRefused, updErr.Error())
			}
			if n == 0 {
				return ErrTooLateToCorrect
			}
			return nil
		})
}

func deliveryFormOf(values func(string) string) *Delivery {
	get := func(k string) string { return strings.TrimSpace(values(k)) }
	return &Delivery{
		Email: get("email"), Recipient: get("recipient"), Phone: get("phone"),
		PostalCode: get("postal_code"), City: get("city"),
		District: get("district"), Street: get("street"),
		PickupBrand: get("pickup_brand"), PickupStoreCode: get("pickup_store_code"),
		PickupStoreName: get("pickup_store_name"),
	}
}
