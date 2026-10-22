package checkout

import (
	"slices"
	"testing"

	"github.com/koopa0/goen/internal/shipping"
)

// TestPickupChains: pickupChains offers the chains whose store picker the shop
// integrates, each known to validation, as a copy the caller cannot change. It
// holds the two sets apart: a shopper chooses between the chains whose store
// picker the shop will integrate, and the back office keeps every chain
// (shipping.Chains), because an order already placed at one of the others still
// has to be correctable.
func TestPickupChains(t *testing.T) {
	t.Parallel()

	want := []shipping.Chain{shipping.ChainSevenEleven, shipping.ChainFamilyMart}
	got := pickupChains()
	if !slices.Equal(got, want) {
		t.Errorf("pickupChains() = %q, want %q", got, want)
	}
	for _, chain := range got {
		if !chain.Known() {
			t.Errorf("checkout offers %q, which validation rejects", chain)
		}
	}
	got[0] = "other_chain"
	if !slices.Equal(pickupChains(), want) {
		t.Error("changing one result of pickupChains() changed the next")
	}
}
