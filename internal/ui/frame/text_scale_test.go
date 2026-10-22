package frame

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

// TestBaseAdminTextScaleMeta: only the storefront head opts into OS text scaling.
func TestBaseAdminTextScaleMeta(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, surface := range []struct {
			name      string
			component templ.Component
			want      int
		}{
			{"storefront", Base(Page{Title: "Text scaling"}), 1},
			{"admin", Admin(Page{Title: "Text scaling"}, "orders"), 0},
		} {
			t.Run(string(locale)+"/"+surface.name, func(t *testing.T) {
				t.Parallel()
				var rendered strings.Builder
				if err := surface.component.Render(templ.WithChildren(i18n.WithLocale(t.Context(), locale), templ.Raw(`<p id="scale-body-marker">Body content</p>`)), &rendered); err != nil {
					t.Fatalf("render %s: %v", surface.name, err)
				}
				doc, err := html.Parse(strings.NewReader(rendered.String()))
				if err != nil {
					t.Fatalf("parse %s HTML: %v", surface.name, err)
				}
				var count, bodyMarkers int
				var visit func(*html.Node)
				visit = func(node *html.Node) {
					var name, content, id string
					for _, attr := range node.Attr {
						switch attr.Key {
						case "name":
							name = attr.Val
						case "content":
							content = attr.Val
						case "id":
							id = attr.Val
						}
					}
					if id == "scale-body-marker" {
						bodyMarkers++
						var inMain bool
						for parent := node.Parent; parent != nil; parent = parent.Parent {
							inMain = inMain || parent.Data == "main"
						}
						if !inMain {
							t.Errorf("%s body children escaped main", surface.name)
						}
					}
					if node.Type == html.ElementNode && node.Data == "meta" && name == "text-scale" {
						count++
						if content != "scale" || node.Parent == nil || node.Parent.Data != "head" {
							t.Errorf("%s text-scale meta must opt in inside head, content=%q", surface.name, content)
						}
					}
					for child := node.FirstChild; child != nil; child = child.NextSibling {
						visit(child)
					}
				}
				visit(doc)
				if bodyMarkers != 1 {
					t.Errorf("%s body marker count = %d, want 1", surface.name, bodyMarkers)
				}
				if count != surface.want {
					t.Errorf("%s text-scale meta count = %d, want %d", surface.name, count, surface.want)
				}
			})
		}
	}
}
