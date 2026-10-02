//go:build integration

package admin_test

import (
	"slices"
	"testing"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/carrier"
)

// The dispatch form lists what the order can use and selects what it implies:
// a store order's chain carries it, and a home delivery names no carrier code.
func TestTheOrderPageCarriesTheCarriersTheDispatchFormOffers(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	store, err := s.Order(ctx, pickupOrderForCorrection(t))
	if err != nil {
		t.Fatalf("read store order: %v", err)
	}
	if want := []carrier.Carrier{carrier.FamilyMart}; !slices.Equal(store.ShipCarriers, want) ||
		store.ShipCarrier != string(carrier.FamilyMart) {
		t.Errorf("a family_mart order lists %v with %q chosen, want only %v chosen", store.ShipCarriers, store.ShipCarrier, want)
	}

	home, err := s.Order(ctx, placeUnpaidOrder(t))
	if err != nil {
		t.Fatalf("read home order: %v", err)
	}
	want, _ := carrier.ForDelivery("")
	if !slices.Equal(home.ShipCarriers, want) || home.ShipCarrier != "" {
		t.Errorf("a home order lists %v with %q chosen, want %v and none chosen", home.ShipCarriers, home.ShipCarrier, want)
	}
}
