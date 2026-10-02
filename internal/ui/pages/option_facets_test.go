package pages

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestOptionFacetsStayInThePlainCollapsedFilterFormAndRemoveOnlyTheirOwnValue(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := ListingView{Slug: "phones", Name: "Phones", Filtered: true, InStockOnly: true,
		Query: "opt=" + url.QueryEscape("容量:256GB") + "&opt=" + url.QueryEscape("顏色:曜石黑") + "&in_stock=1",
		Facets: []FacetGroup{
			{Kind: FacetVariantOption, Name: "容量", Label: "Capacity", Options: []FacetOption{{Value: "容量:256GB", Label: "256 GB", Count: 2, Selected: true}}},
			{Kind: FacetVariantOption, Name: "顏色", Label: "Colour", Options: []FacetOption{{Value: "顏色:曜石黑", Label: "Obsidian", Count: 0, Selected: true}}},
		},
	}
	markup := renderComponent(t, ctx, listingFilters(view))
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		attrs := map[string]string{}
		for _, a := range n.Attr {
			attrs[a.Key] = a.Val
		}
		if n.Data == "input" && attrs["name"] == "opt" {
			count++
			if attrs["type"] != "checkbox" {
				t.Error("option control is not a native checkbox")
			}
			if !insideFacetGetForm(n) {
				t.Error("option control is outside the GET form")
			}
		}
		if n.Data == "details" && attrs["class"] == "goen-filters__shell" {
			if _, open := attrs["open"]; open {
				t.Error("mobile filter panel no longer starts collapsed")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	if count != 2 || !strings.Contains(markup, "Capacity") || !strings.Contains(markup, "Obsidian") {
		t.Error("translated option facets missing")
	}
	chips := view.AppliedChips(ctx)
	if len(chips) != 3 {
		t.Fatalf("chips = %v", chips)
	}
	for _, chip := range chips[:2] {
		u, parseErr := url.Parse(chip.Remove)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if len(u.Query()["opt"]) != 1 || u.Query().Get("in_stock") != "1" {
			t.Errorf("chip drops other filters: %q", chip.Remove)
		}
	}
	a := FacetGroup{Kind: FacetVariantOption, Name: "axis with spaces:and punctuation"}
	if strings.ContainsAny(a.CountID(0), " :") || a.CountID(0) == a.CountID(1) {
		t.Error("facet IDs are not CSS-safe and distinct")
	}
}

func insideFacetGetForm(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Data != "form" {
			continue
		}
		for _, a := range p.Attr {
			if a.Key == "method" && a.Val == "get" {
				return true
			}
		}
	}
	return false
}
