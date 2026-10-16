package orders

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
)

// The helpers below are copies of internal/ui/pages/admin's test helpers, as in
// #2044, #2081, #2082 and #2129, rather than exported ones.

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

func render(t *testing.T, c templ.Component) string {
	t.Helper()
	var out bytes.Buffer
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.En), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

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
