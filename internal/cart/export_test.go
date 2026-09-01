//go:build !integration

package cart

import (
	"context"

	"github.com/google/uuid"
)

// CheckoutQuoteLine exposes a quote line only to black-box integration tests.
type CheckoutQuoteLine = checkoutQuoteLine

// CheckoutQuote exposes a canonical quote only to black-box integration tests.
type CheckoutQuote = checkoutQuote

// CheckoutQuoteID exposes a quote identity only to black-box integration tests.
type CheckoutQuoteID = checkoutQuoteID

// ErrCheckoutChanged exposes the private sentinel only to black-box tests.
var ErrCheckoutChanged = errCheckoutChanged

// PlaceOrder exposes the HTTP handler's transaction only to black-box tests.
// Production ownership stays package-private: form parsing is part of the
// handler/store invariant rather than a sibling-package API.
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
