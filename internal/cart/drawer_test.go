package cart

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

// drawerFacts is what the bag drawer offers, read from its markup.
type drawerFacts struct {
	Root     bool
	Forms    int
	Fields   int
	Links    []string
	Close    string
	Subtotal string
	Hold     string
	Meter    bool
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func readDrawer(t *testing.T, locale i18n.Locale, v View) drawerFacts {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(renderIn(t, locale, Drawer(v))))
	if err != nil {
		t.Fatal(err)
	}
	var got drawerFacts
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		class := " " + attr(n, "class") + " "
		switch {
		case attr(n, "id") == "bag-drawer-body":
			got.Root = true
		case n.Data == "form":
			got.Forms++
		case n.Data == "input" || n.Data == "button" && !hasAttr(n, "data-bag-close"):
			got.Fields++
		case n.Data == "a":
			got.Links = append(got.Links, attr(n, "href"))
		case n.Data == "button":
			got.Close = attr(n, "type") + " " + attr(n, "aria-label")
		case n.Data == "dd" && strings.Contains(class, " goen-summary__value "):
			got.Subtotal = strings.TrimSpace(text(n))
		case n.Data == "p" && strings.Contains(class, " goen-cart__alert "):
			got.Hold = strings.TrimSpace(text(n))
		case strings.Contains(class, " goen-meter "):
			got.Meter = true
		}
	}
	return got
}

// TestDrawerReadOnly holds that the drawer is a reading of the cart and not a
// second place to change it: no form and no field, the lines, the subtotal and
// the meter, the cart and checkout as links, and a close button named for a
// screen reader, since it shows only a cross.
func TestDrawerReadOnly(t *testing.T) {
	t.Parallel()
	v := View{
		Lines: []Line{
			{VariantID: "a", Slug: "mug", Name: "Mug", SKU: "RWD-MUG-1", Quantity: 2, Available: 9, UnitCents: 43200},
			{VariantID: "b", Slug: "tea", Name: "Tea", Quantity: 1, Available: 9, UnitCents: 58500},
		},
		ItemCount: 3, SubtotalCents: 144900, PayStartMinutes: 29,
		FreeDelivery: FreeDelivery{Kind: FreeDeliveryShort, ShortfallCents: 155100, ThresholdCents: 300000},
	}
	got := readDrawer(t, i18n.En, v)
	want := drawerFacts{
		Root:     true,
		Links:    []string{"/p/mug", "/p/tea", "/checkout", "/cart"},
		Close:    "button Close the bag",
		Subtotal: "NT$1,449",
		Meter:    true,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the drawer (-want +got):\n%s", diff)
	}
	markup := renderIn(t, i18n.ZhHant, Drawer(v))
	for _, said := range []string{"3 件商品", "2 × NT$432", "RWD-MUG-1", "免運還差 NT$1,551", "請在 29 分鐘內開始付款"} {
		if !strings.Contains(markup, said) {
			t.Errorf("the drawer does not say %q", said)
		}
	}
}

// TestDrawerHoldsCheckout holds that the drawer offers checkout only where the
// cart page does, and otherwise says why in the cart page's own sentence, with
// the way to the cart still there.
func TestDrawerHoldsCheckout(t *testing.T) {
	t.Parallel()
	line := Line{VariantID: "a", Slug: "mug", Name: "Mug", Quantity: 1, Available: 9, UnitCents: 43200}
	soldOut := line
	soldOut.Unavailable = true
	for _, tt := range []struct {
		name string
		v    View
		hold i18n.Key
	}{
		{"a line sold out", View{Lines: []Line{soldOut}}, i18n.KeyCartSoldOut},
		{"taxable and exempt together", View{Lines: []Line{line}, ItemCount: 1, SubtotalCents: 43200, MixedTaxTypes: true}, i18n.KeyCartMixedTaxTypes},
		{"no delivery method", View{Lines: []Line{line}, ItemCount: 1, SubtotalCents: 43200, NoDelivery: true}, i18n.KeyShippingUnavailable},
	} {
		got := readDrawer(t, i18n.ZhHant, tt.v)
		want := i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), tt.hold)
		if got.Hold != want {
			t.Errorf("%s: the drawer says %q, want %q", tt.name, got.Hold, want)
		}
		for _, href := range got.Links {
			if href == "/checkout" {
				t.Errorf("%s: the drawer offers checkout", tt.name)
			}
		}
		if got.Links[len(got.Links)-1] != "/cart" {
			t.Errorf("%s: the drawer's last way on is %q, not the cart", tt.name, got.Links[len(got.Links)-1])
		}
	}
}

// TestDrawerEmptyCart holds that a visitor with no cart, or an empty one, is
// shown the empty cart's sentence and nothing to check out.
func TestDrawerEmptyCart(t *testing.T) {
	t.Parallel()
	got := readDrawer(t, i18n.ZhHant, View{})
	if got.Links != nil || got.Subtotal != "" || got.Meter {
		t.Errorf("an empty drawer draws %+v", got)
	}
	if markup := renderIn(t, i18n.ZhHant, Drawer(View{})); !strings.Contains(markup, i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyCartEmptyDesc)) {
		t.Error("an empty drawer does not say the cart is empty")
	}
}
