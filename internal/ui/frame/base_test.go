package frame

import (
	"encoding/json"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

// TestBaseHtmxRestoresHistoryByReload holds that Back and Forward reload the page.
//
// htmx would otherwise restore an entry by replacing the body, and goen.js, which
// runs once, would keep the header menu's handlers on the nodes that were removed.
func TestBaseHtmxRestoresHistoryByReload(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := Base(Page{Title: "t"}).Render(i18n.WithLocale(t.Context(), i18n.En), &b); err != nil {
		t.Fatal(err)
	}
	doc, err := html.Parse(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	var content string
	var found bool
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "meta" {
			var name, value string
			for _, a := range n.Attr {
				switch a.Key {
				case "name":
					name = a.Val
				case "content":
					value = a.Val
				}
			}
			if name == "htmx-config" {
				content, found = value, true
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if !found {
		t.Fatal("Base renders no htmx-config meta")
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		t.Fatalf("htmx-config %q is not JSON: %v", content, err)
	}
	if got := config["history"]; got != "reload" {
		t.Errorf("htmx-config history = %v, want %q", got, "reload")
	}
}
