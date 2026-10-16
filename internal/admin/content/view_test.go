package content

import (
	"slices"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// The tests in this file are the content halves of cross-feature tests that
// still live in internal/ui/pages/admin, as in #2081, #2082, #2114 and #2129:
// each half moved here with the page it renders and the other views stay behind.

// From copy_test.go: TestEveryExampleHintReadsInFull, which keeps the product half.
func TestEveryFAQExampleHintReadsInFull(t *testing.T) {
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
				"faq": {renderComponent(t, ctx, FAQ(layouts.Page{Title: "FAQ"}, &FAQView{})), []string{"Orders"}},
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

// From image_draft_test.go, moved whole.
func TestHeroImageRefusalRendersTheCompleteDraft(t *testing.T) {
	t.Parallel()
	v := HeroView{Draft: HeroDraft{Eyebrow: " 原始副標 ", Headline: " 原始標題 ", Body: " 原始內文 ", PrimaryLabel: " 主按鈕 ", PrimaryHref: "/deals?q=1&sort=price", SecondLabel: " 次按鈕 ", SecondHref: "/about", ImageAlt: " 原始圖片 ", Days: "007", EyebrowEn: " Raw kicker ", HeadlineEn: " Raw title ", BodyEn: " Raw body ", PrimaryLabelEn: " Primary ", SecondLabelEn: " Secondary ", ImageAltEn: " Raw picture "}, Errors: map[string]string{"image": "Replace the image"}}
	want := map[string]string{"h-eyebrow": " 原始副標 ", "h-headline": " 原始標題 ", "h-body": " 原始內文 ", "h-plabel": " 主按鈕 ", "h-phref": "/deals?q=1&sort=price", "h-slabel": " 次按鈕 ", "h-shref": "/about", "h-alt": " 原始圖片 ", "h-days": "007", "h-eyebrow-en": " Raw kicker ", "h-headline-en": " Raw title ", "h-body-en": " Raw body ", "h-plabel-en": " Primary ", "h-slabel-en": " Secondary ", "h-alt-en": " Raw picture "}
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			assertImageDraftControls(t, renderComponent(t, i18n.WithLocale(t.Context(), locale), Home(layouts.Page{}, &v)), want, "h-image")
		})
	}
}

// From image_draft_test.go, copied: the product tests there use it too.
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

// From image_draft_test.go, copied with assertImageDraftControls.
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

// From images_test.go, moved whole.

// The hero list's 160px tiles get the 400px rendition, not the original upload.
func TestAdminHeroTilesOfferTheSmallRendition(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("cd", 32)
	v := &HeroView{Rows: []HeroSlide{{ID: "s", Headline: "h", ImageDigest: digest, Active: true}}}
	var b strings.Builder
	if err := Home(layouts.Page{Title: "Home"}, v).Render(i18n.WithLocale(t.Context(), i18n.En), &b); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`src="/media/` + digest + `"`, `/media/` + digest + `/400 400w`, `sizes="160px"`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the hero tile lacks %q", want)
		}
	}
}

// From TestDeskRefusalsNameTheirFormAndLinkToTheFirstInvalidControl, which keeps
// the product half.
func TestContentRefusalsNameTheirFormAndLinkToTheFirstInvalidControl(t *testing.T) {
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
				{"faq-edit", FAQ(layouts.Page{}, &FAQView{Rows: []FAQEntry{{ID: "faq1", Question: "stored", Answer: "stored"}}, Edit: FAQEntry{ID: "faq1", Question: "", Answer: " kept ", QuestionEn: "bad"}, EditErrors: map[string]string{"question": bad, "question_en": bad}}), "/admin/faq/faq1#faq-faq1", "faq-faq1", "q-faq1", i18n.KeyAdminRefusalFAQ, 2},
				{"hero", Home(layouts.Page{}, &HeroView{Errors: map[string]string{"headline": bad, "primary": bad}}), "/admin/home#new-hero", "new-hero", "h-headline", i18n.KeyAdminRefusalHero, 2},
				{"banner", Home(layouts.Page{}, &HeroView{Errors: map[string]string{"message_en": bad, "banner_cta": bad}}), "/admin/home/banner#new-banner", "new-banner", "b-message-en", i18n.KeyAdminRefusalBanner, 2},
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

// From refusal_navigation_test.go, copied: the product half there uses them too.
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
