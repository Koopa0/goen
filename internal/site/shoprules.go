package site

import (
	"context"
	"fmt"
)

// ShopRules is what the shop states about itself, from where each rule is stored.
// The home, department and product pages show it; each converts it to its view.
type ShopRules struct {
	// FreeDeliveryCents is zero where some delivery method never turns free.
	FreeDeliveryCents int64
	LowestFeeCents    int64
	PickupOffered     bool
	// RescissionDays is the statutory window and ReturnDays goen's own, in days from the day after delivery.
	RescissionDays int64
	ReturnDays     int64
	// HoldMinutes is how long placing an order holds the stock, and PayStartMinutes how long the customer then
	// has to start paying.
	HoldMinutes     int64
	PayStartMinutes int64
}

// ShopRules reads the rules the shop states about itself. Where checkout offers
// no pickup (WithoutPickup), a threshold or floor does not count it.
func (s *Store) ShopRules(ctx context.Context) (ShopRules, error) {
	withPickup := !s.noPickup
	freeOver, err := s.q.FreeDeliveryThreshold(ctx, withPickup)
	if err != nil {
		return ShopRules{}, fmt.Errorf("read free delivery threshold: %w", err)
	}
	lowestFee, err := s.q.LowestDeliveryFee(ctx, withPickup)
	if err != nil {
		return ShopRules{}, fmt.Errorf("read lowest delivery fee: %w", err)
	}
	windows, err := s.q.StatedWindows(ctx)
	if err != nil {
		return ShopRules{}, fmt.Errorf("read stated windows: %w", err)
	}
	return ShopRules{
		FreeDeliveryCents: freeOver, LowestFeeCents: lowestFee, PickupOffered: withPickup,
		RescissionDays: windows.RescissionDays, ReturnDays: windows.ReturnDays,
		HoldMinutes: windows.HoldMinutes, PayStartMinutes: windows.PayStartMinutes,
	}, nil
}
