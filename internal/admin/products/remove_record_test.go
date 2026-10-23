package products

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
)

// TestProductImagesRemoveButtons: the remove buttons of the image list and of
// the specification table name their record.
func TestProductImagesRemoveButtons(t *testing.T) {
	t.Parallel()
	for _, locale := range []struct {
		locale i18n.Locale
		remove string
	}{
		{locale: i18n.ZhHant, remove: "移除"},
		{locale: i18n.En, remove: "Remove"},
	} {
		for _, tt := range []struct {
			name      string
			component templ.Component
			action    string
			field     string
			want      map[string]string
		}{
			{name: "product images", component: productImages(ProductView{Slug: "phone", Images: []Image{{Photo: picture.Image{BundledKey: "front"}, Alt: "Front & side"}, {Photo: picture.Image{BundledKey: "back"}}}}), action: "/admin/products/phone/images/remove", field: "digest", want: map[string]string{"front": "Front & side", "back": "back"}},
			{name: "product specifications", component: productSpecs(ProductView{Slug: "phone", Specs: []Spec{{ID: "weight", Label: "Weight <net>", Value: "100g"}, {ID: "width", Label: "Width", Value: "20mm"}}}), action: "/admin/products/phone/specs/remove", field: "spec", want: map[string]string{"weight": "Weight <net>", "width": "Width"}},
		} {
			t.Run(locale.locale.Tag()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale.locale)
				doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, tt.component)))
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]string{}
				for form := range doc.Descendants() {
					if form.Type != html.ElementNode || form.Data != "form" || attr(form, "action") != tt.action {
						continue
					}
					if attr(form, "method") != "post" {
						t.Errorf("removal form method = %q, want post", attr(form, "method"))
					}
					var record string
					var button *html.Node
					for n := range form.Descendants() {
						if n.Data == "input" && attr(n, "name") == tt.field {
							record = attr(n, "value")
						}
						if n.Data == "button" && attr(n, "type") == "submit" {
							button = n
						}
					}
					if button == nil {
						t.Fatal("removal form has no submit button")
					}
					var visible, hidden strings.Builder
					seenRecord := false
					for n := button.FirstChild; n != nil; n = n.NextSibling {
						if n.Type == html.TextNode {
							if seenRecord && strings.TrimSpace(n.Data) != "" {
								t.Errorf("record %q precedes the visible action in the accessible name", record)
							}
							visible.WriteString(n.Data)
							continue
						}
						if n.Data != "span" || attr(n, "class") != "goen-sr-only" {
							t.Errorf("record %q uses a visible or unexpected label element", record)
							continue
						}
						if _, present := attrPresent(n, "hidden"); present || attr(n, "aria-hidden") == "true" {
							t.Errorf("record %q is hidden from assistive technology", record)
						}
						seenRecord = true
						for text := range n.Descendants() {
							if text.Type == html.TextNode {
								hidden.WriteString(text.Data)
							}
						}
					}
					if label := strings.TrimSpace(visible.String()); label != locale.remove {
						t.Errorf("record %q visible label = %q, want %q", record, label, locale.remove)
					}
					if attr(button, "aria-label") != "" || attr(button, "aria-labelledby") != "" {
						t.Errorf("record %q overrides its visible label and record text", record)
					}
					got[record] = strings.TrimSpace(hidden.String())
				}
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("removal button records (-want +got):\n%s", diff)
				}
			})
		}
	}
}
