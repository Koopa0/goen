package shipping

import (
	"slices"
	"testing"
)

func TestKnownChainsIsTheClosedSetAndReturnsFreshStorage(t *testing.T) {
	t.Parallel()

	want := []Chain{ChainSevenEleven, ChainFamilyMart, ChainHiLife, ChainOKMart}
	got := Chains()
	if !slices.Equal(got, want) {
		t.Fatalf("Chains() = %v, want %v", got, want)
	}
	for _, chain := range got {
		if !chain.Known() {
			t.Errorf("Chains() contains unknown chain %q", chain)
		}
	}

	got[0] = Chain("mutated")
	if !slices.Equal(Chains(), want) {
		t.Error("mutating Chains() changed the canonical set")
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
		{ChainSevenEleven, true}, {ChainFamilyMart, true}, {ChainHiLife, false}, {ChainOKMart, false}, {"other_chain", false}, {"", false},
	} {
		t.Run(string(tc.chain), func(t *testing.T) {
			t.Parallel()
			if got := tc.chain.AtCheckout(); got != tc.want {
				t.Errorf("%q AtCheckout = %v, want %v", tc.chain, got, tc.want)
			}
		})
	}
}

// A chain prints under its own name, which it already writes in both scripts,
// so the name is the same in every language.
func TestChain_Name(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		chain Chain
		want  string
	}{
		{ChainSevenEleven, "7-ELEVEN"},
		{ChainFamilyMart, "全家 FamilyMart"},
		{ChainHiLife, "萊爾富 Hi-Life"},
		{ChainOKMart, "OK mart"},
		{"", ""},
	} {
		if got := tc.chain.Name(); got != tc.want {
			t.Errorf("Chain(%q).Name() = %q, want %q", tc.chain, got, tc.want)
		}
	}
	for _, chain := range Chains() {
		if chain.Name() == "" {
			t.Errorf("Chain(%q) has no name", chain)
		}
	}
}
