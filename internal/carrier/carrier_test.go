package carrier

import (
	"slices"
	"testing"
)

func TestAllIsTheKnownClosedSetAndReturnsFreshStorage(t *testing.T) {
	t.Parallel()

	got := All()
	if len(got) != 8 {
		t.Fatalf("All() has %d carriers, want 8: %v", len(got), got)
	}
	for _, c := range got {
		if !c.Known() {
			t.Errorf("All() contains unknown carrier %q", c)
		}
	}
	got[0] = Carrier("mutated")
	if slices.Contains(All(), Carrier("mutated")) {
		t.Error("mutating All() changed the canonical set")
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
			if got := tt.c.TrackingURL(tt.number); got != tt.want {
				t.Errorf("TrackingURL(%q) = %q, want %q", tt.number, got, tt.want)
			}
			if got := tt.c.DeepLinks(); got != tt.deepens {
				t.Errorf("DeepLinks() = %t, want %t", got, tt.deepens)
			}
		})
	}
}
