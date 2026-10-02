package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
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

// TestTheProductPageAddsToTheComparisonInTheAddress: the control extends the
// comparison the reader arrived with, and says so when there is no room.
func TestTheProductPageAddsToTheComparisonInTheAddress(t *testing.T) {
	t.Parallel()
	v := &ProductView{Slug: "c", Comparing: []string{"a", "b"}}
	if got, want := v.CompareHref(), "/compare?p=a&p=b&p=c"; got != want {
		t.Errorf("CompareHref = %q, want %q", got, want)
	}
	if v.ComparingFull() {
		t.Error("two products already reads as full")
	}
	full := &ProductView{Slug: "z"}
	for i := range MaxCompare {
		full.Comparing = append(full.Comparing, string(rune('a'+i)))
	}
	if !full.ComparingFull() {
		t.Errorf("%d products do not read as full", MaxCompare)
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

// TestTheProductPageSaysWhenTheComparisonIsFull: the add control disappears at
// the maximum, so the page has to say why.
func TestTheProductPageSaysWhenTheComparisonIsFull(t *testing.T) {
	t.Parallel()
	v := &ProductView{Slug: "z", Comparable: true}
	for i := range MaxCompare {
		v.Comparing = append(v.Comparing, string(rune('a'+i)))
	}
	html := renderToString(t, Product(ProductMeta(v), v))
	if !strings.Contains(html, "比較已滿") {
		t.Error("a full comparison hides the add control without saying why")
	}
}

// TestTheProductPageOffersComparisonOnlyWhereTheDepartmentDoes: the control is
// the department's to give. Where it is given it sits with the specs and not in
// the buy box; where it is not, nothing on the page leads to /compare.
func TestTheProductPageOffersComparisonOnlyWhereTheDepartmentDoes(t *testing.T) {
	t.Parallel()
	view := func(comparable bool) *ProductView {
		return &ProductView{
			Name: "Pixelight 9 Pro", Slug: "pixelight-9-pro", Comparable: comparable,
			SelectionOK: true, Sellable: true, AnySellable: true, PriceCents: 100,
			Specs: []ProductSpec{{Label: "重量", Value: "199 g"}},
		}
	}

	on := renderToString(t, Product(ProductMeta(view(true)), view(true)))
	link := `href="/compare?p=pixelight-9-pro"`
	at := strings.Index(on, link)
	if at < 0 {
		t.Fatalf("a comparable department's product page has no %s", link)
	}
	if !strings.Contains(on, "與同類商品比較") {
		t.Error("the control does not say what it compares with")
	}
	if buyEnd := strings.Index(on, `id="specs-heading"`); at < buyEnd {
		t.Error("the control is before the specs, in the buy box")
	}

	off := renderToString(t, Product(ProductMeta(view(false)), view(false)))
	for _, bad := range []string{"/compare", "goen-pdp__compare", "與同類商品比較"} {
		if strings.Contains(off, bad) {
			t.Errorf("a department that does not compare shows %q", bad)
		}
	}
}

// TestTheComparisonControlFallsBackWhenThereAreNoSpecs: it must not vanish with
// the specs accordion it normally sits in.
func TestTheComparisonControlFallsBackWhenThereAreNoSpecs(t *testing.T) {
	t.Parallel()
	v := &ProductView{Name: "N", Slug: "n", Comparable: true, SelectionOK: true}
	if html := renderToString(t, Product(ProductMeta(v), v)); !strings.Contains(html, `href="/compare?p=n"`) {
		t.Error("a comparable product with no specs has no way into a comparison")
	}
}

// TestACompareBoxOnlyWhereTheTileIsComparable: the box and the "compare
// selected" bar come and go together.
func TestACompareBoxOnlyWhereTheTileIsComparable(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	tile := func(comparable bool) ProductTile {
		return ProductTile{Slug: "a", Name: "A", Comparable: comparable}
	}
	if !AnyComparable([]ProductTile{tile(false), tile(true)}) || AnyComparable([]ProductTile{tile(false)}) || AnyComparable(nil) {
		t.Error("AnyComparable does not report whether a tile carries the box")
	}
	for _, tt := range []struct {
		comparable bool
		wantBox    bool
	}{{true, true}, {false, false}} {
		html := renderComponent(t, ctx, compareForm(AnyComparable([]ProductTile{tile(tt.comparable)})))
		if got := strings.Contains(html, `id="compare-pick"`); got != tt.wantBox {
			t.Errorf("comparable=%v: the compare bar is present = %v, want %v", tt.comparable, got, tt.wantBox)
		}
		box := renderComponent(t, ctx, Tile(tile(tt.comparable)))
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
	v.Suggestions = []ProductTile{
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
