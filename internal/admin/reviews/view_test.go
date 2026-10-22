package reviews

import (
	"bytes"
	"html"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/frame"
)

// The tests in this file are this desk's halves of tests that render several
// desks' pages: each half lives with the pages it renders.

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

// From TestEveryAdminEmptyPageKeepsItsRestartLink.
func TestReviewsEmptyPageKeepsItsRestartLink(t *testing.T) {
	t.Parallel()
	b := keyset.Bound{PastEnd: true, First: "/admin/products?q=test"}
	body := render(t, Reviews(frame.Page{Title: "Pagination"}, ReviewsView{Bound: b}))
	if !strings.Contains(body, `href="/admin/products?q=test"`) || !strings.Contains(body, "First page") {
		t.Fatal("empty page lost its plain restart link")
	}
	if strings.Count(body, "There are no entries on this page.") != 1 {
		t.Fatal("empty page lost its shared empty state")
	}
}

// From TestEveryQueueKeepsItsOwnFirstPageEmptyState.
func TestReviewsQueueKeepsItsOwnFirstPageEmptyState(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	want := ctx(i18n.KeyAdminReviewsEmpty)
	body := render(t, Reviews(frame.Page{Title: "Queue"}, ReviewsView{Bound: firstPageBound("/admin/queue")}))
	if !strings.Contains(body, html.EscapeString(want)) {
		t.Errorf("the queue's own empty text %q is missing", want)
	}
	if strings.Contains(body, ctx(i18n.KeyPageEmpty)) {
		t.Error("an empty first page shows the later-page sentence")
	}
}

// From TestEveryQueueWithMoreRowsOffersNext.
func TestReviewsQueueWithMoreRowsOffersNext(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	body := render(t, Reviews(frame.Page{Title: "Queue"}, ReviewsView{Bound: fullPageBound("/admin/queue")}))
	if !strings.Contains(body, `rel="next"`) {
		t.Error("a first page with more rows has no Next link")
	}
	if strings.Contains(body, ctx(i18n.KeyPageEmpty)) {
		t.Error("a first page with more rows says it is empty")
	}
}
