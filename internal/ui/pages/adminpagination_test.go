package pages

import (
	"bytes"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestEveryAdminEmptyPageKeepsItsRestartLink(t *testing.T) {
	t.Parallel()
	b := ListBound{Paged: true, Empty: true, First: "/admin/products?q=test"}
	p := layouts.Page{Title: "Pagination"}
	cases := map[string]templ.Component{
		"orders":    AdminOrders(p, AdminOrdersView{ListBound: b}),
		"customers": AdminCustomers(p, AdminCustomersView{ListBound: b, Searched: true, Term: "test"}),
		"products":  AdminProducts(p, AdminProductsView{ListBound: b}),
		"stock":     AdminVariants(p, AdminVariantsView{ListBound: b}),
		"movements": AdminMovements(p, &AdminMovementsView{ListBound: b}),
		"returns":   AdminReturns(p, AdminReturnsView{ListBound: b}),
		"coupons":   AdminCoupons(p, AdminCouponsView{ListBound: b}),
		"campaigns": AdminCampaigns(p, AdminCampaignsView{ListBound: b}),
		"reviews":   AdminReviews(p, AdminReviewsView{ListBound: b}),
		"messages":  AdminMessages(p, AdminMessagesView{ListBound: b}),
		"credit":    AdminCredit(p, AdminCreditView{ListBound: b}),
		"audit":     AdminAudit(p, AuditView{ListBound: b}),
		"warranty":  AdminWarranties(p, AdminWarrantiesView{ListBound: b, Searched: true, Term: "test"}),
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
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
