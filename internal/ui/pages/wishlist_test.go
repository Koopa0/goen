package pages

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestEachSavedProductIsOneWishlistListItem holds the wishlist grid against
// nested list items. Tile emits li.goen-tile__cell; wrapping @Tile in another
// li leaves an empty outer item and parks the remove form as a direct ul child.
func TestEachSavedProductIsOneWishlistListItem(t *testing.T) {
	t.Parallel()

	view := WishlistView{
		Products: []WishlistItem{
			{ProductTile: ProductTile{Slug: "pixelight-9-pro", Name: "Pixelight 9 Pro", Brand: "Pixelight", PriceCents: 3690000, InStock: true}},
			{ProductTile: ProductTile{Slug: "aurora-fold-2", Name: "Aurora Fold 2", Brand: "Aurora", PriceCents: 5990000, InStock: true}},
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

// TestAWishlistRowBuysOrSendsToTheProductAndStatesOnlySoldOut: the page that
// holds what somebody means to buy must let them, and say only when they cannot.
func TestAWishlistRowBuysOrSendsToTheProductAndStatesOnlySoldOut(t *testing.T) {
	t.Parallel()

	const variant = "7f0b6a3e-2c1d-4e5f-8a9b-0c1d2e3f4a5b"
	view := WishlistView{Products: []WishlistItem{
		{ProductTile: ProductTile{Slug: "one-variant", Name: "One", InStock: true}, SoleVariantID: variant},
		{ProductTile: ProductTile{Slug: "many-variants", Name: "Many", InStock: true}},
		{ProductTile: ProductTile{Slug: "sold-out", Name: "Gone"}},
	}}
	items := wishlistGrid(t, parseWishlistHTML(t, view))
	if len(items) != 3 {
		t.Fatalf("%d rows, want 3", len(items))
	}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	add := func(n *html.Node) *html.Node {
		return findDescendant(n, func(n *html.Node) bool { return n.Data == "form" && hasClass(n, "goen-wish__add") })
	}
	choose := func(n *html.Node) bool {
		return findDescendant(n, func(n *html.Node) bool {
			return n.Data == "a" && attrValue(n, "href") == "/p/many-variants" && hasClass(n, "goen-btn")
		}) != nil
	}

	form := add(items[0])
	if form == nil {
		t.Fatal("a product with one variant in stock has no add-to-cart form")
	}
	if got := hiddenInputValue(form, "variant"); got != variant {
		t.Errorf("add form variant = %q, want %q", got, variant)
	}
	if got := hiddenInputValue(form, "return"); got != "/account/wishlist" {
		t.Errorf("add form return = %q, want the wishlist", got)
	}
	if got := hiddenInputValue(form, "back"); got != "" {
		t.Errorf("add form back = %q, want none: it would send the shopper to the product", got)
	}
	if add(items[1]) != nil || !choose(items[1]) {
		t.Error("a product with a choice of variants must link to its page, not add one")
	}
	if add(items[2]) != nil || choose(items[2]) {
		t.Error("a sold-out product offers a way to buy")
	}

	soldOut := i18n.T(ctx, i18n.KeySoldOut)
	if got := strings.Count(nodeText(items[0]), soldOut) + strings.Count(nodeText(items[0]), i18n.T(ctx, i18n.KeyInStock)); got != 0 {
		t.Errorf("an in-stock row states its availability %d times, want none: a row says only the exception", got)
	}
	if got := strings.Count(nodeText(items[2]), soldOut); got != 1 {
		t.Errorf("a sold-out row says %q %d times, want once (the tile's badge)", soldOut, got)
	}
}

// TestRemovingFromTheWishlistSwapsThePageInPlaceAndStillPosts: script keeps the
// page where it is; without it the same form posts and redirects.
func TestRemovingFromTheWishlistSwapsThePageInPlaceAndStillPosts(t *testing.T) {
	t.Parallel()

	items := wishlistGrid(t, parseWishlistHTML(t, WishlistView{Products: []WishlistItem{{ProductTile: ProductTile{Slug: "a", Name: "A", InStock: true}}}}))
	form := findDescendant(items[0], func(n *html.Node) bool { return n.Data == "form" && hasClass(n, "goen-wish__remove") })
	if form == nil {
		t.Fatal("no remove form")
	}
	if attrValue(form, "action") != "/account/wishlist" || attrValue(form, "method") != "post" {
		t.Error("the remove form does not post without script")
	}
	if attrValue(form, "hx-post") != "/account/wishlist" || attrValue(form, "hx-select") != "#wishlist" {
		t.Error("the remove form does not swap the list in place")
	}
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

func TestAddingFromTheWishlistStaysOnItAndSaysWhatHappened(t *testing.T) {
	t.Parallel()
	item := WishlistItem{ProductTile: ProductTile{Slug: "mug", Name: "Mug", PriceCents: 100, InStock: true}, SoleVariantID: "v1"}
	for _, tc := range []struct {
		outcome AddOutcome
		want    i18n.Key
	}{
		{AddOutcomeAdded, i18n.KeyAddedToCart},
		{AddOutcomeAdjusted, i18n.KeyAddAdjusted},
		{AddOutcomeUnavailable, i18n.KeyAddRefused},
		{AddOutcomeFull, i18n.KeyCartLineLimit},
	} {
		ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
		got := renderToString(t, Wishlist(layouts.Page{}, WishlistView{Products: []WishlistItem{item}, Added: tc.outcome}))
		if !strings.Contains(got, i18n.T(ctx, tc.want)) {
			t.Errorf("outcome %q renders no %q", tc.outcome, tc.want)
		}
	}
	plain := renderToString(t, Wishlist(layouts.Page{}, WishlistView{Products: []WishlistItem{item}}))
	if strings.Contains(plain, `id="added"`) {
		t.Error("a plain visit shows an add-to-cart notice")
	}
	if !strings.Contains(plain, `name="return" value="/account/wishlist"`) || strings.Contains(plain, `name="back"`) {
		t.Error("the add form does not ask to come back to the wishlist")
	}
}

func TestNoSavedProductOffersAPrimaryButton(t *testing.T) {
	t.Parallel()

	view := WishlistView{Products: []WishlistItem{
		{ProductTile: ProductTile{Slug: "one-variant", Name: "One", InStock: true}, SoleVariantID: "7f0b6a3e-2c1d-4e5f-8a9b-0c1d2e3f4a5b"},
		{ProductTile: ProductTile{Slug: "many-variants", Name: "Many", InStock: true}},
	}}
	for i, item := range wishlistGrid(t, parseWishlistHTML(t, view)) {
		if findDescendant(item, func(n *html.Node) bool { return hasClass(n, "goen-btn--primary") }) != nil {
			t.Errorf("row %d has a primary button: a list of saved products shows none", i)
		}
	}
}
