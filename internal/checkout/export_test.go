//go:build !integration

package checkout

import (
	"context"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/order/delivery"
)

// QuoteLine exposes a quote line only to black-box integration tests.
type QuoteLine = checkoutQuoteLine

// Quote exposes a canonical quote only to black-box integration tests.
type Quote = checkoutQuote

// QuoteID exposes a quote identity only to black-box integration tests.
type QuoteID = checkoutQuoteID

// ErrQuoteChanged exposes the private sentinel only to black-box tests.
var ErrQuoteChanged = errCheckoutChanged

// PlaceOrder exposes the HTTP handler's transaction only to black-box tests.
// Production ownership stays package-private: form parsing is part of the
// handler/store invariant rather than a sibling-package API.
func (s *Store) PlaceOrder(
	ctx context.Context,
	cartID uuid.UUID,
	userID uuid.NullUUID,
	shippingVersionID uuid.UUID,
	addr *delivery.Details,
	inv *Invoice,
	couponCode string,
	shown QuoteID,
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

// Carts exposes the cart store a checkout reads, on the checkout's own pool, to
// tests that fill the cart they place.
func (s *Store) Carts() *cart.Store { return s.carts }
