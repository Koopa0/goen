package catalog

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

// TestProductCompareEntryIsALink holds that the way from the specifications to
// a ready-made comparison is the department page's blue link with an arrow,
// not a button, and leads to the product beside the similar ones.
//
// A ghost button read as a caption in grey, and the page's other text links
// are blue; the arrow stays out of the accessible name.
func TestProductCompareEntryIsALink(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			view := &ProductView{Slug: "new-product", Comparable: true, Similar: []string{"near", "nearer"}}
			var body strings.Builder
			if err := productCompare(view).Render(ctx, &body); err != nil {
				t.Fatal(err)
			}
			doc, err := html.Parse(strings.NewReader(body.String()))
			if err != nil {
				t.Fatal(err)
			}
			link := findDescendant(doc, func(n *html.Node) bool { return n.Data == "a" && hasClass(n, "goen-pdp__compare") })
			if link == nil {
				t.Fatalf("no comparison link in %s", body.String())
			}
			if hasClass(link, "goen-btn") {
				t.Error("the comparison entry is still a button")
			}
			arrow := findDescendant(link, func(n *html.Node) bool { return n.Data == "span" && attrValue(n, "aria-hidden") == "true" })
			if arrow == nil || strings.TrimSpace(nodeText(arrow)) != "→" {
				t.Error("the link has no hidden arrow")
			}
			if got, want := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(nodeText(link)), "→")), i18n.T(ctx, i18n.KeyCompareSimilar); got != want {
				t.Errorf("label = %q, want %q", got, want)
			}
			if got, want := attrValue(link, "href"), "/compare?p=new-product&p=near&p=nearer"; got != want {
				t.Errorf("href = %q, want %q", got, want)
			}
		})
	}
}
