package checkout

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
)

// The render helpers and node walkers the cart and checkout view tests call,
// with the bodies every page package's tests keep: a test in another package
// cannot import them.

func renderComponent(t *testing.T, ctx context.Context, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	// The context is kept at the call site so the locale and the staff flag are
	// both explicit: this test is about what each one puts on the page.
	if err := c.Render(ctx, &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func renderToString(t *testing.T, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func findDescendant(n *html.Node, match func(*html.Node) bool) *html.Node {
	if n == nil {
		return nil
	}
	if match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findDescendant(c, match); found != nil {
			return found
		}
	}
	return nil
}

func attrValue(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func nodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(nodeText(c))
	}
	return b.String()
}

func descendants(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var found []*html.Node
	if match(n) {
		found = append(found, n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		found = append(found, descendants(c, match)...)
	}
	return found
}

func hasClass(n *html.Node, class string) bool {
	return slices.Contains(strings.Fields(attrValue(n, "class")), class)
}

// invoiceCard is the card of one invoice type: the element holding its radio
// and the fields that type asks for.
func invoiceCard(t *testing.T, doc *html.Node, kind invoice.Preference) *html.Node {
	t.Helper()
	cards := descendants(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && hasClass(n, "goen-checkout__option") &&
			findDescendant(n, func(r *html.Node) bool {
				return r.Type == html.ElementNode && r.Data == "input" && attrValue(r, "name") == "invoice_type" &&
					attrValue(r, "value") == string(kind)
			}) != nil
	})
	if len(cards) != 1 {
		t.Fatalf("%d cards hold the %s radio, want 1", len(cards), kind)
	}
	return cards[0]
}

// invoiceTextFields are the fields under n that carry what an invoice type
// asks for, which is every invoice_ control but the choice itself.
func invoiceTextFields(n *html.Node) []*html.Node {
	return descendants(n, func(f *html.Node) bool {
		return f.Type == html.ElementNode && f.Data == "input" && strings.HasPrefix(attrValue(f, "name"), "invoice_") &&
			attrValue(f, "name") != "invoice_type"
	})
}
