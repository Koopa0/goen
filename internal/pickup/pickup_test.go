package pickup

import (
	"slices"
	"testing"
)

func TestKnownChainsIsTheClosedSetAndReturnsFreshStorage(t *testing.T) {
	t.Parallel()

	want := []Chain{SevenEleven, FamilyMart, HiLife, OKMart}
	got := KnownChains()
	if !slices.Equal(got, want) {
		t.Fatalf("KnownChains() = %v, want %v", got, want)
	}
	for _, chain := range got {
		if !chain.Known() {
			t.Errorf("KnownChains() contains unknown chain %q", chain)
		}
	}

	got[0] = Chain("mutated")
	if !slices.Equal(KnownChains(), want) {
		t.Error("mutating KnownChains() changed the canonical set")
	}
	if Chain("other_chain").Known() {
		t.Error("an unknown chain is Known")
	}
}

func TestOnlyCurrentPickupChainsAreAtCheckout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		chain Chain
		want  bool
	}{
		{SevenEleven, true}, {FamilyMart, true}, {HiLife, false}, {OKMart, false}, {"other_chain", false}, {"", false},
	} {
		t.Run(string(tc.chain), func(t *testing.T) {
			t.Parallel()
			if got := tc.chain.AtCheckout(); got != tc.want {
				t.Errorf("%q AtCheckout = %v, want %v", tc.chain, got, tc.want)
			}
		})
	}
}
