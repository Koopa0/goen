//go:build integration

package cart

import (
	"context"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/order"
)

type CheckoutQuoteLine = checkoutQuoteLine

type CheckoutQuote = checkoutQuote

type CheckoutQuoteID = checkoutQuoteID

var ErrCheckoutChanged = errCheckoutChanged

// PlaceOrder exposes the HTTP handler's transaction only in integration builds;
// production keeps form parsing and placement package-owned.
func (s *Store) PlaceOrder(
	ctx context.Context,
	cartID uuid.UUID,
	userID uuid.NullUUID,
	shippingVersionID uuid.UUID,
	addr *order.Delivery,
	inv *Invoice,
	couponCode string,
	shown CheckoutQuoteID,
	idempotencyKey string,
) (string, error) {
	attemptID, err := parseCheckoutAttemptID(idempotencyKey)
	if err != nil {
		return "", err
	}
	return s.placeOrder(
		ctx, cartID, userID, shippingVersionID, addr, inv, couponCode, shown, attemptID,
	)
}

var FreeDeliveryFor = freeDeliveryFor
