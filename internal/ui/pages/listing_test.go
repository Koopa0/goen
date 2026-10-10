package pages

import (
	"fmt"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestThePageMarksTheCategoryYouAreIn holds layouts.Page.Nav, which drives the
// header's aria-current="page". It is set where the page's
// chrome is BUILT, not by each handler: a field every caller must remember to
// fill is a field that goes unfilled.
func TestThePageMarksTheCategoryYouAreIn(t *testing.T) {
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ListingMeta(ctx, tt.view).Nav; got != tt.want {
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

// TestMobileFiltersStayCollapsedWithoutScript holds the category disclosure: a
// phone visitor must see products before the full filter form, and the shell is
// a native <details> so it works when the enhancement file is deleted.
func TestMobileFiltersStayCollapsedWithoutScript(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{
		Slug: "audio", Name: "耳機與音響",
		Products: []ProductTile{{Slug: "nimbus-buds-pro", Name: "Nimbus Buds Pro", PriceCents: 399000}},
		Facets:   []FacetGroup{{Label: i18n.T(ctx, i18n.KeyFacetBrand), Options: []FacetOption{{Value: "nimbus", Label: "Nimbus", Count: 1}}}},
	}
	html := renderToString(t, Listing(ListingMeta(ctx, view), view, nil, nil))

	if !strings.Contains(html, `<div class="goen-listing__filters">`) {
		t.Error("the filter rail is not grouped into one layout column")
	}
	if !strings.Contains(html, `<details class="goen-filters__shell">`) {
		t.Error("the listing carries no mobile filter disclosure")
	}
	if !strings.Contains(html, `class="goen-filters__shell-summary"`) {
		t.Error("the disclosure has no labelled summary control")
	}
	if strings.Contains(html, `<details class="goen-filters__shell" open`) {
		t.Error("the filter shell is open by default")
	}
	if !strings.Contains(html, `action="/c/audio#listing-results"`) {
		t.Error("the filter form does not land on the results region")
	}
}

// TestTheListingControlsWorkWithoutScript holds the three controls the shopper
// reaches for: each facet a closed disclosure, the sort a native select inside
// the GET form beside a submit button, and the next page a link.
func TestTheListingControlsWorkWithoutScript(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{
		Slug: "audio", Name: "耳機與音響", Total: 45, Page: 1, PageSize: 20, Query: "in_stock=1",
		Products: []ProductTile{{Slug: "nimbus-buds-pro", Name: "Nimbus Buds Pro", PriceCents: 399000}},
		Facets:   []FacetGroup{{Label: i18n.T(ctx, i18n.KeyFacetBrand), Options: []FacetOption{{Value: "nimbus", Label: "Nimbus", Count: 1}}}},
	}
	html := renderToString(t, Listing(ListingMeta(ctx, view), view, nil, nil))

	_, form, ok := strings.Cut(html, `<form class="goen-filters"`)
	if !ok {
		t.Fatal("the listing has no filter form")
	}
	form, _, _ = strings.Cut(form, `</form>`)
	for _, want := range []string{`method="get"`, `<select class="ui-select" id="sort" name="sort">`, `type="submit"`} {
		if !strings.Contains(form, want) {
			t.Errorf("the filter form lacks %q; the sort would need script", want)
		}
	}
	if strings.Contains(form, `class="goen-filters__group" open`) || strings.Contains(form, `data-popover open`) {
		t.Error("a facet menu is open before the shopper opens it")
	}
	if !strings.Contains(form, `<details class="goen-filters__group" name="filters" data-popover>`) {
		t.Error("a facet is not a disclosure menu")
	}

	if count := strings.Index(html, `class="goen-listing__count"`); count < 0 || count > strings.Index(html, `id="listing-results"`) {
		t.Error("the count is not in the toolbar above the results")
	}

	more := `<a class="goen-btn goen-btn--outline" href="/c/audio?in_stock=1&amp;page=2" rel="next">` + i18n.T(ctx, i18n.KeyShowMore) + `</a>`
	if !strings.Contains(html, more) {
		t.Errorf("the next page is not a link to ?page=2:\n%s", html)
	}
	if !strings.Contains(html, `<progress class="goen-pager__progress" value="1" max="3"`) {
		t.Error("the pager draws no progress line")
	}

	last := view
	last.Page = 3
	if got := renderToString(t, Listing(ListingMeta(ctx, last), last, nil, nil)); strings.Contains(got, `rel="next"`) {
		t.Error("the last page offers a next page")
	}
}

// TestAnEmptyListingHasNoCountAndNoSort: the empty state carries the message, and
// the toolbar keeps only what can still change the result.
func TestAnEmptyListingHasNoCountAndNoSort(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	empty := ListingView{Slug: "audio", Name: "耳機與音響", Filtered: true, InStockOnly: true}
	html := renderToString(t, Listing(ListingMeta(ctx, empty), empty, nil, nil))

	if strings.Contains(html, `id="sort"`) {
		t.Error("an empty listing offers a sort")
	}
	if !strings.Contains(html, `<p class="goen-listing__count" id="listing-count" aria-hidden="true"></p>`) {
		t.Error("an empty listing prints a count in the toolbar, or its box is gone so a swap has nothing to fill")
	}
	if !strings.Contains(html, `<div id="listing-sort" class="goen-filters__sort"></div>`) {
		t.Error("the sort box is not empty, or is gone so a swap has nothing to fill")
	}
	if !strings.Contains(html, `goen-filters__apply`) {
		t.Error("the apply button is gone with the sort")
	}

	full := ListingView{
		Slug: "audio", Name: "耳機與音響", Total: 1,
		Products: []ProductTile{{Slug: "nimbus-buds-pro", Name: "Nimbus Buds Pro", PriceCents: 399000}},
	}
	got := renderToString(t, Listing(ListingMeta(ctx, full), full, nil, nil))
	if !strings.Contains(got, `id="listing-count" aria-hidden="true">共 1 件商品</p>`) || !strings.Contains(got, `id="sort"`) {
		t.Error("a listing with products lacks its count or sort")
	}
}

// TestTheSearchCountSitsBesideTheSort: the head carries the query only.
func TestTheSearchCountSitsBesideTheSort(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := SearchView{
		Query: "nimbus", Total: 3, Page: 1, PageSize: 20,
		Products: []ProductTile{{Slug: "nimbus-buds-pro", Name: "Nimbus Buds Pro", PriceCents: 399000}},
	}
	html := renderComponent(t, ctx, Search(layouts.Page{}, view))

	_, form, ok := strings.Cut(html, `<form class="goen-search-sort"`)
	if !ok {
		t.Fatal("the search has no sort form")
	}
	form, _, _ = strings.Cut(form, `</form>`)
	if !strings.Contains(form, `<p class="goen-listing__count">找到 3 件商品</p>`) {
		t.Errorf("the count is not in the sort toolbar:\n%s", form)
	}
	if strings.Contains(html, "goen-pagehead__sub") {
		t.Error("the page head still carries a count line")
	}
}

// TestFilteredListingFocusesResults holds where a filter submission should
// leave keyboard focus: on the results region, not back at the page top.
func TestFilteredListingFocusesResults(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{
		Slug: "audio", Name: "耳機與音響", Filtered: true, InStockOnly: true,
		Products: []ProductTile{{Slug: "nimbus-buds-pro", Name: "Nimbus Buds Pro", PriceCents: 399000}},
	}
	html := renderToString(t, Listing(ListingMeta(ctx, view), view, nil, nil))

	if !strings.Contains(html, `id="listing-results"`) {
		t.Error("the results region has no fragment target")
	}
	if !strings.Contains(html, `id="listing-results" tabindex="-1" autofocus`) {
		t.Error("a filtered listing does not autofocus the results region")
	}
	if !strings.Contains(html, `class="goen-filters__applied"`) {
		t.Error("a filtered listing shows no applied-filter summary")
	}
	idxMain := strings.Index(html, `class="goen-listing__main"`)
	idxResults := strings.Index(html, `id="listing-results"`)
	idxRail := strings.Index(html, `class="goen-listing__filters"`)
	if idxRail < 0 || idxMain < idxRail || idxResults < idxMain {
		t.Fatal("the rail, the results column and the results do not come in that order")
	}
	if !strings.Contains(html[idxMain:idxResults], `class="goen-filters__applied"`) {
		t.Error("applied-filter summary is not in the results column, above the results")
	}
	if strings.Contains(html[idxRail:idxMain], `class="goen-filters__applied"`) {
		t.Error("applied-filter summary is still in the filter rail")
	}
	if !strings.Contains(html, i18n.T(ctx, i18n.KeyFacetInStock)) {
		t.Error("the applied summary does not name the active stock filter")
	}
	if !strings.Contains(html, i18n.T(ctx, i18n.KeyClearFilters)) {
		t.Error("the applied summary offers no reset")
	}
}

// TestAComparisonCanBeBuiltFromAListing holds the one path into /compare. The
// comparison lives in the URL and nowhere else, so every link into it has to
// carry the set. The listing is where somebody chooses between candidates, so
// the listing is where the set is built — a GET form, because a comparison
// writes nothing.
func TestAComparisonCanBeBuiltFromAListing(t *testing.T) {
	t.Parallel()

	view := ListingView{
		Slug: "phones", Name: "手機",
		Products: []ProductTile{
			{Slug: "pixelight-9-pro", Name: "Pixelight 9 Pro", Comparable: true},
			{Slug: "aurora-fold-2", Name: "Aurora Fold 2", Comparable: true},
		},
	}
	html := renderToString(t, Listing(ListingMeta(i18n.WithLocale(t.Context(), i18n.ZhHant), view), view, nil, nil))

	// The form's action and method, not merely a checkbox: a checkbox that
	// submits to the listing filters the listing.
	if !strings.Contains(html, `method="get" action="/compare"`) {
		t.Error("the listing carries no GET form to /compare")
	}
	for _, slug := range []string{"pixelight-9-pro", "aurora-fold-2"} {
		if !strings.Contains(html, `<input type="checkbox" name="p" value="`+slug+`"`) {
			t.Errorf("no compare checkbox for %q; one product can never become two", slug)
		}
	}

	// Every control needs a name, and two dozen controls called 比較 are two
	// dozen identical announcements.
	if !strings.Contains(html, `aria-label="把 Pixelight 9 Pro 加入比較"`) {
		t.Error("the checkbox does not name its product")
	}

	// And the set survives a click back out of the table, or building a third
	// column means starting from one again.
	cmp := CompareView{Products: []CompareProduct{{Slug: "a"}, {Slug: "b"}}}
	if got, want := cmp.ProductHref("a"), "/p/a?p=a&p=b"; got != want {
		t.Errorf("ProductHref(a) = %q, want %q", got, want)
	}
}

// TestACheapestPriceSaysItIsTheCheapest holds a number that reads as a promise:
// a tile and an unchosen product page both render the cheapest BUYABLE
// variant's price, so it has to be marked as the bottom of a range.
func TestACheapestPriceSaysItIsTheCheapest(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	// The whole rendered figure, not a loose word: 最低 beside a price is also
	// the price FILTER's label two columns to the left.
	marked := fmt.Sprintf(i18n.T(ctx, i18n.KeyFromPrice), "NT$25,900")

	// One price for the product: the figure IS the price, and saying "from"
	// there would be worse than saying nothing.
	one := ListingView{Slug: "phones", Name: "手機", Products: []ProductTile{
		{Slug: "solo", Name: "Solo", PriceCents: 2590000},
	}}
	if got := renderToString(t, Listing(ListingMeta(ctx, one), one, nil, nil)); strings.Contains(got, marked) {
		t.Errorf("a single-priced product renders %q", marked)
	}

	many := ListingView{Slug: "phones", Name: "手機", Products: []ProductTile{
		{Slug: "spread", Name: "Spread", PriceCents: 2590000, PriceVaries: true},
	}}
	if got := renderToString(t, Listing(ListingMeta(ctx, many), many, nil, nil)); !strings.Contains(got, marked) {
		t.Errorf("a product spanning prices states NT$25,900 as its price, unmarked")
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

// TestAProductWithNothingLeftSaysSo: the button says 已售完 when nothing is
// buyable, whether every option is gone or only the chosen combination.
func TestAProductWithNothingLeftSaysSo(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	gone := i18n.T(ctx, i18n.KeySoldOut)

	tests := []struct {
		name        string
		anySellable bool
		exact       bool
		sellable    bool
		want        bool
	}{
		{name: "nothing chosen and nothing to choose", anySellable: false, want: true},
		{
			name:        "one combination gone, others buyable",
			anySellable: true, exact: true, sellable: false, want: true,
		},
		{name: "an ordinary product", anySellable: true, exact: true, sellable: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := ProductView{
				Name: "Meridian Book 14", Brand: "Meridian", Slug: "meridian-book-14",
				SelectionOK: true, AnySellable: tt.anySellable,
				Exact: tt.exact, Sellable: tt.sellable, PriceCents: 4290000,
			}
			html := renderToString(t, Product(ProductMeta(&v), &v))
			if got := strings.Contains(html, gone); got != tt.want {
				t.Errorf("a product (anySellable=%v, exact=%v, sellable=%v) says %q = %v, want %v",
					tt.anySellable, tt.exact, tt.sellable, gone, got, tt.want)
			}
		})
	}
}

// TestAProductWithNoReviewsCanReceiveItsFirst holds a bootstrap deadlock: the
// form that writes a review lives inside the reviews section, so gating that
// section on RatingCount > 0 leaves a new product unable to receive its first.
func TestAProductWithNoReviewsCanReceiveItsFirst(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	fresh := ProductView{
		Name: "Newly Listed", Brand: "Meridian", Slug: "newly-listed",
		SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
		PriceCents: 100000, SignedIn: true, ReviewStanding: ReviewOpen, RatingCount: 0,
	}
	html := renderToString(t, Product(ProductMeta(&fresh), &fresh))

	if !strings.Contains(html, `action="/p/newly-listed/reviews#write-review"`) {
		t.Error("a product with no reviews offers no way to write one, so it can " +
			"never have any")
	}
	if !strings.Contains(html, i18n.T(ctx, i18n.KeyNoReviewsYet)) {
		t.Error("the section renders with no reviews and says nothing about why it is empty")
	}
	// The score summary is what depends on there being ratings. Rendering
	// "0.0" above an empty bar chart is worse than saying nobody has reviewed it.
	if strings.Contains(html, `class="goen-pdp__score"`) {
		t.Error("a product with no reviews renders a score")
	}

	rated := fresh
	rated.RatingCount = 3
	rated.Rating = 4.5
	withScore := renderToString(t, Product(ProductMeta(&rated), &rated))
	if !strings.Contains(withScore, `class="goen-pdp__score"`) {
		t.Error("a rated product lost its score summary")
	}
}

// TestQAAskSurface holds the three Q&A states as one matrix: signed-out
// visitors get a return to #questions, signed-in visitors get the form
// without a sign-in hint, and a refused draft stays in the textarea.
func TestQAAskSurface(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	base := ProductView{
		Name: "Pixelight 9 Pro", Brand: "Meridian", Slug: "pixelight-9-pro",
		SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
		PriceCents: 3690000,
	}
	askAction := `action="/p/pixelight-9-pro/questions"`
	signInHint := i18n.T(ctx, i18n.KeySignInToAsk)

	out := base
	html := renderToString(t, Product(ProductMeta(&out), &out))
	if strings.Contains(html, askAction) {
		t.Error("signed-out Q&A still renders the ask form")
	}
	if !strings.Contains(html, out.AskSignInHref()) {
		t.Errorf("signed-out Q&A has no return to #questions; want href %q", out.AskSignInHref())
	}

	in := base
	in.SignedIn = true
	signedIn := renderToString(t, Product(ProductMeta(&in), &in))
	if !strings.Contains(signedIn, askAction) {
		t.Error("signed-in Q&A lost the ask form")
	}
	if strings.Contains(signedIn, in.AskSignInHref()) {
		t.Error("signed-in Q&A still offers the sign-in link")
	}
	if strings.Contains(signedIn, signInHint) {
		t.Error("signed-in Q&A still tells the customer to sign in")
	}

	refused := in
	refused.AskOutcome = "bad"
	refused.AskDraft = "這個草稿在 422 之後還必須留在欄位裡"
	rejected := renderToString(t, Product(ProductMeta(&refused), &refused))
	if !strings.Contains(rejected, refused.AskDraft) {
		t.Error("rejected Q&A lost the submitted draft")
	}
	if !strings.Contains(rejected, i18n.T(ctx, i18n.KeyQuestionRefused)) {
		t.Error("rejected Q&A has no field error")
	}
	if !strings.Contains(rejected, `aria-invalid="true"`) {
		t.Error("rejected Q&A does not mark the textarea invalid")
	}
	if strings.Contains(rejected, signInHint) {
		t.Error("rejected Q&A tells a signed-in customer to sign in")
	}
}

// The first two rows' photographs are the page's first paint, so they must not
// wait for layout; everything past them stays lazy.
func TestTheFirstRowsOfAListingLoadsItsPhotographsEagerly(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	tiles := make([]ProductTile, 0, 10)
	for i := range 10 {
		slug := fmt.Sprintf("p%d", i)
		tiles = append(tiles, ProductTile{Slug: slug, Name: slug, PriceCents: 1000, ImageURL: "/img/" + slug + ".webp", ImageAlt: slug})
	}
	view := ListingView{Slug: "audio", Name: "Audio", Products: tiles}
	html := renderToString(t, Listing(ListingMeta(ctx, view), view, nil, nil))

	if got := strings.Count(html, `loading="lazy"`); got != 2 {
		t.Errorf("%d lazy photographs, want 2 (the tiles after the first eight)", got)
	}
	if got := strings.Count(html, `fetchpriority="high"`); got != 1 {
		t.Errorf("%d high-priority photographs, want 1 (the first tile only)", got)
	}
	if view.Products[0].Eager {
		t.Error("FirstRowEager changed the caller's slice")
	}
}

func shelf(n int) []ProductTile {
	tiles := make([]ProductTile, 0, n)
	for i := range n {
		slug := fmt.Sprintf("p%d", i)
		tiles = append(tiles, ProductTile{Slug: slug, Name: slug, PriceCents: 1000, Comparable: true, ImageURL: "/img/" + slug + ".webp", ImageAlt: slug})
	}
	return tiles
}

// A long shelf is one grid of every product: nothing is lifted into a lead row.
func TestALongShelfIsOneGridOfEveryProduct(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for name, view := range map[string]ListingView{
		"eight products":   {Slug: "a", Name: "A", Products: shelf(8), Total: 8, Page: 1},
		"long, unfiltered": {Slug: "a", Name: "A", Products: shelf(12), Total: 30, Page: 1, PageSize: 24},
		"filtered":         {Slug: "a", Name: "A", Products: shelf(12), Total: 30, Page: 1, Filtered: true},
		"the second page":  {Slug: "a", Name: "A", Products: shelf(12), Total: 30, Page: 2},
	} {
		html := renderToString(t, Listing(ListingMeta(ctx, view), view, nil, nil))
		if strings.Contains(html, "goen-featured") || strings.Contains(html, "精選商品") {
			t.Errorf("%s: has a lead row", name)
		}
		if got := strings.Count(html, `class="goen-tile__cell"`); got != len(view.Products) {
			t.Errorf("%s: the grid holds %d cards, want all %d", name, got, len(view.Products))
		}
		if !strings.Contains(html, `value="p0"`) {
			t.Errorf("%s: the grid drops the first product", name)
		}
	}
}

// The head offers the department's sub-categories, marks the one the shopper is
// in, and draws the department's photograph only when it has one.
func TestTheHeadOffersItsSubcategoriesAndPhotograph(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := ListingView{
		Slug: "cases", Name: "Cases", Crumbs: []Crumb{{Slug: "accessories", Name: "Accessories"}},
		Theme: &Theme{
			Tone:     ToneMist,
			Photo:    Photo{URL: "/img/d.webp", Alt: "a desk"},
			Children: []Crumb{{Slug: "chargers", Name: "Chargers"}, {Slug: "cases", Name: "Cases"}},
		},
	}
	html := renderToString(t, Listing(ListingMeta(ctx, view), view, nil, nil))
	if !strings.Contains(html, `<a class="goen-pagehead__chip" href="/c/chargers">Chargers</a>`) {
		t.Error("a sibling sub-category is not a chip")
	}
	if !strings.Contains(html, `<a class="goen-pagehead__chip" href="/c/cases" aria-current="page">Cases</a>`) {
		t.Error("the current sub-category is not marked")
	}
	if !strings.Contains(html, `class="goen-pagehead__photo" src="/img/d.webp"`) || !strings.Contains(html, `alt="a desk"`) {
		t.Error("the department photograph is not drawn with its alt text")
	}

	bare := ListingView{Slug: "audio", Name: "Audio"}
	if h := renderToString(t, Listing(ListingMeta(ctx, bare), bare, nil, nil)); strings.Contains(h, "goen-pagehead__photo") || strings.Contains(h, "goen-pagehead__chips") {
		t.Error("a head with no photograph or children draws them anyway")
	}

	only := ListingView{Slug: "tea", Name: "Tea", Theme: &Theme{Children: []Crumb{{Slug: "tea", Name: "Tea"}}}}
	if h := renderToString(t, Listing(ListingMeta(ctx, only), only, nil, nil)); strings.Contains(h, "goen-pagehead__chips") {
		t.Error("a head with a single sub-category draws it as a choice")
	}
}

// A search that finds nothing sends the shopper to the departments, which the
// header already lists.
func TestAnEmptySearchSuggestsTheDepartments(t *testing.T) {
	t.Parallel()
	ctx := layouts.WithTopNav(i18n.WithLocale(t.Context(), i18n.ZhHant), []layouts.NavItem{
		{Slug: "tech", Name: "3C", Href: "/c/tech"}, {Slug: "fashion", Name: "服飾", Href: "/c/fashion"},
	})
	view := SearchView{Query: "zzqxv"}
	html := renderComponent(t, ctx, Search(layouts.Page{}, view))
	for _, want := range []string{`<a class="goen-pagehead__chip" href="/c/tech">3C</a>`, `href="/c/fashion">服飾</a>`, "搜尋「zzqxv」"} {
		if !strings.Contains(html, want) {
			t.Errorf("the empty search lacks %q:\n%s", want, html)
		}
	}
}

// A changed filter fetches the page the button would have navigated to, and the
// button stays in the markup for a browser without script.
func TestFiltersApplyThemselvesAndKeepTheirButton(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := ListingView{Slug: "audio", Name: "Audio", Products: shelf(2), Total: 2, Filtered: true, InStockOnly: true}
	html := renderToString(t, Listing(ListingMeta(ctx, view), view, nil, nil))

	for _, want := range []string{
		`hx-get="/c/audio"`, `hx-trigger="change delay:300ms"`, `hx-push-url="true"`,
		`hx-target="#listing-results"`, `hx-select-oob="#filters-applied, #listing-status:innerHTML, #listing-count:innerHTML, #listing-sort:innerHTML"`,
		`id="listing-status" class="goen-sr-only" role="status" aria-live="polite"`,
		`class="goen-btn goen-btn--primary goen-btn--block goen-filters__apply"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("filters lack %q", want)
		}
	}
	if !strings.Contains(html, `<div id="filters-applied">`) {
		t.Error("the active-filter chips have no stable region to be swapped into")
	}
	unfiltered := renderToString(t, Listing(ListingMeta(ctx, ListingView{Slug: "audio", Name: "Audio"}), ListingView{Slug: "audio", Name: "Audio"}, nil, nil))
	if !strings.Contains(unfiltered, `<div id="filters-applied">`) {
		t.Error("an unfiltered page has no chips region, so removing the last filter could not clear it")
	}
}

// htmx focuses any [autofocus] in swapped content, so a partial render that
// carried it would pull focus off the control the shopper just changed. The
// latest change wins, and a failed update has a place to say so.
func TestAPartialListingNeverTakesFocusAndTheLatestChangeWins(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := ListingView{Slug: "audio", Name: "Audio", Products: shelf(2), Total: 2, Filtered: true, InStockOnly: true}

	whole := renderComponent(t, ctx, Listing(layouts.Page{}, view, nil, nil))
	if !strings.Contains(whole, `id="listing-results" tabindex="-1" autofocus`) {
		t.Error("a whole filtered page no longer focuses its results")
	}
	partial := renderComponent(t, AsPartial(ctx), Listing(layouts.Page{}, view, nil, nil))
	if strings.Contains(partial, "autofocus") {
		t.Errorf("a partial render carries autofocus:\n%s", partial)
	}
	for _, want := range []string{`hx-sync="this:replace"`, `hx-status:5xx="swap:none"`, `class="goen-filters__error" role="alert" hidden`, `<div id="filters-applied"></div>`} {
		if !strings.Contains(whole, want) && !strings.Contains(renderToString(t, Listing(layouts.Page{}, ListingView{Slug: "a", Name: "A"}, nil, nil)), want) {
			t.Errorf("missing %q", want)
		}
	}
}

// A search offers the listing's sort control, best match first, and a page of
// results keeps the chosen order in every pager link.
func TestSearchOffersTheListingSortAndKeepsItAcrossPages(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := SearchView{Query: "pro", Products: shelf(2), Total: 60, Page: 1, PageSize: 24, Sort: "price_asc"}
	html := renderComponent(t, ctx, Search(SearchMeta(ctx, "pro"), view))
	for _, want := range []string{
		`hx-get="/search"`, `hx-target="#search-results"`, `<input type="hidden" name="q" value="pro">`,
		`<select class="ui-select" id="sort" name="sort">`,
		`<option value="">Best match</option>`, `<option value="price_asc" selected>Price, low to high</option>`,
		`href="/search?q=pro&amp;sort=price_asc&amp;page=2"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the search lacks %q:\n%s", want, html)
		}
	}
}

func TestSearchKeepsFailedSortFeedbackOutsideResults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		locale i18n.Locale
		text   string
	}{
		{name: "english", locale: i18n.En, text: "We cannot show the product list right now. Please try again shortly."},
		{name: "traditional chinese", locale: i18n.ZhHant, text: "商品列表暫時無法顯示，請稍後再試。"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			view := SearchView{Query: "pro", Products: shelf(2), Total: 2, Page: 1, PageSize: 24}
			page := renderComponent(t, ctx, Search(SearchMeta(ctx, view.Query), view))
			const errorID = `id="goen-search-sort-error"`
			if got := strings.Count(page, errorID); got != 1 {
				t.Fatalf("Search() error region count = %d, want 1", got)
			}
			want := `<p id="goen-search-sort-error" class="goen-filters__error" role="alert" hidden>` + tt.text + `</p>`
			if !strings.Contains(page, want) {
				t.Fatalf("Search() lacks the initially hidden localized alert %q", want)
			}
			_, afterForm, ok := strings.Cut(page, `<form class="goen-search-sort"`)
			if !ok {
				t.Fatal("Search() has no sort form")
			}
			_, afterForm, ok = strings.Cut(afterForm, `</form>`)
			if !ok {
				t.Fatal("Search() has no closing sort form")
			}
			beforeResults, _, ok := strings.Cut(afterForm, `<div id="search-results">`)
			if !ok || !strings.Contains(beforeResults, want) {
				t.Error("Search() alert is not between the sort form and replaceable results")
			}
		})
	}
}

// A search that found nothing offers the newest products besides the departments.
func TestAnEmptySearchOffersTheNewestProducts(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	newest := shelf(2)
	html := renderComponent(t, ctx, Search(SearchMeta(ctx, "zzqxv"), SearchView{Query: "zzqxv", Newest: newest}))
	if !strings.Contains(html, `<h2 class="goen-listing__subhead">Newest products</h2>`) || !strings.Contains(html, newest[0].Name) {
		t.Errorf("the empty search does not show the newest products:\n%s", html)
	}
	if strings.Contains(html, `name="sort"`) {
		t.Error("an empty search offers a sort control with nothing to sort")
	}
}

// Each applied filter links to the listing without that one filter and nothing
// else, and names what it removes, because its glyph is only a cross.
func TestEachAppliedFilterLinksToTheListingWithoutIt(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := ListingView{
		Slug: "audio", Name: "Audio", Filtered: true, InStockOnly: true, MinPrice: 1000, MaxPrice: 5000,
		Query: "brand=aurora&brand=nimbus&in_stock=1&max_price=50&min_price=10&sort=rating",
		Facets: []FacetGroup{{Label: "Brand", Options: []FacetOption{
			{Value: "aurora", Label: "Aurora", Selected: true},
			{Value: "nimbus", Label: "Nimbus", Selected: true},
		}}},
	}
	want := map[string]string{
		"Remove “Aurora”":   "/c/audio?brand=nimbus&in_stock=1&max_price=50&min_price=10&sort=rating",
		"Remove “Nimbus”":   "/c/audio?brand=aurora&in_stock=1&max_price=50&min_price=10&sort=rating",
		"Remove “In stock”": "/c/audio?brand=aurora&brand=nimbus&max_price=50&min_price=10&sort=rating",
		"Remove “10–50”":    "/c/audio?brand=aurora&brand=nimbus&in_stock=1&sort=rating",
	}
	chips := view.AppliedChips(ctx)
	if len(chips) != len(want) {
		t.Fatalf("%d chips, want %d: %+v", len(chips), len(want), chips)
	}
	for _, c := range chips {
		if want[c.RemoveLabel] != c.Remove {
			t.Errorf("%q links to %q, want %q", c.RemoveLabel, c.Remove, want[c.RemoveLabel])
		}
	}
	html := renderComponent(t, ctx, Listing(ListingMeta(ctx, view), view, nil, nil))
	if !strings.Contains(html, `aria-label="Remove “Aurora”"`) {
		t.Error("the chip's remove link has no accessible name in the markup")
	}
}

// The rail is not swapped, so a swapped response carries each brand's count on
// its own; a whole-page response must not, or ids would repeat.
func TestAPartialListingCarriesTheBrandCountsForTheRail(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := ListingView{Slug: "audio", Name: "Audio", Products: shelf(1), Total: 1, Facets: []FacetGroup{{Label: "Brand", Options: []FacetOption{{Value: "aurora", Label: "Aurora", Count: 3}}}}}
	oob := `<span class="goen-filters__count" id="brand-count-aurora" hx-swap-oob="true">3</span>`
	if html := renderComponent(t, AsPartial(ctx), Listing(ListingMeta(ctx, view), view, nil, nil)); !strings.Contains(html, oob) {
		t.Errorf("a partial listing lacks the out-of-band count:\n%s", html)
	}
	if html := renderComponent(t, ctx, Listing(ListingMeta(ctx, view), view, nil, nil)); strings.Contains(html, "hx-swap-oob") {
		t.Error("a whole-page listing carries out-of-band counts")
	}
}

// A group's name carries how many of its options are on, so a shopper who has
// closed the group still sees that it filters.
func TestEachFilterGroupSaysHowManyOfItsOptionsAreOn(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := ListingView{
		Slug: "audio", Name: "Audio", Products: shelf(1), Total: 1, Filtered: true, InStockOnly: true,
		Facets: []FacetGroup{{Label: "Brand", Options: []FacetOption{
			{Value: "aurora", Label: "Aurora", Selected: true},
			{Value: "nimbus", Label: "Nimbus", Selected: true},
			{Value: "orbit", Label: "Orbit"},
		}}},
	}
	html := renderComponent(t, ctx, Listing(ListingMeta(ctx, view), view, nil, nil))
	for id, want := range map[string]string{
		"brand-selected": `<span aria-hidden="true">2</span> <span class="goen-sr-only">2 selected</span>`,
		"stock-selected": `<span aria-hidden="true">1</span> <span class="goen-sr-only">1 selected</span>`,
		"price-selected": ``,
	} {
		got := `<span class="goen-filters__selected" id="` + id + `">` + want + `</span>`
		if !strings.Contains(html, got) {
			t.Errorf("no %s with %q in the rail:\n%s", id, want, html)
		}
	}
}

// The rail is not swapped, so a swapped response carries each badge on its own.
func TestAPartialListingCarriesTheSelectedCountsForTheRail(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := ListingView{
		Slug: "audio", Name: "Audio", Products: shelf(1), Total: 1, Filtered: true, MinPrice: 1000,
		Facets: []FacetGroup{{Label: "Brand", Options: []FacetOption{{Value: "aurora", Label: "Aurora", Selected: true}}}},
	}
	html := renderComponent(t, AsPartial(ctx), Listing(ListingMeta(ctx, view), view, nil, nil))
	for _, want := range []string{
		`<span class="goen-filters__selected" id="brand-selected" hx-swap-oob="true"><span aria-hidden="true">1</span>`,
		`<span class="goen-filters__selected" id="stock-selected" hx-swap-oob="true"></span>`,
		`<span class="goen-filters__selected" id="price-selected" hx-swap-oob="true"><span aria-hidden="true">1</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("a partial listing lacks %s", want)
		}
	}
}
