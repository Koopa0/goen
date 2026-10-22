package catalog

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/warranty"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/tile"
)

// TestCompareShowsTheTableOnlyWhenThereAreTwoProducts holds Enough() and the
// markup it gates. One product is the too-few empty state; the table class
// exists only when there are two columns. A layout row that marks the wrapper
// would pass either way.
func TestCompareShowsTheTableOnlyWhenThereAreTwoProducts(t *testing.T) {
	t.Parallel()

	page := layouts.Page{Title: "比較"}

	if (CompareView{Products: []CompareProduct{{Slug: "a"}}}).Enough() {
		t.Fatal("one product is enough — the empty state would never render")
	}
	if !(CompareView{Products: []CompareProduct{{Slug: "a"}, {Slug: "b"}}}).Enough() {
		t.Fatal("two products are not enough — the table would never render")
	}

	one := renderToString(t, Compare(page, CompareView{
		Products: []CompareProduct{{Slug: "pixelight-9-pro", Name: "Pixelight 9 Pro"}},
	}))
	if strings.Contains(one, `class="goen-compare__table"`) {
		t.Error("one product rendered the comparison table")
	}
	if !strings.Contains(one, `class="ui-empty"`) {
		t.Error("one product did not render the too-few empty state")
	}

	two := renderToString(t, Compare(page, CompareView{
		Products: []CompareProduct{
			{Slug: "pixelight-9-pro", Name: "Pixelight 9 Pro"},
			{Slug: "pixelight-9", Name: "Pixelight 9"},
		},
	}))
	if !strings.Contains(two, `class="goen-compare__table"`) {
		t.Error("two products did not render the comparison table")
	}
	if strings.Contains(two, `class="ui-empty"`) {
		t.Error("two products still rendered the too-few empty state")
	}
}

func compareOf(slugs ...string) CompareView {
	v := CompareView{}
	for _, s := range slugs {
		v.Products = append(v.Products, CompareProduct{Slug: s, Name: s})
	}
	return v
}

// TestTheComparisonIsTheAddress: adding and removing are addresses, so the
// state is the link and nothing is written.
func TestTheComparisonIsTheAddress(t *testing.T) {
	t.Parallel()
	v := compareOf("a", "b")
	if got, want := v.AddHref("c"), "/compare?p=a&p=b&p=c"; got != want {
		t.Errorf("AddHref = %q, want %q", got, want)
	}
	if got, want := v.RemoveHref("a"), "/compare?p=b"; got != want {
		t.Errorf("RemoveHref = %q, want %q", got, want)
	}
}

// TestTheComparisonPageOffersPlainLinksToAddAndRemove: no form posts, no
// script, and no checkbox that would do nothing.
func TestTheComparisonPageOffersPlainLinksToAddAndRemove(t *testing.T) {
	t.Parallel()
	v := compareOf("a", "b")
	v.Query = "phone"
	v.Candidates = []CompareCandidate{{Slug: "c", Name: "Cee", Brand: "B"}}
	html := renderToString(t, Compare(layouts.Page{Title: "比較"}, v))
	for _, want := range []string{
		`href="/compare?p=a&amp;p=b&amp;p=c"`, // add
		`href="/compare?p=b"`,                 // remove a
		`href="/compare?p=a"`,                 // remove b
		`<form class="goen-compare__picker" method="get" action="/compare"`,
		`name="p" value="a"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, bad := range []string{`action="/compare/`, `type="checkbox"`} {
		if strings.Contains(html, bad) {
			t.Errorf("the comparison page contains %s", bad)
		}
	}
}

// TestAFullComparisonSaysSoAndOffersNoPicker: the maximum is refused with a
// message, not by silently dropping a product.
func TestAFullComparisonSaysSoAndOffersNoPicker(t *testing.T) {
	t.Parallel()
	slugs := make([]string, MaxCompare)
	for i := range slugs {
		slugs[i] = string(rune('a' + i))
	}
	full := renderToString(t, Compare(layouts.Page{Title: "比較"}, compareOf(slugs...)))
	if !strings.Contains(full, `goen-compare__note`) || strings.Contains(full, `goen-compare__picker`) {
		t.Errorf("a full comparison should show the limit message and no picker")
	}
	room := renderToString(t, Compare(layouts.Page{Title: "比較"}, compareOf(slugs[:MaxCompare-1]...)))
	if strings.Contains(room, `role="status"`) || !strings.Contains(room, `goen-compare__picker`) {
		t.Errorf("a comparison with room should show the picker and no limit message")
	}

	dropped := compareOf(slugs...)
	dropped.Dropped = true
	if !strings.Contains(renderToString(t, Compare(layouts.Page{Title: "比較"}, dropped)), `role="status"`) {
		t.Error("a link naming too many products dropped some without saying so")
	}
}

// TestOneProductInTheComparisonInvitesTheNextNotTheDeadEnd: a product page's
// add link lands here with one product, and the page must let the reader add
// another rather than tell them to go and find one.
func TestOneProductInTheComparisonInvitesTheNextNotTheDeadEnd(t *testing.T) {
	t.Parallel()
	html := renderToString(t, Compare(layouts.Page{Title: "比較"}, compareOf("a")))
	if !strings.Contains(html, `goen-compare__picker`) {
		t.Error("one product shows no picker to add another")
	}
	if strings.Contains(html, "至少要選兩個商品") {
		t.Error("one product still shows the pick-two-products dead end")
	}
	if !strings.Contains(html, "已在比較中") {
		t.Error("one product does not say it is already in the comparison")
	}
}

// TestACompareBoxOnlyWhereTheTileIsComparable: the box and the "compare
// selected" bar come and go together.
func TestACompareBoxOnlyWhereTheTileIsComparable(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	candidate := func(comparable bool) tile.Tile {
		return tile.Tile{Slug: "a", Name: "A", Comparable: comparable}
	}
	if !tile.AnyComparable([]tile.Tile{candidate(false), candidate(true)}) || tile.AnyComparable([]tile.Tile{candidate(false)}) || tile.AnyComparable(nil) {
		t.Error("AnyComparable does not report whether a tile carries the box")
	}
	for _, tt := range []struct {
		comparable bool
		wantBox    bool
	}{{true, true}, {false, false}} {
		html := renderComponent(t, ctx, compareForm(tile.AnyComparable([]tile.Tile{candidate(tt.comparable)})))
		if got := strings.Contains(html, `id="compare-pick"`); got != tt.wantBox {
			t.Errorf("comparable=%v: the compare bar is present = %v, want %v", tt.comparable, got, tt.wantBox)
		}
		box := renderComponent(t, ctx, tile.Cell(candidate(tt.comparable)))
		if got := strings.Contains(box, `form="compare-pick"`); got != tt.wantBox {
			t.Errorf("comparable=%v: the tile's box is present = %v, want %v", tt.comparable, got, tt.wantBox)
		}
	}
}

// TestACompareRowDiffersWhenTheColumnsDisagree: a spec one product lacks counts
// as a value of its own, and only a row every product states is marked.
func TestACompareRowDiffersWhenTheColumnsDisagree(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name          string
		row           CompareRow
		products      int
		differs, mark bool
	}{
		{"all alike", CompareRow{Values: []string{"5G", "5G"}, SharedBy: 2}, 2, false, false},
		{"values differ", CompareRow{Values: []string{"5G", "4G"}, SharedBy: 2}, 2, true, true},
		{"one lacks it", CompareRow{Values: []string{"5G", ""}, SharedBy: 1}, 2, true, false},
		{"three, one apart", CompareRow{Values: []string{"a", "a", "b"}, SharedBy: 3}, 3, true, true},
		{"none stated", CompareRow{Values: []string{"", ""}}, 2, false, false},
	} {
		if got := tt.row.Differs(); got != tt.differs {
			t.Errorf("%s: Differs = %v, want %v", tt.name, got, tt.differs)
		}
		if got := tt.row.Marked(tt.products); got != tt.mark {
			t.Errorf("%s: Marked = %v, want %v", tt.name, got, tt.mark)
		}
	}
}

// TestADifferingRowIsMarkedAndSaysSoAloud: the weight is not the only signal.
func TestADifferingRowIsMarkedAndSaysSoAloud(t *testing.T) {
	t.Parallel()
	v := compareOf("a", "b")
	v.Rows = []CompareRow{
		{Label: "螢幕", Values: []string{"6.1", "6.7"}, SharedBy: 2},
		{Label: "重量", Values: []string{"199 g", "199 g"}, SharedBy: 2},
	}
	html := renderToString(t, Compare(layouts.Page{Title: "比較"}, v))
	if strings.Count(html, "is-differs") != 1 {
		t.Errorf("want exactly the differing row marked, got %d marks", strings.Count(html, "is-differs"))
	}
	if !strings.Contains(html, "規格不同") {
		t.Error("the marked row does not say it differs to a screen reader")
	}
}

// TestOneProductOffersWhatToCompareItWith: the lone product is a start, not a
// dead end — the heading says what to do, each suggestion adds itself by a plain
// link to the address that includes it, and the shelf is one link away.
func TestOneProductOffersWhatToCompareItWith(t *testing.T) {
	t.Parallel()
	v := compareOf("a")
	v.Products[0].Category, v.ShelfSlug = "手機", "phones"
	v.Suggestions = []tile.Tile{
		{Slug: "b", Name: "Bee", Brand: "B", PriceCents: 1000},
		{Slug: "c", Name: "Cee", Brand: "C", PriceCents: 2000},
	}
	html := renderToString(t, Compare(layouts.Page{Title: "比較"}, v))
	for _, want := range []string{
		"選擇要比較的商品",
		`href="/compare?p=a&amp;p=b"`,
		`href="/compare?p=a&amp;p=c"`,
		`href="/c/phones"`,
		"加入比較",
		"Bee",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the one-product page is missing %q", want)
		}
	}
	for _, bad := range []string{`type="checkbox"`, `action="/compare/`, `class="goen-compare__table"`} {
		if strings.Contains(html, bad) {
			t.Errorf("the one-product page contains %s", bad)
		}
	}

	two := compareOf("a", "b")
	two.Suggestions = v.Suggestions
	if html := renderToString(t, Compare(layouts.Page{Title: "比較"}, two)); strings.Contains(html, "goen-compare__suggest") {
		t.Error("a comparison of two still offers suggestions")
	}
}

// TestTheEmptyComparisonLinksToWhereBoxesAreOffered: the empty state tells the
// shopper to pick products on a list, so the link must be to one that has the
// box to tick; the home page has none.
func TestTheEmptyComparisonLinksToWhereBoxesAreOffered(t *testing.T) {
	t.Parallel()

	page := layouts.Page{Title: "比較"}
	with := renderToString(t, Compare(page, CompareView{StartSlug: "tech"}))
	if !strings.Contains(with, `<a href="/c/tech">`+i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyCompareTooFewLink)+`</a>`) {
		t.Error("the empty comparison does not link to the department that offers comparison")
	}
	without := renderToString(t, Compare(page, CompareView{}))
	if !strings.Contains(without, `<a href="/">`) {
		t.Error("with no department offering comparison the link should fall back to the home page")
	}
}

// TestCompareRowStatesPricesAsTheCardDoes: a product whose variants differ is
// priced "from", and one on sale shows the struck original price.
func TestCompareRowStatesPricesAsTheCardDoes(t *testing.T) {
	t.Parallel()

	got := renderToString(t, Compare(layouts.Page{Title: "比較"}, CompareView{
		Products: []CompareProduct{
			{Slug: "spread", Name: "Spread", PriceCents: 2590000, PriceVaries: true, InStock: true},
			{Slug: "sale", Name: "Sale", PriceCents: 100000, CompareCents: 150000, InStock: true, OnSale: true},
		},
	}))
	for _, want := range []string{"NT$25,900 起", `<s class="goen-tile__was">NT$1,500</s>`} {
		if !strings.Contains(got, want) {
			t.Errorf("Compare price row lacks %q", want)
		}
	}
}

func TestComparisonSaysSoldOutOncePerSoldOutColumn(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	got := renderComponent(t, ctx, Compare(layouts.Page{Title: "比較"}, CompareView{
		Products: []CompareProduct{
			{Slug: "a", Name: "A", PriceCents: 100000},
			{Slug: "b", Name: "B", PriceCents: 100000, InStock: true},
		},
	}))
	if n := strings.Count(got, i18n.T(ctx, i18n.KeySoldOut)); n != 1 {
		t.Errorf("Compare with one sold-out product says sold out %d times, want 1", n)
	}
}

// The header search and the comparison picker are both search landmarks on
// /compare, and two landmarks of one kind need names that tell them apart.
func TestTheComparePickerIsANamedSearchLandmark(t *testing.T) {
	t.Parallel()

	out := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), comparePicker(CompareView{}))
	if !strings.Contains(out, `role="search" aria-label="`) {
		t.Errorf("the comparison picker is an unnamed search landmark: %s", out)
	}
}

func TestWarrantyTermsStateThePurchasedDuration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale       i18n.Locale
		months       int
		want         string
		registration string
	}{
		{locale: i18n.ZhHant, months: 12, want: "12 個月", registration: "1 年"},
		{locale: i18n.ZhHant, months: 1, want: "1 個月", registration: "1 個月"},
		{locale: i18n.En, months: 12, want: "12 months", registration: "1-year"},
		{locale: i18n.En, months: 24, want: "24 months", registration: "2-year"},
		{locale: i18n.En, months: 1, want: "1 month", registration: "1-month"},
	} {
		ctx := i18n.WithLocale(t.Context(), tc.locale)
		if got := (&CompareProduct{WarrantyMonths: tc.months}).Warranty(ctx); got != tc.want {
			t.Errorf("%v: compare page, %d months = %q, want %q", tc.locale, tc.months, got, tc.want)
		}
		if got := (warranty.Line{HasTerm: true, Months: tc.months}).TermText(ctx); got != tc.registration {
			t.Errorf("%v: warranty page, %d months = %q, want %q", tc.locale, tc.months, got, tc.registration)
		}
	}
}
