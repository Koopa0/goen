package slides

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/uitest"
)

// The tests in this file are the slide halves of tests that render several
// desks' pages: each half lives with the page it renders.

// From image_draft_test.go, moved whole.
func TestHeroImageRefusalRendersTheCompleteDraft(t *testing.T) {
	t.Parallel()
	v := HeroView{Draft: HeroDraft{Eyebrow: " 原始副標 ", Headline: " 原始標題 ", Body: " 原始內文 ", PrimaryLabel: " 主按鈕 ", PrimaryHref: "/deals?q=1&sort=price", SecondLabel: " 次按鈕 ", SecondHref: "/about", ImageAlt: " 原始圖片 ", Days: "007", EyebrowEn: " Raw kicker ", HeadlineEn: " Raw title ", BodyEn: " Raw body ", PrimaryLabelEn: " Primary ", SecondLabelEn: " Secondary ", ImageAltEn: " Raw picture "}, Errors: map[string]string{"image": "Replace the image"}}
	want := map[string]string{"h-eyebrow": " 原始副標 ", "h-headline": " 原始標題 ", "h-body": " 原始內文 ", "h-plabel": " 主按鈕 ", "h-phref": "/deals?q=1&sort=price", "h-slabel": " 次按鈕 ", "h-shref": "/about", "h-alt": " 原始圖片 ", "h-days": "007", "h-eyebrow-en": " Raw kicker ", "h-headline-en": " Raw title ", "h-body-en": " Raw body ", "h-plabel-en": " Primary ", "h-slabel-en": " Secondary ", "h-alt-en": " Raw picture "}
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			assertImageDraftControls(t, renderComponent(t, i18n.WithLocale(t.Context(), locale), Slides(layouts.Page{}, &v)), want, "h-image")
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
	if err := Slides(layouts.Page{Title: "Slides"}, v).Render(i18n.WithLocale(t.Context(), i18n.En), &b); err != nil {
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
func TestARefusedSlideNamesItsFormAndLinksToTheFirstInvalidControl(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			bad := i18n.T(ctx, i18n.KeyAdminNoticeRefused)
			body := renderComponent(t, ctx, Slides(layouts.Page{}, &HeroView{Errors: map[string]string{"headline": bad, "primary": bad}}))
			uitest.AssertRefusalSummary(t, body, uitest.RefusedForm{
				Action: "/admin/slides#new-hero", Target: "new-hero", First: "h-headline",
				Title: i18n.T(ctx, i18n.KeyAdminRefusalHero), Count: 2,
			})
		})
	}
}
