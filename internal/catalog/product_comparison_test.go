package catalog

import (
	"strings"
	"testing"
)

// TestProductView_CompareHref holds the ready-made comparison the
// specifications lead to: the product first, then the similar ones, as the
// links of the compare page's own ready-made comparisons are written.
func TestProductView_CompareHref(t *testing.T) {
	t.Parallel()
	v := &ProductView{Slug: "c", Similar: []string{"a", "b"}}
	if got, want := v.CompareHref(), "/compare?p=c&p=a&p=b"; got != want {
		t.Errorf("CompareHref = %q, want %q", got, want)
	}
}

// TestProductComparisonOffered holds that the product page offers comparison
// only where its department does: the toggle in the buy column beside the add,
// and, where its shelf holds similar products, the link from the
// specifications to a ready-made comparison of them.
//
// The control is the department's to give. Where it is not given, nothing on
// the page leads to /compare or posts to the set.
func TestProductComparisonOffered(t *testing.T) {
	t.Parallel()
	view := func(comparable bool, similar ...string) *ProductView {
		return &ProductView{
			Name: "Pixelight 9 Pro", Slug: "pixelight-9-pro", Comparable: comparable, Similar: similar,
			SelectionOK: true, Sellable: true, AnySellable: true, PriceCents: 100,
			Specs: []ProductSpec{{Label: "重量", Value: "199 g"}},
		}
	}

	on := renderToString(t, Product(ProductMeta(view(true, "pixelight-9")), view(true, "pixelight-9")))
	link := `href="/compare?p=pixelight-9-pro&amp;p=pixelight-9"`
	at := strings.Index(on, link)
	if at < 0 {
		t.Fatalf("a comparable department's product page has no %s", link)
	}
	if !strings.Contains(on, "與同類商品比較") {
		t.Error("the link does not say what it compares with")
	}
	if buyEnd := strings.Index(on, `id="specs-heading"`); at < buyEnd {
		t.Error("the link is before the specs, in the buy box")
	}
	// The toggle stands in the add's row, after the add's form and before the
	// row closes, never inside that form.
	add, toggle := strings.Index(on, `<form id="pdp-add"`), strings.Index(on, `<form class="goen-comparetoggle"`)
	if add < 0 || toggle < 0 {
		t.Fatalf("the buy column has no add (%d) or no compare toggle (%d)", add, toggle)
	}
	if addEnd := add + strings.Index(on[add:], "</form>"); toggle < addEnd {
		t.Error("the compare toggle is inside the add's form")
	}
	if actions := strings.LastIndex(on[:add], `<div class="goen-pdp__actions">`); actions < 0 || strings.Contains(on[actions:toggle], `<div class="goen-pdp__links`) {
		t.Error("the compare toggle is not in the add's row")
	}

	// No similar product on the shelf: the toggle, and no link to a
	// comparison of one.
	alone := renderToString(t, Product(ProductMeta(view(true)), view(true)))
	if !strings.Contains(alone, `<form class="goen-comparetoggle"`) || strings.Contains(alone, "goen-pdp__compare") {
		t.Error("a comparable product alone on its shelf does not show the toggle alone")
	}

	// The page's own content: the header's link to compare is on every page.
	_, off, _ := strings.Cut(renderToString(t, Product(ProductMeta(view(false)), view(false))), `<main id="main">`)
	off, _, _ = strings.Cut(off, "</main>")
	for _, bad := range []string{"/compare", "goen-pdp__compare", "goen-comparetoggle", "與同類商品比較"} {
		if strings.Contains(off, bad) {
			t.Errorf("a department that does not compare shows %q", bad)
		}
	}
}

// TestProductComparisonNoSpecs holds that the link to a ready-made comparison
// falls back to the details when there are no specifications.
//
// It must not vanish with the specs section it normally sits in.
func TestProductComparisonNoSpecs(t *testing.T) {
	t.Parallel()
	v := &ProductView{Name: "N", Slug: "n", Comparable: true, Similar: []string{"m"}, SelectionOK: true}
	if html := renderToString(t, Product(ProductMeta(v), v)); !strings.Contains(html, `href="/compare?p=n&amp;p=m"`) {
		t.Error("a comparable product with no specs has no way into a comparison")
	}
}
