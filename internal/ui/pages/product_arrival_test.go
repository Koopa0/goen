package pages

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
)

func TestExpectedArrivalPrecedesNotificationWithoutOfferingAStocklessPurchase(t *testing.T) {
	t.Parallel()
	day, ok := shoptime.ParseInputDay("2028-10-15")
	if !ok {
		t.Fatal("invalid fixture date")
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			v := ProductView{Slug: "arrival", SelectionOK: true, Exact: true, VariantID: "v", SKU: "A", ExpectedArrival: day}
			markup := renderComponent(t, ctx, productBuy(&v))
			arrival := strings.Index(markup, v.ArrivalText(ctx))
			notify := strings.Index(markup, `action="/p/arrival/notify"`)
			if arrival < 0 || notify < arrival {
				t.Fatal("arrival date is not before the notification form")
			}
			if !strings.Contains(markup, `datetime="2028-10-15"`) {
				t.Error("calendar date missing")
			}
			doc, err := html.Parse(strings.NewReader(markup))
			if err != nil {
				t.Fatal(err)
			}
			disabled := false
			var visit func(*html.Node)
			visit = func(n *html.Node) {
				attrs := map[string]string{}
				for _, a := range n.Attr {
					attrs[a.Key] = a.Val
				}
				if n.Data == "button" && attrs["id"] == "add-to-cart" {
					_, disabled = attrs["disabled"]
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					visit(c)
				}
			}
			visit(doc)
			if !disabled || v.CanBuy() {
				t.Error("arrival date enabled a stockless purchase")
			}
			for _, modify := range []func(){
				func() { v.Sellable = true },
				func() { v.Sellable = false; v.Exact = false },
				func() { v.Exact = true; v.ExpectedArrival = time.Time{} },
			} {
				modify()
				if v.ArrivalText(ctx) != "" {
					t.Error("arrival shown for an in-stock, unresolved or undated variant")
				}
			}
		})
	}
}
