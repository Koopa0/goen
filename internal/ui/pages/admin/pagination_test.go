package admin

import (
	"bytes"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/web"
)

func TestEveryAdminEmptyPageKeepsItsRestartLink(t *testing.T) {
	t.Parallel()
	b := web.Bound{PastEnd: true, First: "/admin/products?q=test"}
	p := layouts.Page{Title: "Pagination"}
	cases := map[string]templ.Component{
		"orders":    Orders(p, OrdersView{Bound: b}),
		"customers": Customers(p, CustomersView{Bound: b, Searched: true, Term: "test"}),
		"products":  Products(p, ProductsView{Bound: b}),
		"stock":     Variants(p, VariantsView{Bound: b}),
		"movements": Movements(p, &MovementsView{Bound: b}),
		"returns":   Returns(p, ReturnsView{Bound: b}),
		"coupons":   Coupons(p, CouponsView{Bound: b}),
		"campaigns": Campaigns(p, CampaignsView{Bound: b}),
		"reviews":   Reviews(p, ReviewsView{Bound: b}),
		"messages":  Messages(p, MessagesView{Bound: b}),
		"credit":    Credit(p, CreditView{Bound: b}),
		"audit":     Audit(p, AuditView{Bound: b}),
		"warranty":  Warranties(p, WarrantiesView{Bound: b, Searched: true, Term: "test"}),
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			if err := c.Render(i18n.WithLocale(t.Context(), i18n.En), &out); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			if !strings.Contains(body, `href="/admin/products?q=test"`) || !strings.Contains(body, "First page") {
				t.Fatal("empty page lost its plain restart link")
			}
			if strings.Count(body, "There are no entries on this page.") != 1 {
				t.Fatal("empty page lost its shared empty state")
			}
		})
	}
}
