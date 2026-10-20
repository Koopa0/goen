package catalog

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestProductViewsOmitAnAbsentBrand(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, brand := range []string{"", "Maker"} {
			view := pages.ProductView{Slug: "generic", Name: "Generic", Brand: brand, VariantID: "generic-variant"}
			for name, tc := range map[string]struct {
				component templ.Component
				class     string
			}{
				"product": {component: pages.Product(layouts.Page{Title: "Generic"}, &view), class: "goen-pdp__brand"},
				"tile":    {component: pages.Tile(pages.ProductTile{Slug: "generic", Name: "Generic", Brand: brand}), class: "goen-tile__brand"},
				"compare": {component: Compare(layouts.Page{Title: "Compare"}, CompareView{Products: []CompareProduct{
					{Slug: "a", Name: "A", Brand: brand}, {Slug: "b", Name: "B", Brand: brand},
				}}), class: "goen-compare__brand"},
				"suggestions": {component: compareSuggestions(CompareView{Suggestions: []pages.ProductTile{{Slug: "generic", Name: "Generic", Brand: brand}}}), class: "goen-compare__brand"},
			} {
				t.Run(string(locale)+"/"+name+"/"+brand, func(t *testing.T) {
					var body strings.Builder
					if err := tc.component.Render(i18n.WithLocale(t.Context(), locale), &body); err != nil {
						t.Fatal(err)
					}
					doc, err := html.Parse(strings.NewReader(body.String()))
					if err != nil {
						t.Fatal(err)
					}
					element := findDescendant(doc, func(n *html.Node) bool {
						if n.Type != html.ElementNode {
							return false
						}
						for _, attr := range n.Attr {
							if attr.Key == "class" && attr.Val == tc.class {
								return true
							}
						}
						return false
					})
					if (element != nil) != (brand != "") {
						t.Errorf("brand %q: brand element present = %t", brand, element != nil)
					}
				})
			}
		}
	}
}

func TestAnUnbrandedComparisonCandidateHasNoBrandSeparator(t *testing.T) {
	view := CompareView{Candidates: []CompareCandidate{{Slug: "generic", Name: "Generic"}}}
	body := renderToString(t, comparePicker(view))
	if strings.Contains(body, "·") {
		t.Error("an absent brand still left a separator beside the candidate name")
	}
}
