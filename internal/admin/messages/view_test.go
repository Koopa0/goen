package messages

import (
	"bytes"
	"fmt"
	"html"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	nethtml "golang.org/x/net/html"

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
	root, err := nethtml.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out []string
	var walk func(n *nethtml.Node)
	walk = func(n *nethtml.Node) {
		if n.Type == nethtml.ElementNode {
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

func textOf(n *nethtml.Node) string {
	if n.Type == nethtml.TextNode {
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

// TestMessagesEmptyPage: the messages empty page keeps its restart link.
// From TestAuditEmptyPageRestartLink.
func TestMessagesEmptyPage(t *testing.T) {
	t.Parallel()
	b := keyset.Bound{PastEnd: true, First: "/admin/products?q=test"}
	body := render(t, Messages(frame.Page{Title: "Pagination"}, View{Bound: b}))
	if !strings.Contains(body, `href="/admin/products?q=test"`) || !strings.Contains(body, "First page") {
		t.Fatal("empty page lost its plain restart link")
	}
	if strings.Count(body, "There are no entries on this page.") != 1 {
		t.Fatal("empty page lost its shared empty state")
	}
}

// TestMessagesFirstPageEmpty: the messages queue keeps its own first-page empty state.
// From TestAuditFirstPageEmpty.
func TestMessagesFirstPageEmpty(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	want := ctx(i18n.KeyAdminMessagesEmpty)
	body := render(t, Messages(frame.Page{Title: "Queue"}, View{Bound: firstPageBound("/admin/queue")}))
	if !strings.Contains(body, html.EscapeString(want)) {
		t.Errorf("the queue's own empty text %q is missing", want)
	}
	if strings.Contains(body, ctx(i18n.KeyPageEmpty)) {
		t.Error("an empty first page shows the later-page sentence")
	}
}

// TestMessagesNextLink: a messages queue with more rows offers Next.
// From TestAuditFullPageOffersNext.
func TestMessagesNextLink(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	body := render(t, Messages(frame.Page{Title: "Queue"}, View{Bound: fullPageBound("/admin/queue")}))
	if !strings.Contains(body, `rel="next"`) {
		t.Error("a first page with more rows has no Next link")
	}
	if strings.Contains(body, ctx(i18n.KeyPageEmpty)) {
		t.Error("a first page with more rows says it is empty")
	}
}

// TestMessagesWaitingBadges: in the message queue a wait is the staff's turn, a handled message is a plain badge, and
// overdue is warn and says so.
func TestMessagesWaitingBadges(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	fresh := Message{ID: "fresh", SubjectLabel: "S1", WaitingDays: 0}
	late := Message{ID: "late", SubjectLabel: "S2", WaitingDays: 4}
	done := Message{ID: "done", SubjectLabel: "S3", WaitingDays: 4, Handled: true}
	view := View{Bound: firstPageBound("messages"), Rows: []Message{fresh, late, done}}
	page := renderComponent(t, ctx, Messages(frame.Page{}, view))
	wantBadge(t, page, fresh.Waiting(ctx), "goen-badge goen-badge--staff-turn")
	wantBadge(t, page, late.Waiting(ctx), "goen-badge goen-badge--staff-turn")
	wantBadge(t, page, done.Waiting(ctx), "goen-badge")
	wantBadge(t, page, i18n.T(ctx, i18n.KeyAdminOverdue), "goen-badge goen-badge--warn")
	if got := badgeClasses(t, page, i18n.T(ctx, i18n.KeyAdminOverdue)); len(got) != 1 {
		t.Errorf("%d badges say overdue, want one, on the late row", len(got))
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

// TestMessageWaitsInShopDays: a message is as old as the shop days since it
// arrived, as the dashboard task that opens this desk counts them.
func TestMessageWaitsInShopDays(t *testing.T) {
	t.Parallel()
	taipei := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 10, 12, 1, 0, 0, 0, taipei)
	tests := []struct {
		name    string
		arrived time.Time
		want    int
	}{
		{"two hours ago across midnight", time.Date(2026, 10, 11, 23, 0, 0, 0, taipei), 1},
		{"earlier today", time.Date(2026, 10, 12, 0, 30, 0, 0, taipei), 0},
		{"twenty-five hours ago, which the clock calls one day", time.Date(2026, 10, 10, 23, 59, 0, 0, taipei), 2},
	}
	for _, tt := range tests {
		got := newMessage(&db.AdminMessagesRow{CreatedAt: tt.arrived}, "", now).WaitingDays
		if got != tt.want {
			t.Errorf("%s: WaitingDays = %d, want %d", tt.name, got, tt.want)
		}
	}
}

// TestMessageWaitWords: the desk words a wait and its lateness as the orders
// work tabs do, and the third day is the first overdue one.
func TestMessageWaitWords(t *testing.T) {
	t.Parallel()
	tests := []struct {
		days    int
		handled bool
		zh, en  string
		overdue bool
	}{
		{0, false, "今天", "Today", false},
		{1, false, "已等 1 天", "Waiting 1 day", false},
		{2, false, "已等 2 天", "Waiting 2 days", false},
		{3, false, "已等 3 天", "Waiting 3 days", true},
		{9, true, "已處理", "Handled", false},
	}
	for _, tt := range tests {
		m := Message{WaitingDays: tt.days, Handled: tt.handled}
		if got := m.Waiting(i18n.WithLocale(t.Context(), i18n.ZhHant)); got != tt.zh {
			t.Errorf("Waiting(%d days, zh-Hant) = %q, want %q", tt.days, got, tt.zh)
		}
		if got := m.Waiting(i18n.WithLocale(t.Context(), i18n.En)); got != tt.en {
			t.Errorf("Waiting(%d days, en) = %q, want %q", tt.days, got, tt.en)
		}
		if got := m.Overdue(); got != tt.overdue {
			t.Errorf("Overdue(%d days, handled %v) = %v, want %v", tt.days, tt.handled, got, tt.overdue)
		}
	}
}
