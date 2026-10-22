package home

import (
	"strings"
	"testing"
	"unicode"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/policy"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/tone"
)

// A screen reader says the role description of the hero and of each slide aloud
// ("Featured, carousel"). It is written in the page's language, not left in
// English on the Chinese page.
func TestTheHeroIsDescribedToAScreenReaderInThePagesLanguage(t *testing.T) {
	t.Parallel()
	slides := []hero.Slide{
		{Layout: hero.LayoutSplit, Tone: tone.Mist, Title: "One"},
		{Layout: hero.LayoutSplit, Tone: tone.Mist, Title: "Two"},
	}
	described := map[i18n.Locale]map[string]bool{}
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, Page(Meta(ctx), View{Slides: slides}))))
		if err != nil {
			t.Fatal(err)
		}
		described[locale] = map[string]bool{}
		count := 0
		for n := range doc.Descendants() {
			if n.Type != html.ElementNode {
				continue
			}
			for _, a := range n.Attr {
				if a.Key == "aria-roledescription" {
					described[locale][a.Val] = true
					count++
				}
			}
		}
		if count != 1+len(slides) {
			t.Errorf("%s: %d role descriptions, want the carousel and each of %d slides", locale, count, len(slides))
		}
	}
	for word := range described[i18n.ZhHant] {
		if !strings.ContainsFunc(word, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			t.Errorf("zh-Hant role description %q has no Chinese in it", word)
		}
		if described[i18n.En][word] {
			t.Errorf("zh-Hant role description %q is the English one", word)
		}
	}
}

// The home page's row of internal/contact's TestEveryPageKindSendsItsOwnLinkPreviewTags, which lives here since the page does.
func TestTheHomePageSendsItsOwnLinkPreviewTags(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(frame.WithRequestPath(t.Context(), "/c/books"), i18n.ZhHant)
	head := renderComponent(t, frame.WithSiteOrigin(ctx, "https://goen.test"), frame.Base(Meta(ctx)))
	for _, want := range []string{
		`og:title" content="` + i18n.T(ctx, i18n.KeySiteTitle) + `"`,
		`og:type" content="website"`,
		`og:site_name" content="goen"`,
		`og:url" content="https://goen.test/c/books"`,
		`name="twitter:card" content="summary_large_image"`,
	} {
		if !strings.Contains(head, want) {
			t.Errorf("home head omits %s", want)
		}
	}
}

// The home page draws the shop rules it is given, as the department page does;
// what the rules section says is policy's TestShopRulesStateTheStoredRules.
func TestTheHomePageEndsWithTheShopRules(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	page := renderComponent(t, ctx, Page(frame.Page{}, View{Rules: policy.Rules{FreeDeliveryCents: 300000}}))
	if !strings.Contains(page, `<section class="goen-rules" aria-labelledby="rules-heading">`) {
		t.Error("home page lacks the shop rules")
	}
	if n := strings.Count(page, "<dt>"); n != 4 {
		t.Errorf("home page states %d rules, want 4", n)
	}
}
