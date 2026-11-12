package catalog

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/ui/frame"
)

// TestListingEmptyStatesHaveNoPicture holds that the empty pages of the listing, the
// search and the comparison say so in words alone. The cart's row of this test
// is internal/cart's TestPageEmptyHasNoPicture, the campaign's
// internal/discount/campaign's TestPageEmptyHasNoPicture.
func TestListingEmptyStatesHaveNoPicture(t *testing.T) {
	t.Parallel()
	// The search says it in its head; what follows the head, the departments'
	// photographs and the newest products, is the way on, not the empty state.
	for name, tc := range map[string]struct{ page, box string }{
		"category":        {renderToString(t, Listing(frame.Page{}, ListingView{Slug: "phones"}, nil, nil)), `class="ui-empty goen-listing__empty">`},
		"search prompt":   {renderToString(t, Search(frame.Page{}, SearchView{})), `class="goen-pagehead goen-pagehead--search">`},
		"search no match": {renderToString(t, Search(frame.Page{}, SearchView{Query: "zzz"})), `class="goen-pagehead goen-pagehead--search">`},
		"compare one":     {renderToString(t, Compare(frame.Page{}, compareOf("pixelight-9-pro"))), `class="ui-empty">`},
	} {
		_, rest, ok := strings.Cut(tc.page, tc.box)
		if !ok {
			t.Errorf("%s renders no %s box", name, tc.box)
			continue
		}
		box, _, _ := strings.Cut(rest, `</div>`)
		for _, picture := range []string{`<img`, `goen-medallion`} {
			if strings.Contains(box, picture) {
				t.Errorf("%s empty state draws %q", name, picture)
			}
		}
	}
}
