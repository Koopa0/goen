package customers

import (
	"fmt"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/widget"
)

// badgeClasses returns the class attribute of every badge whose text contains word.
func badgeClasses(t *testing.T, page, word string) []string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out []string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			class := attr(n, "class")
			if strings.HasPrefix(class, "goen-badge") && strings.Contains(textOf(n), word) {
				out = append(out, class)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

func wantBadge(t *testing.T, page, word, class string) {
	t.Helper()
	got := badgeClasses(t, page, word)
	if len(got) == 0 {
		t.Errorf("no badge says %q", word)
		return
	}
	for _, c := range got {
		if c != class {
			t.Errorf("badge %q has class %q, want %q", word, c, class)
		}
	}
}

// From TestDashboardAndCustomerRecentOrdersFollowTheirGroup, which keeps the dashboard half.
func TestCustomerRecentOrdersFollowTheirGroup(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	recent := RecentOrder{Number: "GO-A", Status: order.FulfillmentPending, StatusText: "待出貨", StatusIntent: widget.IntentWarn}
	customer := renderComponent(t, ctx, Customer(Meta(ctx), &CustomerView{Orders: 1, Recent: []RecentOrder{recent}}))
	wantBadge(t, customer, "待出貨", "goen-badge goen-badge--warn")
}

// From TestEveryAdminEmptyPageKeepsItsRestartLink (row customers).
func TestCustomerEmptyPageKeepsItsRestartLink(t *testing.T) {
	t.Parallel()
	b := keyset.Bound{PastEnd: true, First: "/admin/products?q=test"}
	body := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En),
		Customers(layouts.Page{Title: "Pagination"}, CustomersView{Bound: b, Searched: true, Term: "test"}))
	if !strings.Contains(body, `href="/admin/products?q=test"`) || !strings.Contains(body, "First page") {
		t.Fatal("empty page lost its plain restart link")
	}
	if strings.Count(body, "There are no entries on this page.") != 1 {
		t.Fatal("empty page lost its shared empty state")
	}
}

// queuePage builds the search page around one bound, with no rows, and the
// sentence its own empty first page says.
func queuePage(b keyset.Bound, ctx func(k i18n.Key) string) (page templ.Component, empty string) {
	term := "ZZ99"
	return Customers(layouts.Page{Title: "Queue"}, CustomersView{Bound: b, Searched: true, Term: term}),
		fmt.Sprintf(ctx(i18n.KeyAdminCustNoneFound), term)
}

// From TestEveryQueueKeepsItsOwnFirstPageEmptyState and
// TestEveryQueueWithMoreRowsOffersNext (row customers).
func TestCustomerQueueKeepsItsOwnFirstPageEmptyState(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	generic := ctx(i18n.KeyPageEmpty)
	_, b := keyset.PageBound("/admin/queue", false, []db.Order{}, keyset.PageSize, func(*db.Order) string { return "" })
	page, want := queuePage(b, ctx)
	body := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), page)
	if !strings.Contains(body, html.EscapeString(want)) {
		t.Errorf("the queue's own empty text %q is missing", want)
	}
	if strings.Contains(body, generic) {
		t.Error("an empty first page shows the later-page sentence")
	}
}

func TestCustomerQueueWithMoreRowsOffersNext(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	rows := make([]db.Order, keyset.PageLimit)
	_, b := keyset.PageBound("/admin/queue", false, rows, keyset.PageSize,
		func(*db.Order) string { return `{"ID":"12345678-1234-1234-1234-123456789abc"}` })
	page, _ := queuePage(b, ctx)
	body := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), page)
	if !strings.Contains(body, `rel="next"`) {
		t.Error("a first page with more rows has no Next link")
	}
	if strings.Contains(body, ctx(i18n.KeyPageEmpty)) {
		t.Error("a first page with more rows says it is empty")
	}
}
