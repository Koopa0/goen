package pages

import (
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestJoinListFollowsTheReadersLanguage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		locale i18n.Locale
		names  []string
		want   string
	}{
		{i18n.ZhHant, nil, ""},
		{i18n.ZhHant, []string{"書籍"}, "書籍"},
		{i18n.ZhHant, []string{"書籍", "文具"}, "書籍、文具"},
		{i18n.ZhHant, []string{"書籍", "文具", "食品"}, "書籍、文具、食品"},
		{i18n.En, []string{"Books"}, "Books"},
		{i18n.En, []string{"Books", "Stationery"}, "Books and Stationery"},
		{i18n.En, []string{"Books", "Stationery", "Food"}, "Books, Stationery and Food"},
	}
	for _, tt := range tests {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		if got := joinList(ctx, tt.names); got != tt.want {
			t.Errorf("%s joinList(%q) = %q, want %q", tt.locale, tt.names, got, tt.want)
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
	for locale, items := range nav {
		ctx := layouts.WithTopNav(i18n.WithLocale(t.Context(), locale), items)
		home, about, hero := HomeMeta(ctx), AboutMeta(ctx), DefaultHero(ctx)

		for _, item := range items {
			for name, got := range map[string]string{
				"home description":  home.Description,
				"about description": about.Description,
				"built-in hero":     hero.Body,
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
		list := joinList(ctx, names)
		page := renderComponent(t, ctx, About(about))
		if !strings.Contains(page, `<h1 class="about__title">`+i18n.T(ctx, i18n.KeyAboutTitle)+`</h1>`) {
			t.Errorf("%s about page's heading is not its title", locale)
		}
		if !strings.Contains(page, `<p class="about__valuebody">`+html.EscapeString(list)+`</p>`) {
			t.Errorf("%s about page does not list the categories %q", locale, list)
		}
	}
}

func TestHomeAndAboutSayNothingAboutCategoriesTheyCannotRead(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	if d := HomeMeta(ctx).Description; d != "" {
		t.Errorf("home description without categories = %q, want none", d)
	}
	if d := AboutMeta(ctx).Description; d != "" {
		t.Errorf("about description without categories = %q, want none", d)
	}
	if b := DefaultHero(ctx).Body; b != "" {
		t.Errorf("built-in hero body without categories = %q, want none", b)
	}

	home := renderComponent(t, ctx, Home(HomeMeta(ctx), HomeView{Hero: DefaultHero(ctx)}))
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

// While a campaign runs the built-in hero is that campaign: its title, no
// department list, and one button to its page.
func TestBuiltInHeroAnnouncesARunningCampaign(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := layouts.WithTopNav(i18n.WithLocale(t.Context(), locale),
			[]layouts.NavItem{{Slug: "books", Name: "Books"}})
		hero := CampaignHero(ctx, "Autumn desk sale", "autumn-desk")

		if hero.Headline != "Autumn desk sale" {
			t.Errorf("%s headline = %q, want the campaign title", locale, hero.Headline)
		}
		if hero.Body != "" || hero.SecondaryCTA.Shown() {
			t.Errorf("%s campaign hero carries %q and a secondary button; want neither", locale, hero.Body)
		}
		if hero.PrimaryCTA.Href != "/s/autumn-desk" || hero.PrimaryCTA.Label != i18n.T(ctx, i18n.KeyHeroCampaignCTA) {
			t.Errorf("%s primary button = %+v, want the campaign link", locale, hero.PrimaryCTA)
		}

		page := renderComponent(t, ctx, Home(HomeMeta(ctx), HomeView{Hero: hero}))
		if got := strings.Count(page, `class="goen-btn `); got < 1 || strings.Count(page, `goen-hero__actions`) != 1 {
			t.Errorf("%s hero actions did not render", locale)
		}
		if strings.Contains(page, "goen-btn--outline") {
			t.Errorf("%s hero renders a secondary outline button", locale)
		}
	}
}
