package catalog

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

// TestProductCompareFullHint holds that the full-comparison hint links to the
// selected products.
func TestProductCompareFullHint(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			view := &ProductView{Slug: "new-product", Comparable: true}
			for i := range MaxCompare {
				view.Comparing = append(view.Comparing, string(rune('a'+i)))
			}
			var body strings.Builder
			if err := productCompare(view).Render(ctx, &body); err != nil {
				t.Fatal(err)
			}
			doc, err := html.Parse(strings.NewReader(body.String()))
			if err != nil {
				t.Fatal(err)
			}
			hint := findDescendant(doc, func(n *html.Node) bool { return hasClass(n, "goen-pdp__hint") })
			if hint == nil || !strings.Contains(nodeText(hint), fmt.Sprintf(i18n.T(ctx, i18n.KeyCompareFull), MaxCompare)) {
				t.Fatal("the full comparison hint is missing")
			}
			link := findDescendant(hint, func(n *html.Node) bool { return n.Data == "a" })
			if link == nil {
				t.Fatal("the full comparison hint has no link to view the comparison")
			}
			wantLabel := "查看比較"
			if locale == i18n.En {
				wantLabel = "View comparison"
			}
			if got := strings.TrimSpace(nodeText(link)); got != wantLabel {
				t.Errorf("comparison link label = %q, want %q", got, wantLabel)
			}
			target, err := url.Parse(attrValue(link, "href"))
			if err != nil {
				t.Fatal(err)
			}
			if target.Path != "/compare" || !slices.Equal(target.Query()["p"], view.Comparing) {
				t.Errorf("comparison link = %q, want /compare with only %v", target, view.Comparing)
			}
		})
	}
}

// TestProductCompareEntryIsALink holds that the way into the comparison is the
// department page's blue link with an arrow, not a button.
//
// A ghost button read as a caption in grey, and the page's other text links
// are blue; the arrow stays out of the accessible name.
func TestProductCompareEntryIsALink(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		comparing []string
		label     i18n.Key
	}{
		{"offer", nil, i18n.KeyCompareSimilar},
		{"already in", []string{"new-product"}, i18n.KeyViewCompare},
	} {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			t.Run(tt.name+"/"+string(locale), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				view := &ProductView{Slug: "new-product", Comparable: true, Comparing: tt.comparing}
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
				if got, want := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(nodeText(link)), "→")), i18n.T(ctx, tt.label); got != want {
					t.Errorf("label = %q, want %q", got, want)
				}
			})
		}
	}
}
