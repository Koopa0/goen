package home

import (
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/site"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestMetaNamesCategories: The home and about pages name the shop's departments
// from the header's category row, so a sentence about them cannot drift from
// the catalogue.
func TestMetaNamesCategories(t *testing.T) {
	t.Parallel()
	nav := map[i18n.Locale][]frame.NavItem{
		i18n.ZhHant: {{Slug: "books", Name: "書籍"}, {Slug: "kitchen", Name: "廚房用品"}, {Slug: "food", Name: "食品"}},
		i18n.En:     {{Slug: "books", Name: "Books"}, {Slug: "kitchen", Name: "Kitchen"}, {Slug: "food", Name: "Food"}},
	}
	wantDescription := map[i18n.Locale]string{
		i18n.ZhHant: "goen 的館別：書籍、廚房用品、食品。",
		i18n.En:     "Departments at goen: Books, Kitchen and Food.",
	}
	for locale, items := range nav {
		ctx := frame.WithTopNav(i18n.WithLocale(t.Context(), locale), items)
		home, about := Meta(ctx), site.AboutMeta(ctx)
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
		list := i18n.JoinList(ctx, names)
		page := renderComponent(t, ctx, site.About(about))
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

// TestMetaNoCategories holds that the home and about pages say nothing about
// categories they cannot read.
func TestMetaNoCategories(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	if d := Meta(ctx).Description; d != "" {
		t.Errorf("home description without categories = %q, want none", d)
	}
	if d := site.AboutMeta(ctx).Description; d != "" {
		t.Errorf("about description without categories = %q, want none", d)
	}

	home := renderComponent(t, ctx, Page(Meta(ctx), View{}))
	if !strings.Contains(home, "<title>"+i18n.T(ctx, i18n.KeySiteTitle)+"</title>") {
		t.Error("the home page's title is not the shop's name alone")
	}
	if strings.Contains(home, `<meta name="description"`) {
		t.Error("the home page states a description that names no category")
	}
	about := renderComponent(t, ctx, site.About(site.AboutMeta(ctx)))
	if strings.Contains(about, `<h2 class="about__valuetitle">`+i18n.T(ctx, i18n.KeyAboutCurated)+`</h2>`) {
		t.Error("the about page shows a categories card with nothing in it")
	}
}
