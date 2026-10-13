package pages

import "testing"

func TestEmptyListingAndSearchDependOnTotalMatches(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		total    int64
		products []ProductTile
		want     bool
	}{
		{"past last page", 3, nil, false},
		{"no matches", 0, nil, true},
		{"populated page", 1, []ProductTile{{Slug: "match"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := (ListingView{Total: tc.total, Products: tc.products}).Empty(); got != tc.want {
				t.Errorf("listing Empty = %v, want %v", got, tc.want)
			}
			if got := (SearchView{Query: "match", Total: tc.total, Products: tc.products}).Empty(); got != tc.want {
				t.Errorf("search Empty = %v, want %v", got, tc.want)
			}
		})
	}
	if (SearchView{}).Empty() {
		t.Error("an unsubmitted search is empty")
	}
}
