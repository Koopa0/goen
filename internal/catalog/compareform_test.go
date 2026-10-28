package catalog

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

// TestCompareFormSubmitIsTonal: comparing the ticked products is a step forward, not the
// listing's own action, so its submit is the tonal level.
func TestCompareFormSubmitIsTonal(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, compareForm(true))))
			if err != nil {
				t.Fatal(err)
			}
			submit := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "button" && attrValue(n, "type") == "submit"
			})
			if submit == nil {
				t.Fatal("the compare bar has no submit")
			}
			if class := attrValue(submit, "class"); !hasClass(submit, "goen-btn--secondary") || hasClass(submit, "goen-btn--outline") {
				t.Errorf("the compare submit is drawn as %q, want the tonal level", class)
			}
		})
	}
}
