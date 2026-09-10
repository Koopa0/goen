package pickup

import (
	"slices"
	"testing"
)

func TestOfferedIsTheKnownClosedSetAndReturnsFreshStorage(t *testing.T) {
	t.Parallel()

	want := []Brand{SevenEleven, FamilyMart, HiLife, OKMart}
	got := Offered()
	if !slices.Equal(got, want) {
		t.Fatalf("Offered() = %v, want %v", got, want)
	}
	for _, brand := range got {
		if !brand.Known() {
			t.Errorf("Offered() contains unknown brand %q", brand)
		}
	}

	got[0] = Brand("mutated")
	if !slices.Equal(Offered(), want) {
		t.Error("mutating Offered() changed the canonical set")
	}
	if Brand("other_chain").Known() {
		t.Error("an unoffered brand is Known")
	}
}
