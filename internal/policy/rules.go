package policy

import (
	"context"
	"fmt"
)

// Rules is what the shop states about itself, from where each rule is stored.
// The home, department and product pages show it; each converts it to its view.
type Rules struct {
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

// Rules reads the rules the shop states about itself. Where checkout offers
// no pickup (WithoutPickup), a threshold or floor does not count it.
func (s *Store) Rules(ctx context.Context) (Rules, error) {
	withPickup := !s.noPickup
	freeOver, err := s.q.FreeDeliveryThreshold(ctx, withPickup)
	if err != nil {
		return Rules{}, fmt.Errorf("read free delivery threshold: %w", err)
	}
	lowestFee, err := s.q.LowestDeliveryFee(ctx, withPickup)
	if err != nil {
		return Rules{}, fmt.Errorf("read lowest delivery fee: %w", err)
	}
	windows, err := s.q.StatedWindows(ctx)
	if err != nil {
		return Rules{}, fmt.Errorf("read stated windows: %w", err)
	}
	return Rules{
		FreeDeliveryCents: freeOver, LowestFeeCents: lowestFee, PickupOffered: withPickup,
		RescissionDays: windows.RescissionDays, ReturnDays: windows.ReturnDays,
		HoldMinutes: windows.HoldMinutes, PayStartMinutes: windows.PayStartMinutes,
	}, nil
}
