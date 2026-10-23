package taxonomy

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestTaxonomyCategoryControls: category controls have visible headings and named parent choices.
func TestTaxonomyCategoryControls(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale   i18n.Locale
		headings []string
	}{
		{locale: i18n.ZhHant, headings: []string{"名稱", "英文名稱", "圖示", "色調", "商品比較"}},
		{locale: i18n.En, headings: []string{"Name", "English name", "Icon", "Tone", "Product comparison"}},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			view := &View{Categories: []Taxon{{Slug: "kitchen", Name: "廚房"}, {Slug: "books", Name: "書房", Depth: 1}}, Which: "categories", Draft: TaxonDraft{Parent: "books"}, Errors: map[string]string{"name": "Correct the name"}}
			doc, err := html.Parse(strings.NewReader(renderComponent(t, i18n.WithLocale(t.Context(), tt.locale), Taxonomy(frame.Page{}, view))))
			if err != nil {
				t.Fatal(err)
			}
			var headings, choices []string
			var parent *html.Node
			selected := ""
			for n := range doc.Descendants() {
				if n.Type != html.ElementNode {
					continue
				}
				if slices.Contains(strings.Fields(attr(n, "class")), "goen-admin__taxonhead") {
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.ElementNode {
							headings = append(headings, homeText(c))
						}
					}
				}
				if attr(n, "id") == "categories-parent" {
					parent = n
				}
			}
			if diff := cmp.Diff(tt.headings, headings); diff != "" {
				t.Errorf("visible category headings (-want +got):\n%s", diff)
			}
			if parent == nil || parent.Data != "select" {
				t.Fatal("parent category is not a native select")
			}
			for n := range parent.Descendants() {
				if n.Data != "option" {
					continue
				}
				if attr(n, "value") != "" {
					choices = append(choices, attr(n, "value")+":"+homeText(n))
				}
				if _, ok := attrPresent(n, "selected"); ok {
					selected = attr(n, "value")
				}
			}
			if diff := cmp.Diff([]string{"kitchen:廚房", "books:書房"}, choices); diff != "" {
				t.Errorf("parent choices (-want +got):\n%s", diff)
			}
			if selected != "books" {
				t.Errorf("refused parent = %q, want books", selected)
			}
		})
	}
}

// TestCategoryFormImageDescriptions: category image descriptions can be saved without replacing the photograph.
func TestCategoryFormImageDescriptions(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		labels []string
		hint   string
	}{
		{locale: i18n.ZhHant, labels: []string{"替代文字（中文）", "替代文字（英文）"}, hint: "給看不到圖片的人閱讀。"},
		{locale: i18n.En, labels: []string{"Alt text (Chinese)", "Alt text (English)"}, hint: "For people who cannot see the image."},
	} {
		for _, hasImage := range []bool{false, true} {
			name := tt.locale.Tag() + "/missing"
			if hasImage {
				name = tt.locale.Tag() + "/existing"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				view := CategoryView{Slug: "books"}
				if hasImage {
					view.Image = widget.HeaderImage{Image: picture.Image{Digest: strings.Repeat("a", 64)}}
				}
				doc, err := html.Parse(strings.NewReader(renderComponent(t, i18n.WithLocale(t.Context(), tt.locale), CategoryForm(frame.Page{}, view))))
				if err != nil {
					t.Fatal(err)
				}
				var labels []string
				var file *html.Node
				hints := map[string]bool{}
				for n := range doc.Descendants() {
					if n.Type != html.ElementNode {
						continue
					}
					switch attr(n, "id") {
					case "cat-image":
						file = n
					case "cat-alt-hint", "cat-alt-en-hint":
						hints[attr(n, "id")] = homeText(n) == tt.hint
					}
					if n.Data == "label" && (attr(n, "for") == "cat-alt" || attr(n, "for") == "cat-alt-en") {
						labels = append(labels, homeText(n))
					}
					if attr(n, "id") == "cat-alt" || attr(n, "id") == "cat-alt-en" {
						if !slices.Contains(strings.Fields(attr(n, "aria-describedby")), attr(n, "id")+"-hint") {
							t.Errorf("%s has no linked explanation", attr(n, "id"))
						}
					}
				}
				if file == nil {
					t.Fatal("image field is absent")
				}
				_, required := attrPresent(file, "required")
				if required == hasImage {
					t.Errorf("existing image %t: file required = %t, want %t", hasImage, required, !hasImage)
				}
				if diff := cmp.Diff(tt.labels, labels); diff != "" {
					t.Errorf("alt labels (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(map[string]bool{"cat-alt-hint": true, "cat-alt-en-hint": true}, hints); diff != "" {
					t.Errorf("visible alt explanations (-want +got):\n%s", diff)
				}
			})
		}
	}
}
