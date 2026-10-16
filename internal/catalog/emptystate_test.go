package catalog

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// From TestEmptyStatesShowTheirIllustrationAsDecoration, which keeps the cart's row.
func TestCatalogEmptyStatesShowTheirIllustrationAsDecoration(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		page  string
		asset string
	}{
		"campaign":        {renderToString(t, Campaign(layouts.Page{}, CampaignView{Title: "x"})), assets.EmptyCampaignImage},
		"category":        {renderToString(t, Listing(layouts.Page{}, ListingView{Slug: "phones"}, nil, nil)), assets.EmptySearchImage},
		"search prompt":   {renderToString(t, Search(layouts.Page{}, SearchView{})), assets.EmptySearchImage},
		"search no match": {renderToString(t, Search(layouts.Page{}, SearchView{Query: "zzz"})), assets.EmptySearchImage},
	} {
		want := `<img class="goen-empty__art" src="` + assets.URL(tt.asset) + `" alt="" width="128" height="96"`
		if !strings.Contains(tt.page, want) {
			t.Errorf("%s empty state omits %s", name, want)
		}
	}
}
