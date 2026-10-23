package dashboard

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/widget"
)

// The tests in this file are the dashboard halves of tests that render several
// desks' pages: each half lives with the page it renders.

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

// TestDashboardRecentOrderBadge: the dashboard's recent orders follow their group.
// From statusbadge_test.go, moved whole.
func TestDashboardRecentOrderBadge(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	row := OrderRow{Number: "GO-A", Status: order.FulfillmentPending, StatusText: "待出貨", StatusIntent: widget.IntentWarn}

	dashboard := renderComponent(t, ctx, Dashboard(Meta(ctx), View{Recent: []OrderRow{row}}))
	wantBadge(t, dashboard, "待出貨", "goen-badge goen-badge--warn")
}
