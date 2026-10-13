package admin

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestOrderStatusNavigationIsNamedInBothLanguages(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale i18n.Locale
		label  string
	}{
		{i18n.ZhHant, "訂單狀態"},
		{i18n.En, "Order status"},
	} {
		t.Run(string(tc.locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tc.locale)
			view := OrdersView{
				Status: QueuePicking,
				Tabs: []StatusTab{
					{Value: QueueAll, Label: i18n.T(ctx, i18n.KeyAdminTabAll), Count: 3},
					{Value: QueuePicking, Label: i18n.T(ctx, i18n.KeyAdminStatusPicking), Count: 2, Selected: true},
				},
			}
			body := renderComponent(t, ctx, Orders(OrdersMeta(ctx), view))
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			var navigation *html.Node
			if !hasElement(doc, func(n *html.Node) bool {
				if n.Data != "nav" || attr(n, "class") != "ui-filterbar goen-admin__tabs" {
					return false
				}
				navigation = n
				return true
			}) {
				t.Fatal("order status links have no navigation landmark")
			}
			if got := attr(navigation, "aria-label"); got != tc.label {
				t.Errorf("order status navigation label = %q, want %q", got, tc.label)
			}
			for _, link := range []struct {
				href    string
				current string
				count   string
			}{
				{href: "/admin/orders?status=", count: "3"},
				{href: "/admin/orders?status=picking", current: "page", count: "2"},
			} {
				if !hasElement(navigation, func(n *html.Node) bool {
					return n.Data == "a" && attr(n, "href") == link.href && attr(n, "aria-current") == link.current &&
						hasElement(n, func(count *html.Node) bool {
							return count.Data == "span" && attr(count, "class") == "ui-filter__count" && textOf(count) == link.count
						})
				}) {
					t.Errorf("order status navigation lacks link %q with aria-current=%q and count %q", link.href, link.current, link.count)
				}
			}
		})
	}
}
