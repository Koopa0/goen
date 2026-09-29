package pages

import (
	"strings"
	"testing"

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
	v := &ProductView{Slug: "z"}
	for i := range MaxCompare {
		v.Comparing = append(v.Comparing, string(rune('a'+i)))
	}
	html := renderToString(t, Product(ProductMeta(v), v))
	if !strings.Contains(html, "比較已滿") {
		t.Error("a full comparison hides the add control without saying why")
	}
}
