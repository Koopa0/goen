package pages

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestFullComparisonHintLinksToTheSelectedProducts(t *testing.T) {
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
