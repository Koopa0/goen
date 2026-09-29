package admin

import (
	"bytes"
	"fmt"
	"html"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

// firstPageBound is the ListBound the stores build for the first page of a
// queue that has no rows, exactly as production builds it.
func firstPageBound(scope string) pages.ListBound {
	_, b := pageBound(readPageCursor(scope, []string{""}), scope, []db.Order{}, PageSize, func(*db.Order) string { return "" })
	return b
}

// fullPageBound is the first page of a queue with more rows behind it.
func fullPageBound(scope string) pages.ListBound {
	rows := make([]db.Order, PageLimit)
	_, b := pageBound(readPageCursor(scope, []string{""}), scope, rows, PageSize,
		func(*db.Order) string { return `{"ID":"12345678-1234-1234-1234-123456789abc"}` })
	return b
}

func render(t *testing.T, c templ.Component) string {
	t.Helper()
	var out bytes.Buffer
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.En), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// queuePages builds each queue's page around one ListBound, with no rows.
func queuePages(b pages.ListBound, ctx func(k i18n.Key) string) map[string]struct {
	page templ.Component
	want string
} {
	p := layouts.Page{Title: "Queue"}
	type c = struct {
		page templ.Component
		want string
	}
	term := "ZZ99"
	return map[string]c{
		"orders, no filter":  {pages.AdminOrders(p, pages.AdminOrdersView{ListBound: b}), ctx(i18n.KeyAdminQueueNoneYet)},
		"orders, status tab": {pages.AdminOrders(p, pages.AdminOrdersView{ListBound: b, Status: "pending"}), ctx(i18n.KeyAdminQueueEmpty)},
		"orders, search":     {pages.AdminOrders(p, pages.AdminOrdersView{ListBound: b, Searched: true, Term: term}), fmt.Sprintf(ctx(i18n.KeyAdminQueueNoneFound), term)},
		"customers":          {pages.AdminCustomers(p, pages.AdminCustomersView{ListBound: b, Searched: true, Term: term}), fmt.Sprintf(ctx(i18n.KeyAdminCustNoneFound), term)},
		"warranty":           {pages.AdminWarranties(p, pages.AdminWarrantiesView{ListBound: b, Searched: true, Term: term}), fmt.Sprintf(ctx(i18n.KeyAdminWarrantyNoneFound), term)},
		"products":           {pages.AdminProducts(p, pages.AdminProductsView{ListBound: b}), ctx(i18n.KeyAdminProdEmpty)},
		"stock":              {pages.AdminVariants(p, pages.AdminVariantsView{ListBound: b}), ctx(i18n.KeyAdminQueueNoVariants)},
		"movements":          {pages.AdminMovements(p, &pages.AdminMovementsView{ListBound: b}), ctx(i18n.KeyAdminLedgerEmpty)},
		"returns":            {pages.AdminReturns(p, pages.AdminReturnsView{ListBound: b}), ctx(i18n.KeyAdminRetEmpty)},
		"coupons":            {pages.AdminCoupons(p, pages.AdminCouponsView{ListBound: b}), ctx(i18n.KeyAdminCoupEmpty)},
		"campaigns":          {pages.AdminCampaigns(p, pages.AdminCampaignsView{ListBound: b}), ctx(i18n.KeyAdminCampEmpty)},
		"reviews":            {pages.AdminReviews(p, pages.AdminReviewsView{ListBound: b}), ctx(i18n.KeyAdminReviewsEmpty)},
		"messages":           {pages.AdminMessages(p, pages.AdminMessagesView{ListBound: b}), ctx(i18n.KeyAdminMessagesEmpty)},
		"credit":             {pages.AdminCredit(p, pages.AdminCreditView{ListBound: b}), ctx(i18n.KeyAdminCreditEmpty)},
		"audit":              {pages.AdminAudit(p, pages.AuditView{ListBound: b}), ctx(i18n.KeyAdminAuditEmpty)},
	}
}

// A queue with nothing in it on its first page says what that means for that
// queue; the generic "no entries on this page" is only for a later page.
func TestEveryQueueKeepsItsOwnFirstPageEmptyState(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	generic := ctx(i18n.KeyPageEmpty)
	for name, c := range queuePages(firstPageBound("/admin/queue"), ctx) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := render(t, c.page)
			if !strings.Contains(body, html.EscapeString(c.want)) {
				t.Errorf("the queue's own empty text %q is missing", c.want)
			}
			if strings.Contains(body, generic) {
				t.Error("an empty first page shows the later-page sentence")
			}
		})
	}
}

// A full first page points at the next one.
func TestEveryQueueWithMoreRowsOffersNext(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	for name, c := range queuePages(fullPageBound("/admin/queue"), ctx) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := render(t, c.page)
			if !strings.Contains(body, `rel="next"`) {
				t.Error("a first page with more rows has no Next link")
			}
			if strings.Contains(body, ctx(i18n.KeyPageEmpty)) {
				t.Error("a first page with more rows says it is empty")
			}
		})
	}
}
