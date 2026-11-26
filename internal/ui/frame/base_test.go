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

// TestBaseBagDrawerShell holds that every storefront page carries the bag
// drawer as one empty dialog named in the reader's language, a child of the
// body: goen.js fills it from the cart's fragment and opens it, so a shell that
// already held a cart would show the cart as it stood when the page was drawn.
func TestBaseBagDrawerShell(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		name   string
	}{
		{i18n.ZhHant, "購物車"},
		{i18n.En, "Cart"},
	} {
		var b strings.Builder
		if err := Base(Page{Title: "t"}).Render(i18n.WithLocale(t.Context(), tt.locale), &b); err != nil {
			t.Fatal(err)
		}
		doc, err := html.Parse(strings.NewReader(b.String()))
		if err != nil {
			t.Fatal(err)
		}
		value := func(n *html.Node, key string) string {
			for _, a := range n.Attr {
				if a.Key == key {
					return a.Val
				}
			}
			return ""
		}
		var shells []*html.Node
		for n := range doc.Descendants() {
			if n.Type == html.ElementNode && n.Data == "dialog" && value(n, "id") == "bag-drawer" {
				shells = append(shells, n)
			}
		}
		if len(shells) != 1 {
			t.Fatalf("%s: %d bag drawers on the page, want one", tt.locale.Tag(), len(shells))
		}
		if got := value(shells[0], "aria-label"); got != tt.name {
			t.Errorf("%s: the drawer is named %q, want %q", tt.locale.Tag(), got, tt.name)
		}
		if shells[0].FirstChild != nil {
			t.Errorf("%s: the drawer's shell holds content before any add", tt.locale.Tag())
		}
		if shells[0].Parent == nil || shells[0].Parent.Data != "body" {
			t.Errorf("%s: the drawer is not a child of the body", tt.locale.Tag())
		}
	}
}
