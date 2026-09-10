//go:build integration

package cart

import (
	"context"

	"github.com/google/uuid"
)

// CheckoutQuoteLine exposes a quote line only to integration fixtures.
type CheckoutQuoteLine = checkoutQuoteLine

// CheckoutQuote exposes a canonical quote only to integration fixtures.
type CheckoutQuote = checkoutQuote

// CheckoutQuoteID exposes a quote identity only to integration fixtures.
type CheckoutQuoteID = checkoutQuoteID

// ErrCheckoutChanged exposes the private sentinel only to integration fixtures.
var ErrCheckoutChanged = errCheckoutChanged

// PlaceOrder exposes the HTTP handler's transaction only in integration builds.
// The ordinary production build keeps form parsing and placement package-owned.
func (s *Store) PlaceOrder(
	ctx context.Context,
	cartID uuid.UUID,
	userID uuid.NullUUID,
	shippingVersionID uuid.UUID,
	addr *Address,
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
