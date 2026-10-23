package taxonomy

import (
	"slices"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
)

// The tests in this file are the taxonomy halves of tests that render several
// desks' pages: each half lives with the page it renders.

// TestTaxonomyRowSaveButton: a taxonomy row saves what it shows.
// From copy_test.go; it rendered only the taxonomy page, so it moved whole.
func TestTaxonomyRowSaveButton(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		want   string
	}{{i18n.ZhHant, "儲存"}, {i18n.En, "Save"}} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			view := &TaxonomyView{Brands: []Taxon{{Slug: "saelo", Name: "沙羅"}}}
			body := renderComponent(t, ctx, Taxonomy(frame.Page{Title: "Brands"}, view))
			i := strings.Index(body, `value="rename"`)
			if i < 0 {
				t.Fatal("no row save button")
			}
			end := strings.Index(body[i:], "</button>")
			if got := body[i : i+end]; !strings.HasSuffix(strings.TrimSpace(got), tt.want) {
				t.Errorf("row button reads %q, want it to end with %q", got, tt.want)
			}
		})
	}
}

// From TestProductFormExampleLine.
func TestTaxonomyExampleIsItsOwnHintLine(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	pages := map[string]string{
		"taxonomy": renderComponent(t, ctx, Taxonomy(frame.Page{Title: "Brands"}, &TaxonomyView{})),
	}
	for name, body := range pages {
		if strings.Contains(body, "。 ") {
			t.Errorf("%s page runs a sentence into the next on one line: %q", name, body[strings.Index(body, "。 ")-20:strings.Index(body, "。 ")+30])
		}
		if !strings.Contains(body, "例如 ") {
			t.Errorf("%s page has no example line", name)
		}
	}
}

// TestTaxonomyExampleHintsInFull: every taxonomy example hint reads in full.
// From TestProductFormExampleHints.
func TestTaxonomyExampleHintsInFull(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale   i18n.Locale
		sentence func(example string) string
	}{
		{i18n.ZhHant, func(e string) string { return "<p class=\"goen-admin__hint\">例如 " + e + "。</p>" }},
		{i18n.En, func(e string) string { return "<p class=\"goen-admin__hint\">For example, " + e + ".</p>" }},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			for name, c := range map[string]struct {
				body     string
				examples []string
			}{
				"taxonomy": {renderComponent(t, ctx, Taxonomy(frame.Page{Title: "Brands"}, &TaxonomyView{})), []string{"north-light", "cookware"}},
			} {
				for _, e := range c.examples {
					if want := tt.sentence(e); !strings.Contains(c.body, want) {
						t.Errorf("%s page lacks the hint %s", name, want)
					}
				}
			}
		})
	}
}

// TestCategoryFormImageRefusal: category image refusal renders drafts apart from saved images.
// From TestProductImagesRefusalDrafts.
func TestCategoryFormImageRefusal(t *testing.T) {
	t.Parallel()
	const alt = ` 新照片「正面」 & 側面 `
	const altEn = ` New photograph "front" & side `
	const saved = "abababababababababababababababababababababababababababababababab"
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, tt := range []struct {
			name      string
			component templ.Component
			want      map[string]string
			errorID   string
		}{
			{name: "category", component: CategoryForm(frame.Page{}, CategoryView{Slug: "c", Image: widget.HeaderImage{Image: picture.Image{Digest: saved}, Alt: "Saved description"}, ImageAltDraft: alt, ImageAltEnDraft: altEn, Errors: map[string]string{"image": "Replace the image"}}), want: map[string]string{"cat-alt": alt, "cat-alt-en": altEn}, errorID: "cat-image"},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				body := renderComponent(t, ctx, tt.component)
				assertImageDraftControls(t, body, tt.want, tt.errorID)
				if !strings.Contains(body, `alt="Saved description"`) {
					t.Error("refusal replaced the saved image description")
				}
			})
		}
	}
}

func assertImageDraftControls(t *testing.T, body string, want map[string]string, errorID string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	marked := false
	errorShown := false
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		attrs := map[string]string{}
		for _, a := range n.Attr {
			attrs[a.Key] = a.Val
		}
		id := attrs["id"]
		if _, ok := want[id]; ok {
			got[id] = imageDraftControlValue(n, attrs)
		}
		if attrs["aria-invalid"] == "true" && id != errorID {
			t.Errorf("unrefused control %q inherited an error", id)
		}
		if id == errorID {
			marked = attrs["aria-invalid"] == "true" && slices.Contains(strings.Fields(attrs["aria-describedby"]), errorID+"-error")
			if n.Data == "input" && attrs["type"] == "file" && attrs["value"] != "" {
				t.Error("file input was repopulated")
			}
		}
		if id == errorID+"-error" && n.FirstChild != nil && n.FirstChild.Data != "" {
			errorShown = true
		}
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("image refusal controls (-want +got):\n%s", diff)
	}
	if !marked || !errorShown {
		t.Errorf("image refusal %q marked/error = %t/%t, want true/true", errorID, marked, errorShown)
	}
}

func imageDraftControlValue(n *html.Node, attrs map[string]string) string {
	switch n.Data {
	case "textarea":
		if n.FirstChild != nil {
			return n.FirstChild.Data
		}
	case "select":
		var value string
		for option := range n.Descendants() {
			optionAttrs := make(map[string]string, len(option.Attr))
			for _, a := range option.Attr {
				optionAttrs[a.Key] = a.Val
			}
			if _, selected := optionAttrs["selected"]; selected {
				value = optionAttrs["value"]
			}
		}
		return value
	}
	return attrs["value"]
}

// TestTaxonomyRefusalsLinkFirstInvalid: taxonomy refusals name their form and link to the first invalid control.
// From TestProductFormRefusalSummary.
func TestTaxonomyRefusalsLinkFirstInvalid(t *testing.T) {
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
				{"brand", Taxonomy(frame.Page{}, &TaxonomyView{Which: "brands", Errors: map[string]string{"name": bad, "slug": bad}}), "/admin/taxonomy/brands#new-brands", "new-brands", "brands-name", i18n.KeyAdminTaxNewBrand, 2},
				{"category", Taxonomy(frame.Page{}, &TaxonomyView{Which: "categories", Errors: map[string]string{"icon_key": bad}}), "/admin/taxonomy/categories#new-categories", "new-categories", "categories-icon", i18n.KeyAdminTaxNewCategory, 1},
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
