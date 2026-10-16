package pages

import (
	"strings"
	"testing"
	"unicode"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
)

// A screen reader says the role description of the hero and of each slide aloud
// ("Featured, carousel"). It is written in the page's language, not left in
// English on the Chinese page.
func TestTheHeroIsDescribedToAScreenReaderInThePagesLanguage(t *testing.T) {
	t.Parallel()
	slides := []components.HeroSlide{
		{Layout: components.SlideSplit, Tone: components.ToneMist, Title: "One"},
		{Layout: components.SlideSplit, Tone: components.ToneMist, Title: "Two"},
	}
	described := map[i18n.Locale]map[string]bool{}
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, Home(HomeMeta(ctx), HomeView{Slides: slides}))))
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
