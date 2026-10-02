package catalog

import "testing"

// A page's own order is never written into an address, and an unknown value
// falls back to that order rather than to the other page's.
func TestAnUnchosenSortIsTheOrderOfThePageAskingAndNeverInTheAddress(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		value    string
		unchosen Sort
		want     Sort
		param    string
	}{
		{"", SortNewest, SortNewest, ""},
		{"", SortRelevance, SortRelevance, ""},
		{"nonsense", SortRelevance, SortRelevance, ""},
		{"price_asc", SortNewest, SortPriceAsc, "price_asc"},
		{"rating", SortRelevance, SortRating, "rating"},
	} {
		got := ParseSort(tt.value, tt.unchosen)
		if got != tt.want || got.Param() != tt.param {
			t.Errorf("ParseSort(%q, %q) = %q with param %q, want %q and %q", tt.value, tt.unchosen, got, got.Param(), tt.want, tt.param)
		}
	}
}
