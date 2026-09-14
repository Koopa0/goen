package pages

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestEachSavedProductIsOneWishlistListItem holds the wishlist grid against
// nested list items. Tile emits li.goen-tile__cell; wrapping @Tile in another
// li leaves an empty outer item and parks the remove form as a direct ul child.
func TestEachSavedProductIsOneWishlistListItem(t *testing.T) {
	t.Parallel()

	view := WishlistView{
		Products: []ProductTile{
			{Slug: "pixelight-9-pro", Name: "Pixelight 9 Pro", Brand: "Pixelight", PriceCents: 3690000, InStock: true},
			{Slug: "aurora-fold-2", Name: "Aurora Fold 2", Brand: "Aurora", PriceCents: 5990000, InStock: true},
		},
	}
	doc := parseWishlistHTML(t, view)
	grid := wishlistGrid(t, doc)

	if got, want := len(grid), len(view.Products); got != want {
		t.Fatalf("main ul has %d direct children, want %d — one list item per saved product", got, want)
	}

	for i, item := range grid {
		if item.Data != "li" {
			t.Fatalf("child %d is %q, want li — the remove form must not be a direct ul child", i, item.Data)
		}
		if !hasClass(item, "goen-wish") {
			t.Errorf("child %d is not .goen-wish", i)
		}
		if !hasClass(item, "goen-tile__cell") {
			t.Errorf("child %d is not .goen-tile__cell — the card lost its grid cell", i)
		}
		if nested := findChildDescendant(item, func(n *html.Node) bool { return n.Data == "li" }); nested != nil {
			t.Errorf("child %d nests %q inside it — Tile must not emit its own list item here", i, className(nested))
		}
		if findDescendant(item, func(n *html.Node) bool {
			return n.Data == "a" && hasClass(n, "goen-tile")
		}) == nil {
			t.Errorf("child %d has no product card link", i)
		}
		form := findDescendant(item, func(n *html.Node) bool {
			return n.Data == "form" && hasClass(n, "goen-wish__remove")
		})
		if form == nil {
			t.Fatalf("child %d has no remove form beside its card", i)
		}
		slug := hiddenInputValue(form, "slug")
		if slug != view.Products[i].Slug {
			t.Errorf("child %d remove form slug = %q, want %q", i, slug, view.Products[i].Slug)
		}
	}
}

// TestTileStillEmitsItsOwnGridCell holds other Tile consumers: extracting
// tileCard for the wishlist must not drop the list item the grid expects.
func TestTileStillEmitsItsOwnGridCell(t *testing.T) {
	t.Parallel()

	out := renderToString(t, Tile(ProductTile{
		Slug: "pixelight-9-pro", Name: "Pixelight 9 Pro", Brand: "Pixelight",
		PriceCents: 3690000, InStock: true, Comparable: true,
	}))
	if strings.Count(out, "<li") != 1 {
		t.Fatalf("Tile rendered %d list items, want 1", strings.Count(out, "<li"))
	}
	if !strings.Contains(out, `class="goen-tile__cell"`) {
		t.Error("Tile lost its grid cell wrapper")
	}
	if !strings.Contains(out, `class="goen-tile__compare"`) {
		t.Error("Tile lost its compare checkbox")
	}
}

func parseWishlistHTML(t *testing.T, view WishlistView) *html.Node {
	t.Helper()
	raw := renderToString(t, Wishlist(layouts.Page{Title: "願望清單"}, view))
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse wishlist HTML: %v", err)
	}
	return doc
}

func wishlistGrid(t *testing.T, doc *html.Node) []*html.Node {
	t.Helper()
	main := findDescendant(doc, func(n *html.Node) bool { return n.Data == "main" })
	if main == nil {
		t.Fatal("wishlist rendered no main landmark")
	}
	grid := findDescendant(main, func(n *html.Node) bool {
		return n.Data == "ul" && hasClass(n, "goen-tiles__grid")
	})
	if grid == nil {
		t.Fatal("wishlist rendered no product grid")
	}
	return childElements(grid)
}

func childElements(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			out = append(out, c)
		}
	}
	return out
}

func findChildDescendant(n *html.Node, match func(*html.Node) bool) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findDescendant(c, match); found != nil {
			return found
		}
	}
	return nil
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

func hiddenInputValue(form *html.Node, name string) string {
	var found string
	var scan func(*html.Node)
	scan = func(n *html.Node) {
		if found != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "input" && attrValue(n, "name") == name {
			found = attrValue(n, "value")
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			scan(c)
		}
	}
	scan(form)
	return found
}

func attrValue(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
