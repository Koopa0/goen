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
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// firstPageBound is the bound the stores build for the first page of a
// queue that has no rows, exactly as production builds it.
func firstPageBound(scope string) keyset.Bound {
	_, b := keyset.PageBound(scope, false, []db.Order{}, keyset.PageSize, func(*db.Order) string { return "" })
	return b
}

// fullPageBound is the first page of a queue with more rows behind it.
func fullPageBound(scope string) keyset.Bound {
	rows := make([]db.Order, keyset.PageLimit)
	_, b := keyset.PageBound(scope, false, rows, keyset.PageSize,
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

// queuePages builds each queue's page around one bound, with no rows.
func queuePages(b keyset.Bound, ctx func(k i18n.Key) string) map[string]struct {
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
		"orders, no filter":  {Orders(p, OrdersView{Bound: b}), ctx(i18n.KeyAdminQueueNoneYet)},
		"orders, status tab": {Orders(p, OrdersView{Bound: b, Status: "pending"}), ctx(i18n.KeyAdminQueueEmpty)},
		"orders, search":     {Orders(p, OrdersView{Bound: b, Searched: true, Term: term}), fmt.Sprintf(ctx(i18n.KeyAdminQueueNoneFound), term)},
		"customers":          {Customers(p, CustomersView{Bound: b, Searched: true, Term: term}), fmt.Sprintf(ctx(i18n.KeyAdminCustNoneFound), term)},
		"warranty":           {Warranties(p, WarrantiesView{Bound: b, Searched: true, Term: term}), fmt.Sprintf(ctx(i18n.KeyAdminWarrantyNoneFound), term)},
		"products":           {Products(p, ProductsView{Bound: b}), ctx(i18n.KeyAdminProdEmpty)},
		"returns":            {Returns(p, ReturnsView{Bound: b}), ctx(i18n.KeyAdminRetEmpty)},
		"campaigns":          {Campaigns(p, CampaignsView{Bound: b}), ctx(i18n.KeyAdminCampEmpty)},
		"audit":              {Audit(p, AuditView{Bound: b}), ctx(i18n.KeyAdminAuditEmpty)},
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
