package cart

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseQuantity(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		in   string
		want int32
	}{
		{"1", 1},
		{"3", 3},
		{"999", 999},
		{"1000", MaxLineQuantity}, // clamped to the schema's own ceiling
		{"0", 1},                  // the button means "add one"
		{"-5", 1},
		{"", 1},
		{"abc", 1},
		{" 2 ", 2},
	} {
		if got := ParseQuantity(tt.in); got != tt.want {
			t.Errorf("ParseQuantity(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestParseQuantityAllowingZero(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		in     string
		want   int32
		wantOK bool
	}{
		{"0", 0, true}, // on the cart page zero means remove
		{"2", 2, true},
		{"1000", MaxLineQuantity, true},
		{"-1", 0, false},
		{"x", 0, false},
	} {
		got, ok := ParseQuantityAllowingZero(tt.in)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("ParseQuantityAllowingZero(%q) = %d/%v, want %d/%v", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestShippingFee(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name                          string
		fee, freeOver, subtotal, want int64
	}{
		{"below the threshold pays", 8000, 300000, 299900, 8000},
		{"exactly the threshold is free", 8000, 300000, 300000, 0},
		{"above the threshold is free", 8000, 300000, 500000, 0},
		{"no threshold always pays", 8000, 0, 900000, 8000},
		{"a free method is free", 0, 0, 100, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ShippingFee(tt.fee, tt.freeOver, tt.subtotal); got != tt.want {
				t.Errorf("ShippingFee(%d, %d, %d) = %d, want %d",
					tt.fee, tt.freeOver, tt.subtotal, got, tt.want)
			}
		})
	}
}

// TestTheCartSpeaksOfFreeDeliveryOnlyWhereItIsTrueForEveryMethod holds the cart's
// line to what checkout charges: one amount at which every method on offer turns
// free, or nothing.
func TestTheCartSpeaksOfFreeDeliveryOnlyWhereItIsTrueForEveryMethod(t *testing.T) {
	t.Parallel()

	home := ShippingChoice{FeeCents: 100, FreeOverCents: 300000}
	pickupPoint := ShippingChoice{FeeCents: 60, FreeOverCents: 150000}
	never := ShippingChoice{FeeCents: 100}
	free := func(c ShippingChoice) ShippingChoice { c.FeeCents, c.Free = 0, true; return c }

	islands := func(c ShippingChoice) ShippingChoice { c.SurchargeZones = []string{"離島"}; return c }

	for _, tt := range []struct {
		name     string
		choices  []ShippingChoice
		subtotal int64
		want     FreeDelivery
	}{
		{"nothing offered", nil, 0, FreeDelivery{}},
		{"one method, short", []ShippingChoice{home}, 100000,
			FreeDelivery{Kind: FreeDeliveryShort, ShortfallCents: 200000, ThresholdCents: 300000}},
		{"two thresholds, the higher one is the one that is true for both",
			[]ShippingChoice{home, pickupPoint}, 100000,
			FreeDelivery{Kind: FreeDeliveryShort, ShortfallCents: 200000, ThresholdCents: 300000}},
		{"one method is already free, the other is not",
			[]ShippingChoice{home, free(pickupPoint)}, 200000,
			FreeDelivery{Kind: FreeDeliveryShort, ShortfallCents: 100000, ThresholdCents: 300000}},
		{"a method that is never free says nothing", []ShippingChoice{home, never}, 100000, FreeDelivery{}},
		{"every method free", []ShippingChoice{free(home), free(pickupPoint)}, 300000,
			FreeDelivery{Kind: FreeDeliveryReached, ThresholdCents: 300000}},
		{"every method free, two of them charging the same zone extra",
			[]ShippingChoice{islands(free(home)), islands(free(pickupPoint)), free(pickupPoint)}, 300000,
			FreeDelivery{Kind: FreeDeliveryReached, ThresholdCents: 300000, SurchargeZones: []string{"離島"}}},
	} {
		if got := freeDeliveryFor(tt.choices, tt.subtotal); !cmp.Equal(got, tt.want) {
			t.Errorf("%s: got %+v, want %+v", tt.name, got, tt.want)
		}
	}
}
