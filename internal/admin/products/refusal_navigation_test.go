package products

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

func TestDeskRefusalsNameTheirFormAndLinkToTheFirstInvalidControl(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			bad := i18n.T(ctx, i18n.KeyAdminNoticeRefused)
			cases := []struct {
				name                  string
				component             templ.Component
				action, target, first string
				title                 i18n.Key
				count                 int
			}{
				{"details", ProductForm(frame.Page{}, ProductView{Slug: "test", Name: "", Summary: "kept", Errors: map[string]string{"name": bad, "summary": bad}}), "/admin/products/test#sec-details", "sec-details", "p-name", i18n.KeyAdminProdBasics, 2},
				{"variant", ProductForm(frame.Page{}, ProductView{Slug: "test", VariantDraft: VariantDraft{SKU: "", Price: "wrong"}, Errors: map[string]string{"sku": bad, "price": bad}}), "/admin/products/test/variants#sec-variants", "sec-variants", "v-sku", i18n.KeyAdminProdVariantAdd, 2},
				{"option", ProductForm(frame.Page{}, ProductView{Slug: "test", Errors: map[string]string{"option": bad}}), "/admin/products/test/options#sec-options", "sec-options", "opt-name", i18n.KeyAdminProdOptionAdd, 1},
				{"spec", ProductForm(frame.Page{}, ProductView{Slug: "test", Errors: map[string]string{"spec_label": bad, "spec_value": bad}}), "/admin/products/test/specs#sec-specs", "sec-specs", "spec-label", i18n.KeyAdminProdSpecs, 2},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					body := renderComponent(t, ctx, tc.component)
					doc, err := html.Parse(strings.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					var alert *html.Node
					var form, target, first *html.Node
					ids := map[string]*html.Node{}
					for n := range doc.Descendants() {
						if n.Type != html.ElementNode {
							continue
						}
						id := refusalAttr(n, "id")
						if id != "" {
							ids[id] = n
						}
						if n.Data == "form" && refusalAttr(n, "action") == tc.action {
							form = n
						}
						if id == tc.target {
							target = n
						}
						if id == tc.first {
							first = n
						}
						if refusalAttr(n, "role") == "alert" && strings.Contains(refusalText(n), i18n.T(ctx, tc.title)) {
							for link := range n.Descendants() {
								if link.Data == "a" && refusalAttr(link, "href") == "#"+tc.first {
									alert = n
								}
							}
						}
					}
					if form == nil || target == nil {
						t.Errorf("form must post to its section: %s", tc.action)
					}
					if alert == nil {
						t.Fatalf("page-top alert must name %q and link to #%s", i18n.T(ctx, tc.title), tc.first)
					}
					count := "2"
					if tc.count == 1 {
						count = "1"
					}
					if !strings.Contains(refusalText(alert), count) {
						t.Errorf("alert does not count %d invalid fields", tc.count)
					}
					for n := range doc.Descendants() {
						if n == alert {
							break
						}
						if n.Data == "form" && strings.Contains(refusalAttr(n, "class"), "goen-admin__") {
							t.Error("summary must precede the forms")
							break
						}
					}
					if first == nil || refusalAttr(first, "aria-invalid") != "true" {
						t.Errorf("first invalid control %s is not marked", tc.first)
					}
					for n := range doc.Descendants() {
						if n.Type != html.ElementNode || refusalAttr(n, "aria-invalid") != "true" {
							continue
						}
						describes := strings.Fields(refusalAttr(n, "aria-describedby"))
						if len(describes) == 0 {
							t.Errorf("invalid control %s has no description", refusalAttr(n, "id"))
						}
						for _, id := range describes {
							if ids[id] == nil || strings.TrimSpace(refusalText(ids[id])) == "" {
								t.Errorf("invalid control references missing error %s", id)
							}
						}
					}
				})
			}
		})
	}
}

func refusalAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func refusalText(n *html.Node) string {
	if n == nil {
		return ""
	}
	var out strings.Builder
	if n.Type == html.TextNode {
		out.WriteString(n.Data)
	}
	for child := range n.Descendants() {
		if child.Type == html.TextNode {
			out.WriteString(child.Data)
		}
	}
	return out.String()
}

func TestRefusalSummariesCountEachRefusalOnce(t *testing.T) {
	t.Parallel()
	product := &ProductView{
		Options: []Option{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Errors:  map[string]string{"options": "pick one"},
	}
	if got := product.VariantRefusal(); got.Count != 1 || got.First != "v-opt-a" {
		t.Errorf("VariantRefusal() = %+v, want Count 1 at v-opt-a", got)
	}
}
