package taxonomy

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestTaxonomyRefusedParentKept: refused parent is kept even when its category no longer exists.
func TestTaxonomyRefusedParentKept(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			view := &View{Categories: []Taxon{{Slug: "books", Name: "書房"}}, Which: "categories", Draft: TaxonDraft{Parent: "removed-parent"}, Errors: map[string]string{"parent": "No such parent"}}
			doc, err := html.Parse(strings.NewReader(renderComponent(t, i18n.WithLocale(t.Context(), locale), Taxonomy(frame.Page{}, view))))
			if err != nil {
				t.Fatal(err)
			}
			chosen := ""
			for n := range doc.Descendants() {
				if n.Data != "select" || attr(n, "id") != "categories-parent" {
					continue
				}
				for option := range n.Descendants() {
					if option.Data != "option" {
						continue
					}
					if _, ok := attrPresent(option, "selected"); ok {
						chosen = attr(option, "value")
					}
				}
			}
			if chosen != "removed-parent" {
				t.Errorf("refused parent choice = %q,want removed-parent", chosen)
			}
		})
	}
}
