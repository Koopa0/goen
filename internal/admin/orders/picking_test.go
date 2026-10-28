package orders

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestPickingSlipsReadOnly: the picking page renders separate read-only slips
// with their outstanding quantities.
func TestPickingSlipsReadOnly(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		v := &PickingView{
			Bound:  keyset.Bound{Next: "/admin/orders/picking/slips?after=next"},
			Totals: []PickingLine{{SKU: "SKU-A", Name: "商品 <A>", Label: "容量 256 GB", Remaining: 7}},
			Slips: []*OrderView{
				{OutstandingQuantities: true, Number: "GO-ONE", CustomerNote: "Gift <note>: no price inside", Recipient: "顧客 <一>", Address: "臺北市 1 號", Phone: "0912345678", Lines: []Line{{SKU: "SKU-A", Name: "商品 <A>", Quantity: 2, UnitCents: 100}}},
				{OutstandingQuantities: true, Number: "GO-TWO", Recipient: "顧客二", Address: "臺南市 2 號", Lines: []Line{{SKU: "SKU-A", Name: "商品 <A>", Quantity: 5, UnitCents: 100}}},
			},
		}
		out := renderComponent(t, ctx, Picking(frame.Page{Title: "Picking"}, v))
		if strings.Contains(out, `class="goen-admin__linetotal"`) {
			t.Errorf("%s: outstanding quantities printed a line total absent from the purchased order", locale)
		}
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
		for _, want := range []string{"商品 &lt;A&gt;", "顧客 &lt;一&gt;", "Gift &lt;note&gt;: no price inside", "容量 256 GB", "臺南市 2 號", "× 2", "× 5", v.ScopeText(ctx), "?after=next"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: missing %q", locale, want)
			}
		}
		if strings.Count(out, `class="goen-order__line"`) != 2 || strings.Count(out, `class="ui-dl__row"`) != 8 {
			t.Errorf("%s: each slip lost its shared items or delivery block", locale)
		}
	}
}

// TestOrdersPickingLink: the picking link appears only on the picking tab, and
// only when the tab has orders (or is a later page of one that has).
func TestOrdersPickingLink(t *testing.T) {
	t.Parallel()
	packing := workView(QueuePicking, numberN(1))
	searched := View{Status: QueuePicking, Term: "GO-ONE", Searched: true}
	pastEnd := View{Status: QueuePicking, Bound: keyset.Bound{PastEnd: true, First: "/admin/orders?status=picking"}}
	for name, tt := range map[string]struct {
		v    View
		want bool
	}{
		"packing with orders":   {packing, true},
		"packing, empty":        {View{Status: QueuePicking}, false},
		"packing, past its end": {pastEnd, true},
		"packing, searched":     {searched, false},
		"ready with orders":     {workView(QueueReady, numberN(1)), false},
		"all":                   {View{Status: QueueAll}, false},
	} {
		out := renderToString(t, Orders(frame.Page{Title: "Orders"}, tt.v))
		if got := strings.Contains(out, `href="/admin/orders/picking/slips"`); got != tt.want {
			t.Errorf("%s: picking link=%t, want %t", name, got, tt.want)
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
