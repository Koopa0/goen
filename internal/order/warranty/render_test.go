package warranty

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// renderToString renders c in Traditional Chinese. It is the same helper every page package's tests keep;
// none is importable from here.
func renderToString(t *testing.T, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// TestWarrantyReadyRowIsTonal: each line ready to register carries the same step forward,
// once a row, so it is the tonal level.
func TestWarrantyReadyRowIsTonal(t *testing.T) {
	t.Parallel()
	view := ListView{Ready: []Ready{{Number: "GO-261001-000001", Line: Line{ID: "7", Name: "Buds"}}}}
	doc, err := html.Parse(strings.NewReader(renderToString(t, List(frame.Page{Title: "Warranty"}, view))))
	if err != nil {
		t.Fatal(err)
	}
	var link *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" && a.Val == "/account/warranty/GO-261001-000001#serial-7" {
					link = n
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if link == nil {
		t.Fatal("the ready row has no link to register")
	}
	var class string
	for _, a := range link.Attr {
		if a.Key == "class" {
			class = " " + a.Val + " "
		}
	}
	if !strings.Contains(class, " goen-btn--secondary ") || strings.Contains(class, " goen-btn--outline ") {
		t.Errorf("the register link is drawn as %q, want the tonal level", class)
	}
}
