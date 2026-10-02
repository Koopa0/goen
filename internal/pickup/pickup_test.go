package pickup

import (
	"slices"
	"testing"
)

func TestOfferedIsTheKnownClosedSetAndReturnsFreshStorage(t *testing.T) {
	t.Parallel()

	want := []Chain{SevenEleven, FamilyMart, HiLife, OKMart}
	got := Offered()
	if !slices.Equal(got, want) {
		t.Fatalf("Offered() = %v, want %v", got, want)
	}
	for _, chain := range got {
		if !chain.Known() {
			t.Errorf("Offered() contains unknown chain %q", chain)
		}
	}

	got[0] = Chain("mutated")
	if !slices.Equal(Offered(), want) {
		t.Error("mutating Offered() changed the canonical set")
	}
	if Chain("other_chain").Known() {
		t.Error("an unoffered chain is Known")
	}
}
