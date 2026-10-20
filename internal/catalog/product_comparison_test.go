package catalog

import (
	"strings"
	"testing"
)

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
