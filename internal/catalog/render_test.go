package catalog

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// The helpers below are the ones the view tests call, with the bodies every
// page package's tests keep; those are not importable from here.

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

func hasClass(n *html.Node, want string) bool {
	for _, c := range strings.Fields(className(n)) {
		if c == want {
			return true
		}
	}
	return false
}

func className(n *html.Node) string {
	for _, a := range n.Attr {
		if a.Key == "class" {
			return a.Val
		}
	}
	return ""
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

func renderHeadAt(t *testing.T, ctx context.Context, p frame.Page) string {
	t.Helper()
	return renderComponent(t, frame.WithSiteOrigin(ctx, "https://goen.test"), frame.Base(p))
}

func renderIn(t *testing.T, locale i18n.Locale, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), locale), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func assertEmailRefusal(t *testing.T, markup, inputID, sentence string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	input := findDescendant(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "input" && attrValue(n, "id") == inputID
	})
	if input == nil || attrValue(input, "aria-invalid") != "true" {
		t.Fatal("refused email field is not marked invalid")
	}
	errorID := attrValue(input, "aria-describedby")
	attached := false
	for _, id := range strings.Fields(errorID) {
		target := findDescendant(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && attrValue(n, "id") == id })
		if target == nil {
			continue
		}
		var text strings.Builder
		for n := range target.Descendants() {
			if n.Type == html.TextNode {
				text.WriteString(n.Data)
			}
		}
		if strings.TrimSpace(text.String()) == sentence {
			attached = true
		}
	}
	if !attached {
		t.Errorf("email refusal does not attach %q to its field", sentence)
	}
}
