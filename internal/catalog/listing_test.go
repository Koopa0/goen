package catalog

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	nethtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestListingMetaProductMetaNav holds that a listing or product page marks the
// category the shopper is in, through the Nav of its page meta, and a page
// outside the tree marks nothing.
//
// It holds frame.Page.Nav, which drives the header's aria-current="page". It is
// set where the page's chrome is BUILT, not by each handler: a field every
// caller must remember to fill is a field that goes unfilled.
func TestListingMetaProductMetaNav(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	tests := []struct {
		name string
		view ListingView
		want string
	}{
		{
			name: "a root category marks itself",
			view: ListingView{Slug: "phones", Name: "手機"},
			want: "phones",
		},
		{
			name: "a child marks its ROOT, which is what the header shows",
			view: ListingView{
				Slug: "chargers", Name: "充電與線材",
				Crumbs: []Crumb{{Slug: "accessories", Name: "周邊配件"}},
			},
			want: "accessories",
		},
		{
			name: "every product marks no department",
			view: ListingView{Name: "全館選物"},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ListingMeta(ctx, &tt.view).Nav; got != tt.want {
				t.Errorf("ListingMeta(%q).Nav = %q, want %q", tt.view.Slug, got, tt.want)
			}
		})
	}

	// A PDP marks the category its product is in, for the same reason: somebody who
	// followed 手機 → a phone should still see 手機 marked.
	pdp := ProductView{
		Name: "Pixelight 9 Pro", Brand: "Pixelight",
		CategorySlug: "chargers",
		Crumbs:       []Crumb{{Slug: "accessories", Name: "周邊配件"}},
	}
	if got := ProductMeta(&pdp).Nav; got != "accessories" {
		t.Errorf("ProductMeta().Nav = %q, want accessories", got)
	}

	// And a page outside the tree marks nothing. current() is false for an empty
	// slug precisely so an unrelated page never highlights a category.
	if got := (ProductMeta(&ProductView{Name: "x", Brand: "y"})).Nav; got != "" {
		t.Errorf("a product with no category marks %q", got)
	}
}

func parse(t *testing.T, page string) *nethtml.Node {
	t.Helper()
	doc, err := nethtml.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func byID(doc *nethtml.Node, id string) *nethtml.Node {
	return findDescendant(doc, func(n *nethtml.Node) bool { return n.Type == nethtml.ElementNode && attrValue(n, "id") == id })
}

// TestListingDrawer holds that the filters are one panel in the column beside
// the results, with an empty modal drawer and the button that opens it.
//
// goen.js carries the panel into the drawer and back, so the page must hold
// exactly one form, one set of ids and a closed, empty <dialog>; the button
// names what it controls and that it opens a dialog.
func TestListingDrawer(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{
		Slug: "audio", Name: "耳機與音響",
		Shelf: Shelf{
			Path: "/c/audio", Total: 1, Products: []tile.Tile{{Slug: "nimbus-buds-pro", Name: "Nimbus Buds Pro", PriceCents: 399000}},
			Facets: []FacetGroup{{Label: i18n.T(ctx, i18n.KeyFacetBrand), Options: []FacetOption{{Value: "nimbus", Label: "Nimbus", Count: 1}}}},
		},
	}
	page := renderComponent(t, ctx, Listing(ListingMeta(ctx, &view), &view))
	doc := parse(t, page)

	drawer := byID(doc, "filters-drawer")
	if drawer == nil || drawer.Data != "dialog" {
		t.Fatal("the listing has no <dialog id=filters-drawer>")
	}
	if drawer.FirstChild != nil {
		t.Error("the drawer is drawn with something in it; the panel is carried there when it opens")
	}
	if attrValue(drawer, "aria-labelledby") != "facets-title" || byID(doc, "facets-title") == nil {
		t.Error("the drawer is not named by the panel's heading")
	}
	for _, attr := range drawer.Attr {
		if attr.Key == "open" {
			t.Error("the drawer is open before its button is pressed")
		}
	}
	opener := findDescendant(doc, func(n *nethtml.Node) bool { return n.Data == "button" && hasClass(n, "goen-shelf__filter") })
	if opener == nil || attrValue(opener, "aria-controls") != "filters-drawer" || attrValue(opener, "aria-haspopup") != "dialog" {
		t.Fatal("no button controls the drawer as a dialog")
	}
	if got := strings.Count(page, `data-facets-panel`); got != 1 {
		t.Errorf("%d filter panels, want the one goen.js moves", got)
	}
	if got := strings.Count(page, `<form class="goen-filters"`); got != 1 {
		t.Errorf("%d filter forms, want one", got)
	}
	column := findDescendant(doc, func(n *nethtml.Node) bool { return hasClass(n, "goen-facets") })
	if column == nil || findDescendant(column, func(n *nethtml.Node) bool { return attrValue(n, "id") == "listing-filters" }) == nil {
		t.Error("the form is not in the filters' column")
	}

	// The sort is in the head, outside the form, and belongs to it by name.
	form := byID(doc, "listing-filters")
	sort := byID(doc, "sort")
	if sort == nil || sort.Data != "select" || attrValue(sort, "form") != "listing-filters" {
		t.Fatal("the sort is not a select that belongs to the filter form")
	}
	if findDescendant(form, func(n *nethtml.Node) bool { return n == sort }) != nil {
		t.Error("the sort is inside the form's panel, which a phone folds away")
	}
}

// TestListingControls holds the shelf's controls: a GET form whose groups are
// fieldsets named by their legends, a price band and two switches as choices,
// and the next page as a link.
func TestListingControls(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{
		Slug: "audio", Name: "耳機與音響",
		Shelf: Shelf{
			Path: "/c/audio", Total: 45, Page: 1, PageSize: 20, Filters: Filters{InStockOnly: true},
			Products: []tile.Tile{{Slug: "nimbus-buds-pro", Name: "Nimbus Buds Pro", PriceCents: 399000}},
			Facets:   []FacetGroup{{Label: i18n.T(ctx, i18n.KeyFacetBrand), Options: []FacetOption{{Value: "nimbus", Label: "Nimbus", Count: 1}}}},
			Prices:   []PriceOption{{Count: 45, Selected: true}, {Band: priceBands[0], Count: 12}},
		},
	}
	html := renderComponent(t, ctx, Listing(ListingMeta(ctx, &view), &view))

	_, form, ok := strings.Cut(html, `<form class="goen-filters"`)
	if !ok {
		t.Fatal("the listing has no filter form")
	}
	form, _, _ = strings.Cut(form, `</form>`)
	for _, want := range []string{
		`method="get"`, `action="/c/audio#listing-results"`,
		`<legend class="goen-facets__legend">品牌</legend>`,
		`<input type="radio" name="price" value="" checked>`,
		`<input type="radio" name="price" value="under-1000">`,
		`<span class="goen-filters__label">未滿 NT$1,000</span> <span class="goen-filters__count" id="price-count-under-1000">12</span>`,
		`<input type="checkbox" name="in_stock" value="1" class="goen-check" checked>`,
		`<input type="checkbox" name="sale" value="1" class="goen-check">`,
	} {
		if !strings.Contains(form, want) {
			t.Errorf("the filter form lacks %q", want)
		}
	}

	more := `<a class="goen-btn goen-btn--outline" href="/c/audio?in_stock=1&amp;page=2" rel="next">` + i18n.T(ctx, i18n.KeyShowMore) + `</a>`
	if !strings.Contains(html, more) {
		t.Errorf("the next page is not a link to ?page=2 that keeps the filter:\n%s", html)
	}
	if !strings.Contains(html, `<progress class="goen-pager__progress" value="1" max="3"`) {
		t.Error("the pager draws no progress line")
	}

	last := view
	last.Page = 3
	if got := renderComponent(t, ctx, Listing(ListingMeta(ctx, &last), &last)); strings.Contains(got, `rel="next"`) {
		t.Error("the last page offers a next page")
	}
}

// TestListingEmptyToolbar holds that an empty listing has no count, and keeps
// its sort.
//
// The empty state carries the message, and the count's box stays empty so a
// swap can fill it; app.css hides the sort beside an empty count. The sort is
// drawn either way and never swapped, so a filter that empties the shelf and a
// failed update both leave the select, and the focus on it, where they were.
func TestListingEmptyToolbar(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	empty := ListingView{Slug: "audio", Name: "耳機與音響", Shelf: Shelf{Path: "/c/audio", Filters: Filters{InStockOnly: true}}}
	html := renderComponent(t, ctx, Listing(ListingMeta(ctx, &empty), &empty))

	if !strings.Contains(html, `<p class="goen-listing__count" id="listing-count" aria-hidden="true"></p>`) {
		t.Error("an empty listing prints a count in the toolbar, or its box is gone so a swap has nothing to fill")
	}
	if !strings.Contains(html, `<div id="listing-sort" class="goen-filters__sort"><div class="goen-filters__field">`) {
		t.Error("the sort is not drawn on an empty listing, so the first result would have none")
	}
	if strings.Contains(html, `#listing-sort`) {
		t.Error("the sort is swapped with the results and loses its node and its focus")
	}
	if !strings.Contains(html, `<a class="goen-listing__all" href="/c/audio">清除所有篩選條件</a>`) {
		t.Error("the empty state does not lead back to the unfiltered shelf")
	}

	full := ListingView{Slug: "audio", Name: "耳機與音響", Shelf: Shelf{
		Path: "/c/audio", Total: 1, Products: []tile.Tile{{Slug: "nimbus-buds-pro", Name: "Nimbus Buds Pro", PriceCents: 399000}},
	}}
	got := renderComponent(t, ctx, Listing(ListingMeta(ctx, &full), &full))
	if !strings.Contains(got, `id="listing-count" aria-hidden="true">共 1 件商品</p>`) || !strings.Contains(got, `id="sort"`) {
		t.Error("a listing with products lacks its count or sort")
	}
}

// TestSearchCountInHead holds that a search with matches says how many under
// its term, where a category says its count.
func TestSearchCountInHead(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := SearchView{Query: "nimbus", Matches: 3, Shelf: Shelf{
		Path: "/search", Searching: true, Total: 3, Page: 1, PageSize: 20,
		Products: []tile.Tile{{Slug: "nimbus-buds-pro", Name: "Nimbus Buds Pro", PriceCents: 399000}},
	}}
	html := renderComponent(t, ctx, Search(frame.Page{}, &view))
	for _, want := range []string{
		`<h1 class="goen-pagehead__title">搜尋「nimbus」</h1>`,
		`<p class="goen-listing__count" id="listing-count" aria-hidden="true">找到 3 件商品</p>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the search head lacks %s:\n%s", want, html)
		}
	}
}

// TestListingFocusesResults holds that a filtered listing puts keyboard focus
// on its results.
//
// It holds where a filter submission should leave keyboard focus: on the
// results region, not back at the page top; and the chips that say what is on
// stand above the results, in their column, after the filters.
func TestListingFocusesResults(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{Slug: "audio", Name: "耳機與音響", Shelf: Shelf{
		Path: "/c/audio", Total: 1, Filters: Filters{InStockOnly: true},
		Products: []tile.Tile{{Slug: "nimbus-buds-pro", Name: "Nimbus Buds Pro", PriceCents: 399000}},
	}}
	html := renderComponent(t, ctx, Listing(ListingMeta(ctx, &view), &view))

	if !strings.Contains(html, `id="listing-results" tabindex="-1" autofocus`) {
		t.Error("a filtered listing does not autofocus the results region")
	}
	idxFacets := strings.Index(html, `class="goen-facets"`)
	idxMain := strings.Index(html, `class="goen-listing__main"`)
	idxResults := strings.Index(html, `id="listing-results"`)
	if idxFacets < 0 || idxMain < idxFacets || idxResults < idxMain {
		t.Fatal("the filters, the results column and the results do not come in that order")
	}
	if !strings.Contains(html[idxMain:idxResults], `class="goen-filters__applied"`) {
		t.Error("applied-filter summary is not in the results column, above the results")
	}
	if !strings.Contains(html, i18n.T(ctx, i18n.KeyFacetInStock)) || !strings.Contains(html, i18n.T(ctx, i18n.KeyClearFilters)) {
		t.Error("the applied summary does not name the stock filter or offer a reset")
	}

	unfiltered := view
	unfiltered.Filters = Filters{}
	if strings.Contains(renderComponent(t, ctx, Listing(ListingMeta(ctx, &unfiltered), &unfiltered)), "autofocus") {
		t.Error("an unfiltered listing takes focus from the top of the page")
	}
}

// TestListingCompareForm holds that a comparison can be built from a listing.
//
// It holds the one path into /compare. The comparison lives in the URL and
// nowhere else, so every link into it has to carry the set. The listing is
// where somebody chooses between candidates, so the listing is where the set is
// built — a GET form, because a comparison writes nothing.
func TestListingCompareForm(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{Slug: "phones", Name: "手機", Shelf: Shelf{Path: "/c/phones", Total: 2, Products: []tile.Tile{
		{Slug: "pixelight-9-pro", Name: "Pixelight 9 Pro", Comparable: true},
		{Slug: "aurora-fold-2", Name: "Aurora Fold 2", Comparable: true},
	}}}
	for _, v := range []View{ViewGrid, ViewList} {
		view.View = v
		html := renderComponent(t, ctx, Listing(ListingMeta(ctx, &view), &view))

		// The form's action and method, not merely a checkbox: a checkbox that
		// submits to the listing filters the listing.
		if !strings.Contains(html, `method="get" action="/compare"`) {
			t.Errorf("%q view: the listing carries no GET form to /compare", v)
		}
		for _, slug := range []string{"pixelight-9-pro", "aurora-fold-2"} {
			if !strings.Contains(html, `<input type="checkbox" name="p" value="`+slug+`" class="goen-check" form="compare-pick">`) {
				t.Errorf("%q view: no compare checkbox for %q; one product can never become two", v, slug)
			}
		}
	}

	// The set survives a click back out of the table, or building a third
	// column means starting from one again.
	cmp := CompareView{Products: []CompareProduct{{Slug: "a"}, {Slug: "b"}}}
	if got, want := cmp.ProductHref("a"), "/p/a?p=a&p=b"; got != want {
		t.Errorf("ProductHref(a) = %q, want %q", got, want)
	}
}

// TestListingPriceFrom holds that a cheapest price says it is the cheapest: a
// listing tile and an unchosen product page both mark the price as a lowest one
// only where it is.
func TestListingPriceFrom(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	marked := fmt.Sprintf(i18n.T(ctx, i18n.KeyFromPrice), "NT$25,900")

	for _, v := range []View{ViewGrid, ViewList} {
		one := ListingView{Slug: "phones", Name: "手機", Shelf: Shelf{Total: 1, View: v, Products: []tile.Tile{
			{Slug: "solo", Name: "Solo", PriceCents: 2590000},
		}}}
		if got := renderComponent(t, ctx, Listing(ListingMeta(ctx, &one), &one)); strings.Contains(got, marked) {
			t.Errorf("%q view: a single-priced product renders %q", v, marked)
		}
		many := ListingView{Slug: "phones", Name: "手機", Shelf: Shelf{Total: 1, View: v, Products: []tile.Tile{
			{Slug: "spread", Name: "Spread", PriceCents: 2590000, PriceVaries: true},
		}}}
		if got := renderComponent(t, ctx, Listing(ListingMeta(ctx, &many), &many)); !strings.Contains(got, marked) {
			t.Errorf("%q view: a product spanning prices states NT$25,900 as its price, unmarked", v)
		}
	}

	// And the PDP marks it only while the choice is still open: once a variant
	// is resolved the price is that variant's, whatever else the product sells.
	tests := []struct {
		name          string
		varies, exact bool
		want          bool
	}{
		{name: "nothing chosen, dearer ones exist", varies: true, exact: false, want: true},
		{name: "chosen: this is its price", varies: true, exact: true, want: false},
		{name: "nothing chosen, one price only", varies: false, exact: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := ProductView{PriceVaries: tt.varies, Exact: tt.exact}
			if got := v.PriceFrom(); got != tt.want {
				t.Errorf("PriceFrom(varies=%v, exact=%v) = %v, want %v",
					tt.varies, tt.exact, got, tt.want)
			}
		})
	}
}

// TestListingEagerFirstRows holds that the first rows of a listing load their
// photographs eagerly, in the grid and in the list.
//
// The first two rows' photographs are the page's first paint, so they must not
// wait for layout; everything past them stays lazy.
func TestListingEagerFirstRows(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	tiles := make([]tile.Tile, 0, 10)
	for i := range 10 {
		slug := fmt.Sprintf("p%d", i)
		tiles = append(tiles, tile.Tile{Slug: slug, Name: slug, PriceCents: 1000, ImageURL: "/img/" + slug + ".webp", ImageAlt: slug})
	}
	for _, v := range []View{ViewGrid, ViewList} {
		view := ListingView{Slug: "audio", Name: "Audio", Shelf: Shelf{Total: int64(len(tiles)), Products: tiles, View: v}}
		html := renderComponent(t, ctx, Listing(ListingMeta(ctx, &view), &view))
		if got := strings.Count(html, `loading="lazy"`); got != 2 {
			t.Errorf("%q view: %d lazy photographs, want 2 (the tiles after the first eight)", v, got)
		}
		if got := strings.Count(html, `fetchpriority="high"`); got != 1 {
			t.Errorf("%q view: %d high-priority photographs, want 1 (the first tile only)", v, got)
		}
	}
	if tiles[0].Eager {
		t.Error("FirstRowEager changed the caller's slice")
	}
}

func shelf(n int) []tile.Tile {
	tiles := make([]tile.Tile, 0, n)
	for i := range n {
		slug := fmt.Sprintf("p%d", i)
		tiles = append(tiles, tile.Tile{Slug: slug, Name: slug, PriceCents: 1000, Comparable: true, ImageURL: "/img/" + slug + ".webp", ImageAlt: slug})
	}
	return tiles
}

// TestListingOneGrid holds that a long shelf is one grid of every product:
// nothing is lifted into a lead row.
func TestListingOneGrid(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for name, view := range map[string]ListingView{
		"eight products":   {Slug: "a", Name: "A", Shelf: Shelf{Products: shelf(8), Total: 8, Page: 1}},
		"long, unfiltered": {Slug: "a", Name: "A", Shelf: Shelf{Products: shelf(12), Total: 30, Page: 1, PageSize: 24}},
		"filtered":         {Slug: "a", Name: "A", Shelf: Shelf{Products: shelf(12), Total: 30, Page: 1, Filters: Filters{InStockOnly: true}}},
		"the second page":  {Slug: "a", Name: "A", Shelf: Shelf{Products: shelf(12), Total: 30, Page: 2}},
	} {
		html := renderComponent(t, ctx, Listing(ListingMeta(ctx, &view), &view))
		if strings.Contains(html, "goen-featured") || strings.Contains(html, "精選商品") {
			t.Errorf("%s: has a lead row", name)
		}
		if got := strings.Count(html, `class="goen-tile__cell"`); got != len(view.Products) {
			t.Errorf("%s: the grid holds %d cards, want all %d", name, got, len(view.Products))
		}
	}
}

// TestListingSpecList holds the list: a row a product whose one link is its
// photograph, brand and name, its specifications as capsules each read with
// its name, and whether it can be bought; and the view's two links, the one on
// screen marked, each keeping the filters.
func TestListingSpecList(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{Slug: "phones", Name: "手機", Shelf: Shelf{
		Path: "/c/phones", Total: 2, View: ViewList, Filters: Filters{InStockOnly: true},
		Products: []tile.Tile{
			{Slug: "pixelight-9", Name: "Pixelight 9", Brand: "Pixelight", PriceCents: 2590000, InStock: true},
			{Slug: "aurora-edge-7", Name: "Aurora Edge 7", Brand: "Aurora", PriceCents: 1990000},
		},
		Specs: map[string][]Spec{"pixelight-9": {{Label: "螢幕", Value: `6.3" OLED`}, {Label: "電池", Value: "4700mAh"}}},
	}}
	html := renderComponent(t, ctx, Listing(ListingMeta(ctx, &view), &view))
	if strings.Contains(html, "goen-tiles__grid") {
		t.Error("the list view draws the grid")
	}
	for _, want := range []string{
		`<ul class="goen-speclist" role="list">`,
		`<a class="goen-specrow__link" href="/p/pixelight-9">`,
		`<span class="goen-specrow__brand">Pixelight</span><h3 class="goen-specrow__name">Pixelight 9</h3></span></a>`,
		`<li class="goen-specrow__spec"><span class="goen-sr-only">螢幕：</span>6.3&#34; OLED</li>`,
		`<p class="goen-specrow__stock">現貨供應</p>`,
		`<p class="goen-specrow__stock goen-specrow__stock--out">已售完</p>`,
		`<input type="hidden" name="view" value="list">`,
		`<a class="goen-viewswitch__link" href="/c/phones?in_stock=1">`,
		`<a class="goen-viewswitch__link" href="/c/phones?in_stock=1&amp;view=list" aria-current="true">`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the list lacks %s:\n%s", want, html)
		}
	}
	if got := strings.Count(html, `class="goen-specrow__specs"`); got != 1 {
		t.Errorf("%d rows draw capsules, want only the product that lists specifications", got)
	}

	grid := view
	grid.View = ViewGrid
	if got := renderComponent(t, ctx, Listing(ListingMeta(ctx, &grid), &grid)); strings.Contains(got, `name="view"`) || !strings.Contains(got, `href="/c/phones?in_stock=1" aria-current="true"`) {
		t.Error("the grid carries the list's field, or does not mark itself the view on screen")
	}
}

// TestListingTree holds the catalogue's tree beside a category: every product
// first, the departments, the page's own marked as the page and its department
// as the one it is in; on a phone the sub-categories are a row under the name.
// The head draws no photograph: the products are the page's pictures.
func TestListingTree(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := ListingView{
		Slug: "cases", Name: "Cases", Crumbs: []Crumb{{Slug: "accessories", Name: "Accessories"}},
		Theme: &Theme{Photo: widget.Photo{URL: "/img/d.webp", Alt: "a desk"}},
		Shelf: Shelf{
			Path: "/c/cases",
			Departments: []TreeLink{
				{Name: "All products", Href: "/shop", Count: 9},
				{Slug: "accessories", Name: "Accessories", Href: "/c/accessories", Count: 9, Open: true},
			},
			Subs:      []TreeLink{{Slug: "chargers", Name: "Chargers", Href: "/c/chargers", Count: 4}, {Slug: "cases", Name: "Cases", Href: "/c/cases", Count: 5, Current: true}},
			SubsTitle: "Accessories",
		},
	}
	html := renderComponent(t, ctx, Listing(ListingMeta(ctx, &view), &view))
	for _, want := range []string{
		`<a class="goen-facets__link" href="/shop"><span class="goen-facets__name">All products</span> <span class="goen-facets__count">9</span></a>`,
		`<a class="goen-facets__link" href="/c/accessories" aria-current="true">`,
		`<h3 class="goen-facets__legend" id="facets-subs">Accessories</h3>`,
		`<a class="goen-facets__link" href="/c/cases" aria-current="page">`,
		`<a class="goen-subindex__link" href="/c/chargers">Chargers</a><a class="goen-subindex__link" href="/c/cases" aria-current="page">Cases</a></nav>`,
		`<h1 class="goen-pagehead__title">Cases</h1>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the tree lacks %s:\n%s", want, html)
		}
	}
	if strings.Contains(html, "/img/d.webp") || strings.Contains(html, `rel="expect"`) {
		t.Error("the head draws the department photograph, or holds the first paint for it")
	}

	only := ListingView{Slug: "tea", Name: "Tea", Shelf: Shelf{Subs: []TreeLink{{Slug: "tea", Name: "Tea", Href: "/c/tea", Current: true}}}}
	if h := renderComponent(t, ctx, Listing(ListingMeta(ctx, &only), &only)); strings.Contains(h, "goen-subindex") {
		t.Error("a head with a single sub-category draws it as a choice")
	}
}

// TestSearchEmptyDepartments holds that a search that matched nothing suggests
// the departments, which the header already lists.
func TestSearchEmptyDepartments(t *testing.T) {
	t.Parallel()
	ctx := frame.WithTopNav(i18n.WithLocale(t.Context(), i18n.ZhHant), []frame.NavItem{
		{Slug: "tech", Name: "3C", Href: "/c/tech"}, {Slug: "fashion", Name: "服飾", Href: "/c/fashion"},
	})
	view := SearchView{Query: "zzqxv"}
	html := renderComponent(t, ctx, Search(frame.Page{}, &view))
	for _, want := range []string{
		`<h2 class="goen-search__heading" id="search-departments">館別</h2>`,
		`<h3 class="goen-deptindex__name"><a href="/c/tech">3C</a></h3>`,
		`<a href="/c/fashion">服飾</a>`,
		`<h1 class="goen-pagehead__title">找不到「zzqxv」</h1>`,
		`<p class="goen-search__lead">試試更短的關鍵字，或從下面的館別開始逛。</p>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the empty search lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "goen-facets") {
		t.Error("a search that matched nothing offers filters for it")
	}
}

// TestListingFiltersApply holds that the filters apply themselves: a changed
// control, the sort included, fetches the address the form names and swaps
// the results, the chips, the counts and the announced total.
func TestListingFiltersApply(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := ListingView{Slug: "audio", Name: "Audio", Shelf: Shelf{Path: "/c/audio", Products: shelf(2), Total: 2, Filters: Filters{InStockOnly: true}}}
	html := renderComponent(t, ctx, Listing(ListingMeta(ctx, &view), &view))

	for _, want := range []string{
		`hx-get="/c/audio"`, `hx-trigger="change delay:300ms, change delay:300ms from:body target:#sort"`, `hx-push-url="true"`,
		`hx-target="#listing-results"`,
		`hx-select-oob="#filters-applied, #listing-status:innerHTML, #listing-count:innerHTML, #listing-views:innerHTML, #filters-active:innerHTML, #facets-done:innerHTML"`,
		`id="listing-status" class="goen-sr-only" role="status" aria-live="polite"`,
		`<div id="filters-applied">`,
		`<div class="goen-viewswitch" id="listing-views" role="group" aria-label="View">`,
		`<button class="goen-facets__done goen-btn goen-btn--primary goen-btn--block" id="facets-done" type="button" data-filters-close>Show 2 products</button>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("filters lack %q", want)
		}
	}
	// Each region the response is selected into is the panel's or the head's,
	// and is drawn on an unfiltered page too, so the first filter has one to fill.
	unfiltered := ListingView{Slug: "audio", Name: "Audio", Shelf: Shelf{Path: "/c/audio"}}
	page := renderComponent(t, ctx, Listing(ListingMeta(ctx, &unfiltered), &unfiltered))
	for _, id := range []string{"filters-applied", "listing-status", "listing-count", "listing-views", "filters-active", "facets-done"} {
		if got := strings.Count(page, `id="`+id+`"`); got != 1 {
			t.Errorf("an unfiltered page has %d #%s, want one to swap into", got, id)
		}
	}
}

// TestListingPartialFocus holds that a partial listing never takes focus and
// the latest change wins.
//
// htmx focuses any [autofocus] in swapped content, so a partial render that
// carried it would pull focus off the control the shopper just changed. The
// latest change wins, and a failed update has a place to say so.
func TestListingPartialFocus(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := ListingView{Slug: "audio", Name: "Audio", Shelf: Shelf{Path: "/c/audio", Products: shelf(2), Total: 2, Filters: Filters{InStockOnly: true}}}

	whole := renderComponent(t, ctx, Listing(frame.Page{}, &view))
	if !strings.Contains(whole, `id="listing-results" tabindex="-1" autofocus`) {
		t.Error("a whole filtered page no longer focuses its results")
	}
	partial := renderComponent(t, AsPartial(ctx), Listing(frame.Page{}, &view))
	if strings.Contains(partial, "autofocus") {
		t.Errorf("a partial render carries autofocus:\n%s", partial)
	}
	for _, want := range []string{`hx-sync="this:replace"`, `hx-status:5xx="swap:none"`, `class="goen-filters__error" role="alert" hidden`} {
		if !strings.Contains(whole, want) {
			t.Errorf("missing %q", want)
		}
	}
}

// TestSearchShelf holds that a search with matches is a shelf like a
// category's: the form sends the term with every filter, its departments are a
// choice with the matches each holds, and a page of results keeps the chosen
// order in every pager link.
func TestSearchShelf(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := SearchView{Query: "pro", Matches: 60, Shelf: Shelf{
		Path: "/search", Keep: url.Values{"q": {"pro"}}, Searching: true,
		Products: shelf(2), Total: 60, Page: 1, PageSize: 24, Filters: Filters{Sort: SortPriceAsc, Department: "tech"},
		Departments: []TreeLink{{Slug: "tech", Name: "Tech", Count: 40, Current: true}, {Slug: "home", Name: "Home", Count: 20}},
	}}
	html := renderComponent(t, ctx, Search(SearchMeta(ctx, "pro"), &view))
	for _, want := range []string{
		`hx-get="/search"`, `<input type="hidden" name="q" value="pro">`,
		`<select class="ui-select" id="sort" name="sort" form="listing-filters">`,
		`<option value="">Best match</option>`, `<option value="price_asc" selected>Price, low to high</option>`,
		`<input type="radio" name="c" value=""> <span class="goen-filters__label">All results</span> <span class="goen-filters__count">60</span>`,
		`<input type="radio" name="c" value="tech" checked> <span class="goen-filters__label">Tech</span> <span class="goen-filters__count">40</span>`,
		`href="/search?c=tech&amp;page=2&amp;q=pro&amp;sort=price_asc"`,
		`<a class="ui-filterbar__clear" href="/search?q=pro">Clear all</a>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the search lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, `class="goen-facets__link"`) {
		t.Error("a search's departments are links away from the search")
	}
}

// TestSearchEmptyNewest holds that a search that matched nothing offers the
// newest products besides the departments, and no sort.
func TestSearchEmptyNewest(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	newest := shelf(2)
	html := renderComponent(t, ctx, Search(SearchMeta(ctx, "zzqxv"), &SearchView{Query: "zzqxv", Newest: newest}))
	if !strings.Contains(html, `<h2 class="goen-listing__subhead" id="search-newest">Newest products</h2>`) || !strings.Contains(html, newest[0].Name) {
		t.Errorf("the empty search does not show the newest products:\n%s", html)
	}
	if strings.Contains(html, `name="sort"`) {
		t.Error("an empty search offers a sort control with nothing to sort")
	}
}

// TestShelf_AppliedChips holds that each applied filter links to the shelf
// without it and nothing else, keeps what the page keeps and the view, and
// names what it removes, because its glyph is only a cross.
func TestShelf_AppliedChips(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	s := &Shelf{
		Path: "/search", Keep: url.Values{"q": {"pro"}}, Searching: true, View: ViewList,
		Filters: Filters{
			BrandSlugs: []string{"aurora", "nimbus"}, InStockOnly: true, OnSaleOnly: true,
			Price: priceBands[1], Department: "tech", Sort: SortRating,
		},
		Departments: []TreeLink{{Slug: "tech", Name: "Tech", Count: 3, Current: true}},
		Facets: []FacetGroup{{Label: "Brand", Options: []FacetOption{
			{Value: "aurora", Label: "Aurora", Selected: true},
			{Value: "nimbus", Label: "Nimbus", Selected: true},
		}}},
	}
	const all = "brand=aurora&brand=nimbus&c=tech&in_stock=1&price=1000-9999&q=pro&sale=1&sort=rating&view=list"
	without := func(drop string) string {
		return "/search?" + strings.Replace(all, drop+"&", "", 1)
	}
	want := map[string]string{
		"Remove “Tech”":              without("c=tech"),
		"Remove “Aurora”":            without("brand=aurora"),
		"Remove “Nimbus”":            without("brand=nimbus"),
		"Remove “In stock”":          without("in_stock=1"),
		"Remove “On sale”":           without("sale=1"),
		"Remove “NT$1,000–NT$9,999”": without("price=1000-9999"),
	}
	chips := s.AppliedChips(ctx)
	if len(chips) != len(want) {
		t.Fatalf("%d chips, want %d: %+v", len(chips), len(want), chips)
	}
	for _, c := range chips {
		if want[c.RemoveLabel] != c.Remove {
			t.Errorf("%q links to %q, want %q", c.RemoveLabel, c.Remove, want[c.RemoveLabel])
		}
	}
	if got, want := s.ResetHref(), "/search?q=pro&view=list"; got != want {
		t.Errorf("ResetHref() = %q, want %q", got, want)
	}
	if got := s.ActiveCount(); got != 6 {
		t.Errorf("ActiveCount() = %d, want 6", got)
	}
}

// TestPriceOption_Label holds that each band names its bounds as money, and
// that the bands meet without a gap or an overlap: a product priced at a
// band's edge is in exactly one band, and the one its label says.
func TestPriceOption_Label(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		band   PriceBand
		locale i18n.Locale
		want   string
	}{
		{PriceBand{}, i18n.ZhHant, "全部價格"},
		{priceBands[0], i18n.ZhHant, "未滿 NT$1,000"},
		{priceBands[0], i18n.En, "Under NT$1,000"},
		{priceBands[1], i18n.ZhHant, "NT$1,000–NT$9,999"},
		{priceBands[2], i18n.ZhHant, "NT$10,000 起"},
		{priceBands[2], i18n.En, "From NT$10,000"},
	} {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		if got := (PriceOption{Band: tt.band}).Label(ctx); got != tt.want {
			t.Errorf("Label(%q, %s) = %q, want %q", tt.band.ID, tt.locale, got, tt.want)
		}
	}
	for i := 1; i < len(priceBands); i++ {
		if below, above := priceBands[i-1], priceBands[i]; below.Max+1 != above.Min {
			t.Errorf("band %q ends at %d and %q starts at %d: they do not meet", below.ID, below.Max, above.ID, above.Min)
		}
	}
	if first, last := priceBands[0], priceBands[len(priceBands)-1]; first.Min != 0 || last.Max != 0 {
		t.Errorf("the bands do not run from nothing (%d) to no bound (%d)", first.Min, last.Max)
	}
}

// TestListingPartialCounts holds that a partial listing carries the counts the
// filters show beside each brand and each price band.
//
// The filters are not swapped, so a swapped response carries each count on its
// own; a whole-page response must not, or ids would repeat.
func TestListingPartialCounts(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := ListingView{Slug: "audio", Name: "Audio", Shelf: Shelf{
		Path: "/c/audio", Products: shelf(1), Total: 1,
		Facets: []FacetGroup{{Label: "Brand", Options: []FacetOption{{Value: "aurora", Label: "Aurora", Count: 3}}}},
		Prices: []PriceOption{{Count: 7}, {Band: priceBands[2], Count: 2}},
	}}
	partial := renderComponent(t, AsPartial(ctx), Listing(ListingMeta(ctx, &view), &view))
	for _, oob := range []string{
		`<span class="goen-filters__count" id="brand-count-aurora" hx-swap-oob="true">3</span>`,
		`<span class="goen-filters__count" id="price-count-any" hx-swap-oob="true">7</span>`,
		`<span class="goen-filters__count" id="price-count-10000-up" hx-swap-oob="true">2</span>`,
	} {
		if !strings.Contains(partial, oob) {
			t.Errorf("a partial listing lacks the out-of-band count %s", oob)
		}
	}
	if html := renderComponent(t, ctx, Listing(ListingMeta(ctx, &view), &view)); strings.Contains(html, "hx-swap-oob") {
		t.Error("a whole-page listing carries out-of-band counts")
	}
}

// TestListingActiveCount holds that the drawer's button says how many filters
// are on, read as a sentence, and nothing when none are.
func TestListingActiveCount(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := ListingView{Slug: "audio", Name: "Audio", Shelf: Shelf{
		Path: "/c/audio", Products: shelf(1), Total: 1,
		Filters: Filters{BrandSlugs: []string{"aurora", "nimbus"}, InStockOnly: true},
	}}
	html := renderComponent(t, ctx, Listing(ListingMeta(ctx, &view), &view))
	if want := `<span class="goen-shelf__active" id="filters-active"><span aria-hidden="true">3</span> <span class="goen-sr-only">3 selected</span></span>`; !strings.Contains(html, want) {
		t.Errorf("the drawer's button lacks %s:\n%s", want, html)
	}
	view.Filters = Filters{}
	if html := renderComponent(t, ctx, Listing(ListingMeta(ctx, &view), &view)); !strings.Contains(html, `<span class="goen-shelf__active" id="filters-active"></span>`) {
		t.Error("an unfiltered shelf counts filters on its drawer's button")
	}
}
