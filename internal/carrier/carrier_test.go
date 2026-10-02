package carrier

import (
	"slices"
	"testing"

	"github.com/koopa0/goen/internal/pickup"
)

func TestTheDeliveryChoicesAreTheKnownClosedSet(t *testing.T) {
	t.Parallel()

	home, _ := ForDelivery("", false)
	stores, _ := ForDelivery("", true)
	got := slices.Concat(home, stores)
	if len(got) != 8 {
		t.Fatalf("a home and a store order offer %d carriers between them, want 8: %v", len(got), got)
	}
	for _, c := range got {
		if !c.Known() {
			t.Errorf("a delivery offers unknown carrier %q", c)
		}
	}
	for _, typed := range []Carrier{"", "黑貓", "黑貓宅急便", "T-cat"} {
		if typed.Known() {
			t.Errorf("%q is Known: only the codes are", typed)
		}
	}
}

func TestTrackingURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		c       Carrier
		number  string
		want    string
		deepens bool
	}{
		{"black cat opens the parcel", BlackCat, "903-2214 8872", "https://www.t-cat.com.tw/Inquire/TraceDetail.aspx?BillID=903-2214+8872", true},
		{"black cat with no number falls back to the page", BlackCat, " ", "https://www.t-cat.com.tw/inquire/trace.aspx", true},
		{"hct is the lookup page", HCT, "1234567890", "https://www.hct.com.tw/Search/SearchGoods_n.aspx", false},
		{"seven eleven is the lookup page", SevenEleven, "X", "https://eservice.7-11.com.tw/e-tracking/search.aspx", false},
		{"hi-life publishes none goen could confirm", HiLife, "X", "", false},
		{"ok mart publishes none goen could confirm", OKMart, "X", "", false},
		{"unknown carrier", Carrier("黑貓"), "X", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.c.TrackingURL(tt.number); got != tt.want {
				t.Errorf("TrackingURL(%q) = %q, want %q", tt.number, got, tt.want)
			}
			if got := tt.c.DeepLinks(); got != tt.deepens {
				t.Errorf("DeepLinks() = %t, want %t", got, tt.deepens)
			}
		})
	}
}

func TestForDeliveryNamesTheCarriersAnOrderCanUse(t *testing.T) {
	t.Parallel()

	for _, chain := range []pickup.Brand{pickup.SevenEleven, pickup.FamilyMart, pickup.HiLife, pickup.OKMart} {
		valid, implied := ForDelivery(chain, true)
		if implied != Carrier(chain) || len(valid) != 1 || valid[0] != implied {
			t.Errorf("a %s order: valid %v implied %q, want only the chain's own carrier", chain, valid, implied)
		}
	}

	home, implied := ForDelivery("", false)
	if implied != "" {
		t.Errorf("a home delivery implies %q: nothing on the order names a carrier code", implied)
	}
	for _, c := range home {
		if !c.Known() {
			t.Errorf("home delivery lists %q, which is not a carrier", c)
		}
		if slices.Contains([]Carrier{SevenEleven, FamilyMart, HiLife, OKMart}, c) {
			t.Errorf("home delivery lists the convenience-store carrier %q", c)
		}
	}
	stores, none := ForDelivery("", true)
	if none != "" || len(stores) != 4 || slices.Contains(stores, BlackCat) {
		t.Errorf("a store order with no chain lists %v implying %q, want the four store carriers and none implied", stores, none)
	}
	if len(home) != 4 {
		t.Errorf("home delivery lists %d carriers, want 4", len(home))
	}
}
