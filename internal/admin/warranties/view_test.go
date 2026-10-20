package warranties

import (
	"fmt"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// From TestEveryAdminEmptyPageKeepsItsRestartLink (row warranty).
func TestWarrantyEmptyPageKeepsItsRestartLink(t *testing.T) {
	t.Parallel()
	b := keyset.Bound{PastEnd: true, First: "/admin/products?q=test"}
	body := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En),
		Warranties(layouts.Page{Title: "Pagination"}, WarrantiesView{Bound: b, Searched: true, Term: "test"}))
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
	return Warranties(layouts.Page{Title: "Queue"}, WarrantiesView{Bound: b, Searched: true, Term: term}),
		fmt.Sprintf(ctx(i18n.KeyAdminWarrantyNoneFound), term)
}

// From TestEveryQueueKeepsItsOwnFirstPageEmptyState and
// TestEveryQueueWithMoreRowsOffersNext (row warranty).
func TestWarrantyQueueKeepsItsOwnFirstPageEmptyState(t *testing.T) {
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

func TestWarrantyQueueWithMoreRowsOffersNext(t *testing.T) {
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
