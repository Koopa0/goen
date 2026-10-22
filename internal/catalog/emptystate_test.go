package catalog

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/ui/frame"
)

// The cart's row of this test is internal/cart's TestPageEmptyIllustration,
// the campaign's internal/discount/campaign's TestPageEmptyIllustration.
func TestEmptyStatesShowTheirIllustrationAsDecoration(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		page  string
		asset string
	}{
		"category":        {renderToString(t, Listing(frame.Page{}, ListingView{Slug: "phones"}, nil, nil)), assets.EmptySearchImage},
		"search prompt":   {renderToString(t, Search(frame.Page{}, SearchView{})), assets.EmptySearchImage},
		"search no match": {renderToString(t, Search(frame.Page{}, SearchView{Query: "zzz"})), assets.EmptySearchImage},
	} {
		want := `<img class="goen-empty__art" src="` + assets.URL(tt.asset) + `" alt="" width="128" height="96"`
		if !strings.Contains(tt.page, want) {
			t.Errorf("%s empty state omits %s", name, want)
		}
	}
}
