package credits

import (
	"bytes"
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/frame"
)

// The tests in this file are the credit halves of tests that render several
// desks' pages: each half lives with the page it renders.

// From TestEveryAdminEmptyPageKeepsItsRestartLink.
func TestCreditEmptyPageKeepsItsRestartLink(t *testing.T) {
	t.Parallel()
	b := keyset.Bound{PastEnd: true, First: "/admin/products?q=test"}
	var out bytes.Buffer
	if err := Credit(frame.Page{Title: "Pagination"}, CreditView{Bound: b}).Render(i18n.WithLocale(t.Context(), i18n.En), &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, `href="/admin/products?q=test"`) || !strings.Contains(body, "First page") {
		t.Fatal("empty page lost its plain restart link")
	}
	if strings.Count(body, "There are no entries on this page.") != 1 {
		t.Fatal("empty page lost its shared empty state")
	}
}

// From TestEveryQueueKeepsItsOwnFirstPageEmptyState and
// TestEveryQueueWithMoreRowsOffersNext.
func TestCreditQueueKeepsItsOwnFirstPageEmptyState(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	generic := ctx(i18n.KeyPageEmpty)
	_, b := keyset.PageBound("/admin/queue", false, []db.Order{}, keyset.PageSize, func(*db.Order) string { return "" })
	body := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), Credit(frame.Page{Title: "Queue"}, CreditView{Bound: b}))
	if !strings.Contains(body, html.EscapeString(ctx(i18n.KeyAdminCreditEmpty))) {
		t.Errorf("the queue's own empty text %q is missing", ctx(i18n.KeyAdminCreditEmpty))
	}
	if strings.Contains(body, generic) {
		t.Error("an empty first page shows the later-page sentence")
	}
}

func TestCreditQueueWithMoreRowsOffersNext(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	rows := make([]db.Order, keyset.PageLimit)
	_, b := keyset.PageBound("/admin/queue", false, rows, keyset.PageSize,
		func(*db.Order) string { return `{"ID":"12345678-1234-1234-1234-123456789abc"}` })
	body := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), Credit(frame.Page{Title: "Queue"}, CreditView{Bound: b}))
	if !strings.Contains(body, `rel="next"`) {
		t.Error("a first page with more rows has no Next link")
	}
	if strings.Contains(body, ctx(i18n.KeyPageEmpty)) {
		t.Error("a first page with more rows says it is empty")
	}
}
