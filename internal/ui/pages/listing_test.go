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
		Brands:   []FacetOption{{Value: "nimbus", Label: "Nimbus", Count: 1}},
	}
	html := renderToString(t, Listing(ListingMeta(ctx, view), view))

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

// TestFilteredListingFocusesResults holds where a filter submission should
// leave keyboard focus: on the results region, not back at the page top.
func TestFilteredListingFocusesResults(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{
		Slug: "audio", Name: "耳機與音響", Filtered: true, InStockOnly: true,
		Products: []ProductTile{{Slug: "nimbus-buds-pro", Name: "Nimbus Buds Pro", PriceCents: 399000}},
	}
	html := renderToString(t, Listing(ListingMeta(ctx, view), view))

	if !strings.Contains(html, `id="listing-results"`) {
		t.Error("the results region has no fragment target")
	}
	if !strings.Contains(html, `id="listing-results" tabindex="-1" autofocus`) {
		t.Error("a filtered listing does not autofocus the results region")
	}
	if !strings.Contains(html, `class="goen-filters__applied"`) {
		t.Error("a filtered listing shows no applied-filter summary")
	}
	idxFilters := strings.Index(html, `class="goen-listing__filters"`)
	idxResults := strings.Index(html, `id="listing-results"`)
	if idxFilters < 0 || idxResults < 0 ||
		!strings.Contains(html[idxFilters:idxResults], `class="goen-filters__applied"`) {
		t.Error("applied-filter summary is not grouped in the filter column")
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
	html := renderToString(t, Listing(ListingMeta(i18n.WithLocale(t.Context(), i18n.ZhHant), view), view))

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
	if got := renderToString(t, Listing(ListingMeta(ctx, one), one)); strings.Contains(got, marked) {
		t.Errorf("a single-priced product renders %q", marked)
	}

	many := ListingView{Slug: "phones", Name: "手機", Products: []ProductTile{
		{Slug: "spread", Name: "Spread", PriceCents: 2590000, PriceVaries: true},
	}}
	if got := renderToString(t, Listing(ListingMeta(ctx, many), many)); !strings.Contains(got, marked) {
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

// TestAProductWithNothingLeftSaysSo holds the state between "this combination
// is gone" and "choose one". SoldOut() asks about the RESOLVED variant, so it
// is false until every option is picked: "has this visitor chosen one" and "can
// anything here be bought" are different questions.
func TestAProductWithNothingLeftSaysSo(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	gone := i18n.T(ctx, i18n.KeyAllSoldOut)

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
			anySellable: true, exact: true, sellable: false, want: false,
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

// The first row's photograph is the page's largest paint, so it must not wait
// for layout; everything past it stays lazy.
func TestTheFirstRowOfAListingLoadsItsPhotographsEagerly(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	tiles := make([]ProductTile, 0, 6)
	for i := range 6 {
		slug := fmt.Sprintf("p%d", i)
		tiles = append(tiles, ProductTile{Slug: slug, Name: slug, PriceCents: 1000, ImageURL: "/img/" + slug + ".webp", ImageAlt: slug})
	}
	view := ListingView{Slug: "audio", Name: "Audio", Products: tiles}
	html := renderToString(t, Listing(ListingMeta(ctx, view), view))

	if got := strings.Count(html, `loading="lazy"`); got != 2 {
		t.Errorf("%d lazy photographs, want 2 (the tiles after the first row of 4)", got)
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

// A long shelf leads with four larger cards, and those four are not drawn a
// second time in the grid below.
func TestALongShelfLeadsWithFourCardsThatTheGridDoesNotRepeat(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{Slug: "audio", Name: "Audio", Products: shelf(12), Total: 30, Page: 1, PageSize: 24}
	html := renderToString(t, Listing(ListingMeta(ctx, view), view))

	lead, grid, ok := strings.Cut(html, `class="goen-listing__layout"`)
	if !ok || strings.Count(lead, `class="goen-featured__grid"`) != 1 {
		t.Fatalf("no lead row before the layout:\n%s", html)
	}
	if got := strings.Count(lead, `class="goen-tile__cell"`); got != 4 {
		t.Errorf("the lead row holds %d cards, want 4", got)
	}
	if got := strings.Count(grid, `class="goen-tile__cell"`); got != 8 {
		t.Errorf("the grid holds %d cards, want the other 8", got)
	}
	if strings.Contains(grid, `value="p0"`) || !strings.Contains(grid, `value="p4"`) {
		t.Error("the grid repeats a lead card or drops the first one after them")
	}
	if got := strings.Count(html, `fetchpriority="high"`); got != 1 {
		t.Errorf("%d high-priority photographs, want 1 (the first lead card)", got)
	}
	if got := strings.Count(html, `form="compare-pick"`); got != 12 {
		t.Errorf("%d compare boxes joined to the form, want all 12", got)
	}
}

// Short shelves, filtered shelves and later pages have no lead row.
func TestOnlyTheUnfilteredFirstPageOfALongShelfHasALeadRow(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for name, view := range map[string]ListingView{
		"eight products":  {Slug: "a", Name: "A", Products: shelf(8), Total: 8, Page: 1},
		"filtered":        {Slug: "a", Name: "A", Products: shelf(12), Total: 30, Page: 1, Filtered: true},
		"the second page": {Slug: "a", Name: "A", Products: shelf(12), Total: 30, Page: 2},
	} {
		html := renderToString(t, Listing(ListingMeta(ctx, view), view))
		if strings.Contains(html, "goen-featured") {
			t.Errorf("%s: has a lead row", name)
		}
		if got := strings.Count(html, `class="goen-tile__cell"`); got != len(view.Products) {
			t.Errorf("%s: the grid holds %d cards, want all %d", name, got, len(view.Products))
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
	html := renderToString(t, Listing(ListingMeta(ctx, view), view))
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
	if h := renderToString(t, Listing(ListingMeta(ctx, bare), bare)); strings.Contains(h, "goen-pagehead__photo") || strings.Contains(h, "goen-pagehead__chips") {
		t.Error("a head with no photograph or children draws them anyway")
	}
}

// A run of pages stays one row: both ends, the neighbours of the current page,
// and a gap wherever pages are left out.
func TestThePagerKeepsBothEndsAndTheNeighboursOfTheCurrentPage(t *testing.T) {
	t.Parallel()
	href := func(n int) string { return fmt.Sprintf("?page=%d", n) }
	label := func(links []PageLink) string {
		var parts []string
		for _, l := range links {
			switch {
			case l.Gap:
				parts = append(parts, "…")
			case l.Current:
				parts = append(parts, "["+l.Label+"]")
			default:
				parts = append(parts, l.Label)
			}
		}
		return strings.Join(parts, " ")
	}
	for _, c := range []struct {
		current, pages int
		want           string
	}{
		{1, 1, "[1]"},
		{2, 3, "1 [2] 3"},
		{1, 9, "[1] 2 … 9"},
		{5, 9, "1 … 4 [5] 6 … 9"},
		{9, 9, "1 … 8 [9]"},
	} {
		if got := label(pageWindow(c.current, c.pages, href)); got != c.want {
			t.Errorf("page %d of %d: %q, want %q", c.current, c.pages, got, c.want)
		}
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
	html := renderToString(t, Listing(ListingMeta(ctx, view), view))

	for _, want := range []string{
		`hx-get="/c/audio"`, `hx-trigger="change delay:300ms"`, `hx-push-url="true"`,
		`hx-target="#listing-results"`, `hx-select-oob="#filters-applied, #listing-status:innerHTML"`,
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
	unfiltered := renderToString(t, Listing(ListingMeta(ctx, ListingView{Slug: "audio", Name: "Audio"}), ListingView{Slug: "audio", Name: "Audio"}))
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

	whole := renderComponent(t, ctx, Listing(layouts.Page{}, view))
	if !strings.Contains(whole, `id="listing-results" tabindex="-1" autofocus`) {
		t.Error("a whole filtered page no longer focuses its results")
	}
	partial := renderComponent(t, AsPartial(ctx), Listing(layouts.Page{}, view))
	if strings.Contains(partial, "autofocus") {
		t.Errorf("a partial render carries autofocus:\n%s", partial)
	}
	for _, want := range []string{`hx-sync="this:replace"`, `hx-status:5xx="swap:none"`, `class="goen-filters__error" role="alert" hidden`, `<div id="filters-applied"></div>`} {
		if !strings.Contains(whole, want) && !strings.Contains(renderToString(t, Listing(layouts.Page{}, ListingView{Slug: "a", Name: "A"})), want) {
			t.Errorf("missing %q", want)
		}
	}
}
