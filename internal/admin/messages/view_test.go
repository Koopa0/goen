package messages

import (
	"bytes"
	"fmt"
	"html"
	"strings"
	"testing"

	"github.com/a-h/templ"
	xhtml "golang.org/x/net/html"

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

// badgeClasses returns the class attribute of every badge whose text contains word.
func badgeClasses(t *testing.T, page, word string) []string {
	t.Helper()
	root, err := xhtml.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out []string
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
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

func textOf(n *xhtml.Node) string {
	if n.Type == xhtml.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textOf(c))
	}
	return strings.TrimSpace(b.String())
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

func render(t *testing.T, c templ.Component) string {
	t.Helper()
	var out bytes.Buffer
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.En), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// From TestEveryAdminEmptyPageKeepsItsRestartLink.
func TestMessagesEmptyPageKeepsItsRestartLink(t *testing.T) {
	t.Parallel()
	b := keyset.Bound{PastEnd: true, First: "/admin/products?q=test"}
	body := render(t, Messages(frame.Page{Title: "Pagination"}, MessagesView{Bound: b}))
	if !strings.Contains(body, `href="/admin/products?q=test"`) || !strings.Contains(body, "First page") {
		t.Fatal("empty page lost its plain restart link")
	}
	if strings.Count(body, "There are no entries on this page.") != 1 {
		t.Fatal("empty page lost its shared empty state")
	}
}

// From TestEveryQueueKeepsItsOwnFirstPageEmptyState.
func TestMessagesQueueKeepsItsOwnFirstPageEmptyState(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	want := ctx(i18n.KeyAdminMessagesEmpty)
	body := render(t, Messages(frame.Page{Title: "Queue"}, MessagesView{Bound: firstPageBound("/admin/queue")}))
	if !strings.Contains(body, html.EscapeString(want)) {
		t.Errorf("the queue's own empty text %q is missing", want)
	}
	if strings.Contains(body, ctx(i18n.KeyPageEmpty)) {
		t.Error("an empty first page shows the later-page sentence")
	}
}

// From TestEveryQueueWithMoreRowsOffersNext.
func TestMessagesQueueWithMoreRowsOffersNext(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	body := render(t, Messages(frame.Page{Title: "Queue"}, MessagesView{Bound: fullPageBound("/admin/queue")}))
	if !strings.Contains(body, `rel="next"`) {
		t.Error("a first page with more rows has no Next link")
	}
	if strings.Contains(body, ctx(i18n.KeyPageEmpty)) {
		t.Error("a first page with more rows says it is empty")
	}
}

func TestMessageQueueWaitingAndOverdueNeedYouAndOverdueSaysSo(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	fresh := Message{ID: "fresh", SubjectLabel: "S1", WaitingDays: 0}
	late := Message{ID: "late", SubjectLabel: "S2", WaitingDays: 4}
	view := MessagesView{Bound: firstPageBound("messages"), Rows: []Message{fresh, late}}
	page := renderComponent(t, ctx, Messages(frame.Page{}, view))
	wantBadge(t, page, fresh.Waiting(ctx), "goen-badge goen-badge--warn")
	wantBadge(t, page, late.OverdueText(ctx), "goen-badge goen-badge--warn")
	if !strings.Contains(late.OverdueText(ctx), "逾時") {
		t.Errorf("overdue text %q does not say 逾時", late.OverdueText(ctx))
	}
	if !strings.Contains(page, `<span aria-hidden="true">▲</span>`) {
		t.Error("the overdue badge has no ▲")
	}
	if strings.Count(page, "▲") != 1 {
		t.Errorf("▲ appears %d times, want it on the overdue row only", strings.Count(page, "▲"))
	}
	count := fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminMessagesOpen), view.OpenCountText())
	if !strings.Contains(page, `<p class="goen-admin__headcount">`+count+`</p>`) {
		t.Errorf("the open count %q is not plain text in the page head", count)
	}
	if got := badgeClasses(t, page, count); len(got) != 0 {
		t.Errorf("the count is still a badge: %v", got)
	}
}
