package orders

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/frame"
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

func textOf(n *html.Node) string {
	if n.Type == html.TextNode {
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

// TestOrdersListPills: the queue's pills follow their group and leave out
// committed.
func TestOrdersListPills(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := View{
		Bound: firstPageBound("orders"),
		Orders: []Row{
			{Number: "GO-A", Status: order.FulfillmentPicking, StatusText: "備貨中", StatusIntent: widget.IntentProgress},
			{Number: "GO-B", Status: order.FulfillmentPending, StatusText: "待出貨", StatusIntent: widget.IntentWarn},
			{Number: "GO-C", Status: order.FulfillmentCompleted, StatusText: "已完成", StatusIntent: widget.IntentDone},
		},
	}
	page := renderComponent(t, ctx, Orders(frame.Page{}, view))
	wantBadge(t, page, "備貨中", "goen-badge goen-badge--progress")
	wantBadge(t, page, "待出貨", "goen-badge goen-badge--warn")
	wantBadge(t, page, "已完成", "goen-badge goen-badge--done")
	if got := badgeClasses(t, page, i18n.T(ctx, i18n.KeyAdminQueueCommitted)); len(got) != 0 {
		t.Errorf("the list repeats the committed pill on every row: %v", got)
	}
}

func TestOrderPageHeadKeepsItsGroupAndCommitted(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := &OrderView{Number: "GO-A", Status: order.FulfillmentShipped, StatusText: "已出貨", StatusIntent: widget.IntentProgress, Committed: true}
	page := renderComponent(t, ctx, Order(frame.Page{}, view))
	wantBadge(t, page, "已出貨", "goen-badge goen-badge--progress")
	wantBadge(t, page, i18n.T(ctx, i18n.KeyAdminQueueCommitted), "goen-badge")
}
