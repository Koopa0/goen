//go:build integration

package checkout

import (
	"context"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/order/delivery"
)

type QuoteLine = checkoutQuoteLine

type Quote = checkoutQuote

type QuoteID = checkoutQuoteID

var ErrQuoteChanged = errCheckoutChanged

// PlaceOrder exposes the HTTP handler's transaction only in integration builds;
// production keeps form parsing and placement package-owned.
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
