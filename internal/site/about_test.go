package site

import (
	"html"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
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

// The home and about pages name the shop's departments from the header's
// category row, so a sentence about them cannot drift from the catalogue.
func TestHomeAndAboutNameTheShopsOwnCategories(t *testing.T) {
	t.Parallel()
	nav := map[i18n.Locale][]layouts.NavItem{
		i18n.ZhHant: {{Slug: "books", Name: "書籍"}, {Slug: "kitchen", Name: "廚房用品"}, {Slug: "food", Name: "食品"}},
		i18n.En:     {{Slug: "books", Name: "Books"}, {Slug: "kitchen", Name: "Kitchen"}, {Slug: "food", Name: "Food"}},
	}
	wantDescription := map[i18n.Locale]string{
		i18n.ZhHant: "goen 的館別：書籍、廚房用品、食品。",
		i18n.En:     "Departments at goen: Books, Kitchen and Food.",
	}
	for locale, items := range nav {
		ctx := layouts.WithTopNav(i18n.WithLocale(t.Context(), locale), items)
		home, about := pages.HomeMeta(ctx), AboutMeta(ctx)
		if about.Description != wantDescription[locale] {
			t.Errorf("%s AboutMeta().Description = %q, want %q", locale, about.Description, wantDescription[locale])
		}

		for _, item := range items {
			for name, got := range map[string]string{
				"home description":  home.Description,
				"about description": about.Description,
			} {
				if !strings.Contains(got, item.Name) {
					t.Errorf("%s %s %q does not name %s", locale, name, got, item.Name)
				}
			}
		}
		if home.Description == about.Description {
			t.Errorf("%s home and about share the description %q", locale, home.Description)
		}
		if home.Title != "" {
			t.Errorf("%s home title = %q, want none, so the tab reads as the shop's name", locale, home.Title)
		}

		names := make([]string, 0, len(items))
		for _, item := range items {
			names = append(names, item.Name)
		}
		list := layouts.JoinList(ctx, names)
		page := renderComponent(t, ctx, About(about))
		if want := `<meta name="description" content="` + html.EscapeString(wantDescription[locale]) + `"`; !strings.Contains(page, want) {
			t.Errorf("%s about page omits description %q", locale, wantDescription[locale])
		}
		if !strings.Contains(page, `<h1 class="about__title">`+i18n.T(ctx, i18n.KeyAboutTitle)+`</h1>`) {
			t.Errorf("%s about page's heading is not its title", locale)
		}
		wantTitle := map[i18n.Locale]string{i18n.ZhHant: "館別", i18n.En: "Departments"}[locale]
		if !strings.Contains(page, `<h2 class="about__valuetitle">`+wantTitle+`</h2>`) {
			t.Errorf("%s about page does not head its department list %q", locale, wantTitle)
		}
		if !strings.Contains(page, `<p class="about__valuebody">`+html.EscapeString(list)+`</p>`) {
			t.Errorf("%s about page does not list the categories %q", locale, list)
		}
	}
}

func TestHomeAndAboutSayNothingAboutCategoriesTheyCannotRead(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	if d := pages.HomeMeta(ctx).Description; d != "" {
		t.Errorf("home description without categories = %q, want none", d)
	}
	if d := AboutMeta(ctx).Description; d != "" {
		t.Errorf("about description without categories = %q, want none", d)
	}

	home := renderComponent(t, ctx, pages.Home(pages.HomeMeta(ctx), pages.HomeView{}))
	if !strings.Contains(home, "<title>"+i18n.T(ctx, i18n.KeySiteTitle)+"</title>") {
		t.Error("the home page's title is not the shop's name alone")
	}
	if strings.Contains(home, `<meta name="description"`) {
		t.Error("the home page states a description that names no category")
	}
	about := renderComponent(t, ctx, About(AboutMeta(ctx)))
	if strings.Contains(about, `<h2 class="about__valuetitle">`+i18n.T(ctx, i18n.KeyAboutCurated)+`</h2>`) {
		t.Error("the about page shows a categories card with nothing in it")
	}
}

// The about page's row of ui/pages' TestEveryPageKindSendsItsOwnLinkPreviewTags, which lives here since the page does.
func TestAboutSendsItsOwnLinkPreviewTags(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(layouts.WithRequestPath(t.Context(), "/about"), i18n.ZhHant)
	head := renderComponent(t, layouts.WithSiteOrigin(ctx, "https://goen.test"), layouts.Base(AboutMeta(ctx)))
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

// The about page's entry of ui/pages' TestNoPageNamesAnInventedCompanyOrAddress, which lives here since the page does.
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
