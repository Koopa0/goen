package admin

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

func TestHomeHistoryHasSeparateLabelledNavigationInBothLanguages(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			view := HeroView{
				Bound:       web.Bound{Next: "/admin/home?queue=slides&after=slide-token#hero-history"},
				BannerBound: web.Bound{First: "/admin/home?queue=banners#banner-history", Next: "/admin/home?queue=banners&after=banner-token#banner-history"},
			}
			body := renderComponent(t, ctx, Home(layouts.Page{}, &view))
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			got := map[string][]string{}
			labels := map[string]string{}
			for n := range doc.Descendants() {
				if n.Type != html.ElementNode {
					continue
				}
				if id := homeAttribute(n, "id"); id == "hero-history" || id == "banner-history" {
					labels[id] = homeText(n)
				}
				if n.Data != "nav" || homeAttribute(n, "class") != "goen-pager" {
					continue
				}
				id := homeAttribute(n, "aria-labelledby")
				got[id] = []string{}
				for a := range n.Descendants() {
					if a.Type == html.ElementNode && a.Data == "a" {
						got[id] = append(got[id], homeAttribute(a, "href"))
						if homeAttribute(a, "rel") == "next" && homeText(a) != i18n.T(ctx, i18n.KeyNextPage) {
							t.Errorf("next link has no translated accessible name: %q", homeText(a))
						}
					}
				}
			}
			want := map[string][]string{
				"hero-history":   {view.Bound.Next},
				"banner-history": {view.BannerBound.First, view.BannerBound.Next},
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("home queue navigation (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(map[string]string{"hero-history": i18n.T(ctx, i18n.KeyAdminHomeScheduled), "banner-history": i18n.T(ctx, i18n.KeyAdminHomeBannerCurrent)}, labels); diff != "" {
				t.Errorf("queue headings (-want +got):\n%s", diff)
			}
			view.Bound, view.BannerBound = web.Bound{PastEnd: true, First: "/admin/home?queue=slides#hero-history"}, web.Bound{PastEnd: true, First: "/admin/home?queue=banners#banner-history"}
			body = renderComponent(t, ctx, Home(layouts.Page{}, &view))
			if strings.Count(body, i18n.T(ctx, i18n.KeyPageEmpty)) != 2 {
				t.Error("both past-end pages must explain the empty page")
			}
			for _, key := range []i18n.Key{i18n.KeyAdminHomeEmpty, i18n.KeyAdminHomeBannerEmpty} {
				if strings.Contains(body, i18n.T(ctx, key)) {
					t.Errorf("a past-end page claims the entire queue is empty: %s", key)
				}
			}
		})
	}
}

func TestShowingSlideIdentityDoesNotDependOnTheManagementPage(t *testing.T) {
	t.Parallel()
	live := HeroSlide{ID: "live-on-second-page", Active: true, InWindow: true}
	queued := HeroSlide{ID: "queued-on-second-page", Active: true, InWindow: true}
	v := HeroView{Rows: []HeroSlide{queued, live}, Carousel: []pages.HeroSlide{
		{Source: pages.SlideScheduled, ID: "live-on-first-page"},
		{Source: pages.SlideScheduled, ID: live.ID},
	}}
	if !v.IsShowing(live) || v.IsShowing(queued) || v.IsShowing(HeroSlide{}) {
		t.Errorf("later-page showing badge: live=%v queued=%v empty=%v", v.IsShowing(live), v.IsShowing(queued), v.IsShowing(HeroSlide{}))
	}
	v.Rows = nil
	if !v.IsShowing(live) {
		t.Error("the showing badge depends on the rows loaded for management")
	}
}

func homeAttribute(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func homeText(n *html.Node) string {
	var text strings.Builder
	for child := range n.Descendants() {
		if child.Type == html.TextNode {
			text.WriteString(child.Data)
		}
	}
	return strings.TrimSpace(text.String())
}
