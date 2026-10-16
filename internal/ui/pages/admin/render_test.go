package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// renderComponent renders c under ctx, which the caller owns so the locale and
// the staff flag stay explicit at the call site.
func renderComponent(t *testing.T, ctx context.Context, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(ctx, &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func renderToString(t *testing.T, c templ.Component) string {
	t.Helper()
	return renderComponent(t, i18n.WithLocale(t.Context(), i18n.ZhHant), c)
}

// Meta is the page the tests here wrap their views in: the title is not what
// they assert, so a fixed one is enough.
func Meta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyAdminPageDashboard)}
}

// formsCarrying returns the forms holding an input named name.
func formsCarrying(doc *html.Node, name string) []*html.Node {
	var forms []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" && hasElement(n, func(c *html.Node) bool {
			return c.Data == "input" && attr(c, "name") == name
		}) {
			forms = append(forms, n)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return forms
}

func hasElement(n *html.Node, match func(*html.Node) bool) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && match(c) || hasElement(c, match) {
			return true
		}
	}
	return false
}

func attr(n *html.Node, key string) string {
	v, _ := attrPresent(n, key)
	return v
}

func attrPresent(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
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
