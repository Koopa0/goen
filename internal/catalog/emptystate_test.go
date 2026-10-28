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
	for name, page := range map[string]string{
		"category":        renderToString(t, Listing(frame.Page{}, ListingView{Slug: "phones"}, nil, nil)),
		"search prompt":   renderToString(t, Search(frame.Page{}, SearchView{})),
		"search no match": renderToString(t, Search(frame.Page{}, SearchView{Query: "zzz"})),
		"compare one":     renderToString(t, Compare(frame.Page{}, compareOf("pixelight-9-pro"))),
	} {
		_, rest, ok := strings.Cut(page, `class="ui-empty">`)
		if !ok {
			t.Errorf("%s renders no ui-empty box", name)
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
