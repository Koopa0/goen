package products

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// A refused details form shows the draft in its fields, but the page is named by
// the product's saved name: a cleared name leaves no empty heading, and a name
// that was typed and not saved never heads the page.
func TestProductRefusedPageIsNamedBySavedName(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		for name, draft := range map[string]string{"cleared": "", "changed": "Unsaved kettle"} {
			view := ProductView{Slug: "kettle", Name: draft, SavedName: "Saved kettle", Errors: map[string]string{"name": "refused"}}
			doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, Product(frame.Page{Refused: true, Title: view.Title(ctx)}, view))))
			if err != nil {
				t.Fatal(err)
			}
			var title, heading string
			for n := range doc.Descendants() {
				switch {
				case n.Type == html.ElementNode && n.Data == "title":
					title = refusalText(n)
				case n.Type == html.ElementNode && n.Data == "h1" && hasClass(n, "ui-page-head__title"):
					heading = refusalText(n)
				}
			}
			if want := strings.Replace(i18n.T(ctx, i18n.KeyAdminRefusedTitle), "%s", "Saved kettle", 1); !strings.HasPrefix(title, want) {
				t.Errorf("%s, name %s: title = %q, want it to start with %q", locale.Tag(), name, title, want)
			}
			if heading != "Saved kettle" {
				t.Errorf("%s, name %s: h1 = %q, want the saved name", locale.Tag(), name, heading)
			}
		}
	}
}
