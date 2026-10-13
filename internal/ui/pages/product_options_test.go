package pages

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestPartiallySoldOutTextOptionHasAVisibleMark(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		view := ProductView{Slug: "book", Name: "Book", SelectionOK: true, Exact: true, AnySellable: true,
			Options: []ProductOption{{Name: "capacity", Label: "Capacity", Values: []ProductOptionValue{
				{Value: "128", Label: "128GB", Available: true, Href: "/p/book?capacity=128"},
				{Value: "256", Label: "256GB", Selected: true, Href: "/p/book?capacity=256"},
			}}}}
		doc, err := html.Parse(strings.NewReader(buyBox(t, locale, &view)))
		if err != nil {
			t.Fatal(err)
		}
		option := findDescendant(doc, func(n *html.Node) bool { return n.Data == "a" && attrValue(n, "href") == "/p/book?capacity=256" })
		if option == nil || findDescendant(option, func(n *html.Node) bool {
			return n.Data == "small" && attrValue(n, "class") == "goen-swatch__note"
		}) == nil {
			t.Errorf("%s: the selected sold-out option has no visible sold-out mark", locale)
		}
		if option == nil || findDescendant(option, func(n *html.Node) bool { return n.Data == "s" }) == nil {
			t.Errorf("%s: the selected sold-out option is not struck through", locale)
		}
	}
}

func TestSoldOutProductHasNoQuantityStepper(t *testing.T) {
	t.Parallel()
	view := soldOutProduct(nil)
	box := buyBox(t, i18n.En, &view)
	if strings.Contains(box, `id="quantity"`) {
		t.Error("a sold-out product still offers a quantity stepper")
	}
	if !strings.Contains(box, `id="restock"`) {
		t.Error("a sold-out product has no restock action")
	}
}

func TestUnpickedColourAndEmptyRelatedProductsOfferNextSteps(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		view := ProductView{Slug: "book", Name: "Book", CategorySlug: "computers", CategoryName: "Computers", SelectionOK: true, AnySellable: true,
			Options: []ProductOption{{Name: "colour", Label: "Colour", Values: []ProductOptionValue{
				{Value: "white", Label: "White", Available: true, Href: "/p/book?colour=white", SwatchHex: "#ffffff"},
				{Value: "black", Label: "Black", Available: true, Href: "/p/book?colour=black", SwatchHex: "#18181b"},
			}}}}
		page := buyBox(t, locale, &view)
		pick, category := "請選擇", "瀏覽「Computers」全部商品"
		if locale == i18n.En {
			pick, category = "Please choose", "Browse all Computers products"
		}
		doc, err := html.Parse(strings.NewReader(page))
		if err != nil {
			t.Fatal(err)
		}
		legend := findDescendant(doc, func(n *html.Node) bool { return n.Data == "legend" })
		if legend == nil || findDescendant(legend, func(n *html.Node) bool { return n.Type == html.TextNode && n.Data == pick }) == nil {
			t.Errorf("%s: an unpicked colour has no instruction", locale)
		}
		if !strings.Contains(page, `href="/c/computers"`) || !strings.Contains(page, category) {
			t.Errorf("%s: the page has no category continuation", locale)
		}
	}
}
