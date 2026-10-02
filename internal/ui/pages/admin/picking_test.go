package admin

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestPickingRendersSeparateReadOnlySlipsWithOutstandingQuantities(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		v := &PickingView{
			ListBound: pages.ListBound{Next: "/admin/orders/picking/slips?after=next"},
			Totals:    []PickingLine{{SKU: "SKU-A", Name: "商品 <A>", Label: "容量 256 GB", Remaining: 7}},
			Slips: []*OrderView{
				{Number: "GO-ONE", Recipient: "顧客 <一>", Address: "臺北市 1 號", Phone: "0912345678", Lines: []pages.OrderLine{{SKU: "SKU-A", Name: "商品 <A>", Quantity: 2, UnitCents: 100}}},
				{Number: "GO-TWO", Recipient: "顧客二", Address: "臺南市 2 號", Lines: []pages.OrderLine{{SKU: "SKU-A", Name: "商品 <A>", Quantity: 5, UnitCents: 100}}},
			},
		}
		out := renderComponent(t, ctx, Picking(layouts.Page{Title: "Picking"}, v))
		doc, err := html.Parse(strings.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		var slips, forms int
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode {
				if n.Data == "form" && pickingFormWithinMain(n) {
					forms++
				}
				for _, a := range n.Attr {
					if a.Key == "class" && a.Val == "goen-admin__slip" {
						slips++
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		if slips != 2 || forms != 0 {
			t.Fatalf("%s: slips=%d forms=%d, want two read-only slips", locale, slips, forms)
		}
		for _, want := range []string{"商品 &lt;A&gt;", "顧客 &lt;一&gt;", "容量 256 GB", "臺南市 2 號", "× 2", "× 5", i18n.T(ctx, i18n.KeyAdminPickingScope), "?after=next"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: missing %q", locale, want)
			}
		}
		if strings.Count(out, `class="goen-order__line"`) != 2 || strings.Count(out, `class="ui-dl__row"`) != 8 {
			t.Errorf("%s: each slip lost its shared items or delivery block", locale)
		}
	}
}

func TestPickingLinkAppearsOnlyOnThePickingTab(t *testing.T) {
	t.Parallel()
	for _, v := range []OrdersView{{Status: QueuePicking}, {Status: QueueReady}, {Status: QueueAll}, {Status: QueuePicking, Term: "GO-ONE", Searched: true}} {
		out := renderToString(t, Orders(layouts.Page{Title: "Orders"}, v))
		got := strings.Contains(out, `href="/admin/orders/picking/slips"`)
		if want := v.Status == QueuePicking && !v.Searched; got != want {
			t.Errorf("status=%q searched=%t: picking link=%t, want %t", v.Status, v.Searched, got, want)
		}
	}
}

func pickingFormWithinMain(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Data == "main" {
			return true
		}
	}
	return false
}
