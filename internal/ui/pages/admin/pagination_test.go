package admin

import (
	"bytes"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestEveryAdminEmptyPageKeepsItsRestartLink(t *testing.T) {
	t.Parallel()
	b := pages.ListBound{PastEnd: true, First: "/admin/products?q=test"}
	p := layouts.Page{Title: "Pagination"}
	cases := map[string]templ.Component{
		"orders":    Orders(p, OrdersView{ListBound: b}),
		"customers": Customers(p, CustomersView{ListBound: b, Searched: true, Term: "test"}),
		"products":  Products(p, ProductsView{ListBound: b}),
		"stock":     Variants(p, VariantsView{ListBound: b}),
		"movements": Movements(p, &MovementsView{ListBound: b}),
		"returns":   Returns(p, ReturnsView{ListBound: b}),
		"coupons":   Coupons(p, CouponsView{ListBound: b}),
		"campaigns": Campaigns(p, CampaignsView{ListBound: b}),
		"reviews":   Reviews(p, ReviewsView{ListBound: b}),
		"messages":  Messages(p, MessagesView{ListBound: b}),
		"credit":    Credit(p, CreditView{ListBound: b}),
		"audit":     Audit(p, AuditView{ListBound: b}),
		"warranty":  Warranties(p, WarrantiesView{ListBound: b, Searched: true, Term: "test"}),
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
