package shipping

import (
	"slices"
	"testing"
)

// TestChain_Carrier: a chain's Carrier is the same-named carrier, with the same
// wire value, and an unknown chain has none.
func TestChain_Carrier(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		chain Chain
		want  Carrier
	}{
		{ChainSevenEleven, CarrierSevenEleven},
		{ChainFamilyMart, CarrierFamilyMart},
		{ChainHiLife, CarrierHiLife},
		{ChainOKMart, CarrierOKMart},
	} {
		if got := tc.chain.Carrier(); got != tc.want {
			t.Errorf("%q.Carrier() = %q, want %q", tc.chain, got, tc.want)
		}
		if string(tc.chain) != string(tc.want) {
			t.Errorf("chain %q and carrier %q no longer share a wire value", tc.chain, tc.want)
		}
	}
	for _, unknown := range []Chain{"", "other_chain"} {
		if got := unknown.Carrier(); got != "" {
			t.Errorf("%q.Carrier() = %q, want none", unknown, got)
		}
	}
}

// TestCarriersForChainAndDestination: CarriersFor answers by chain and
// destination: a store chain to a pickup point, an unknown chain to a pickup
// point, and a home delivery.
func TestCarriersForChainAndDestination(t *testing.T) {
	t.Parallel()

	valid, implied := CarriersFor(ChainFamilyMart, DestinationPickupPoint)
	if !slices.Equal(valid, []Carrier{CarrierFamilyMart}) || implied != CarrierFamilyMart {
		t.Errorf("store chain: valid %v implied %q", valid, implied)
	}

	valid, implied = CarriersFor("other_chain", DestinationPickupPoint)
	want := []Carrier{CarrierSevenEleven, CarrierFamilyMart, CarrierHiLife, CarrierOKMart}
	if !slices.Equal(valid, want) || implied != "" {
		t.Errorf("unknown chain to a pickup point: valid %v implied %q, want %v and none", valid, implied, want)
	}

	valid, implied = CarriersFor("", DestinationAddress)
	want = []Carrier{CarrierBlackCat, CarrierHCT, CarrierChunghwaPost, CarrierKerryTJ}
	if !slices.Equal(valid, want) || implied != "" {
		t.Errorf("home delivery: valid %v implied %q, want %v and none", valid, implied, want)
	}
}

func TestParseDestination(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]Destination{"address": DestinationAddress, "pickup_point": DestinationPickupPoint} {
		if got, ok := ParseDestination(in); !ok || got != want {
			t.Errorf("ParseDestination(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "Address", "store", "pickup"} {
		if got, ok := ParseDestination(in); ok || got != "" {
			t.Errorf("ParseDestination(%q) = %q, %v; want refused", in, got, ok)
		}
	}
}
