package site

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// The about page draws goen's mark instead of a photograph: the two rings the
// name is about, hidden from a screen reader because the heading and lead say
// it, and no picture to fetch. Its colours come from classes, because the CSP
// refuses an inline style attribute and the drawing would render black.
func TestAboutDrawsTheMarkRatherThanAPhotograph(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		page := renderIn(t, locale, About(AboutMeta(ctx)))
		art := regexp.MustCompile(`(?s)<svg class="about__art".*?</svg>`).FindString(page)
		if art == "" {
			t.Fatalf("%s about page draws no mark", locale)
		}
		if !strings.Contains(art, `aria-hidden="true"`) {
			t.Errorf("%s about drawing is announced; the heading already says what it means", locale)
		}
		if got := strings.Count(art, `class="about__ring`); got != 2 {
			t.Errorf("%s about drawing has %d rings, want the mark's 2", locale, got)
		}
		if strings.Contains(art, "style=") {
			t.Errorf("%s about drawing carries an inline style the CSP refuses", locale)
		}
		if strings.Contains(page, "<img") {
			t.Errorf("%s about page still fetches a picture", locale)
		}
	}
}

// The about page's row of internal/contact's TestEveryPageKindSendsItsOwnLinkPreviewTags, which lives here since the page does.
func TestAboutSendsItsOwnLinkPreviewTags(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(frame.WithRequestPath(t.Context(), "/about"), i18n.ZhHant)
	head := renderComponent(t, frame.WithSiteOrigin(ctx, "https://goen.test"), frame.Base(AboutMeta(ctx)))
	for _, want := range []string{
		`og:title" content="` + i18n.T(ctx, i18n.KeyAboutTitle) + ` · goen"`,
		`og:type" content="website"`,
		`og:site_name" content="goen"`,
		`og:url" content="https://goen.test/about"`,
		`name="twitter:card" content="summary_large_image"`,
	} {
		if !strings.Contains(head, want) {
			t.Errorf("about head omits %s", want)
		}
	}
}

// The about page's entry of internal/ui/frame' TestFooterContactNoInventedCompany, which lives here since the page does.
func TestTheAboutPageNamesNoInventedCompanyOrAddress(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		page := renderComponent(t, ctx, About(AboutMeta(ctx)))
		for _, forbidden := range []string{"90123456", "Co., Ltd.", "松高路", "google.com/maps"} {
			if strings.Contains(page, forbidden) {
				t.Errorf("%s/about still carries %q", locale, forbidden)
			}
		}
	}
}
