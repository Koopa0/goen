package slides

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/hero"
)

// TestSlidesHistoryNavigation: the slide history has labelled navigation in both languages.
func TestSlidesHistoryNavigation(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			view := HeroView{Bound: keyset.Bound{First: "/admin/slides#hero-history", Next: "/admin/slides?after=slide-token#hero-history"}}
			got, label := historyNavigation(t, ctx, renderComponent(t, ctx, Slides(frame.Page{}, &view)), "hero-history")
			if diff := cmp.Diff([]string{view.Bound.First, view.Bound.Next}, got); diff != "" {
				t.Errorf("slide queue navigation (-want +got):\n%s", diff)
			}
			if want := i18n.T(ctx, i18n.KeyAdminHomeScheduled); label != want {
				t.Errorf("queue heading = %q, want %q", label, want)
			}
			view.Bound = keyset.Bound{PastEnd: true, First: "/admin/slides#hero-history"}
			body := renderComponent(t, ctx, Slides(frame.Page{}, &view))
			if !strings.Contains(body, i18n.T(ctx, i18n.KeyPageEmpty)) {
				t.Error("a past-end page must explain the empty page")
			}
			if strings.Contains(body, i18n.T(ctx, i18n.KeyAdminHomeEmpty)) {
				t.Error("a past-end page claims the entire queue is empty")
			}
		})
	}
}

// historyNavigation is the links of the pager labelled by heading, checking
// that its next link is named in the page's language, and the heading's text.
func historyNavigation(t *testing.T, ctx context.Context, body, heading string) (links []string, label string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	pagers := 0
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		if homeAttribute(n, "id") == heading {
			label = homeText(n)
		}
		if n.Data != "nav" || homeAttribute(n, "class") != "goen-pager" {
			continue
		}
		pagers++
		if id := homeAttribute(n, "aria-labelledby"); id != heading {
			t.Errorf("pager is labelled by %q, want %q", id, heading)
		}
		links = []string{}
		for a := range n.Descendants() {
			if a.Type == html.ElementNode && a.Data == "a" {
				links = append(links, homeAttribute(a, "href"))
				if homeAttribute(a, "rel") == "next" && homeText(a) != i18n.T(ctx, i18n.KeyNextPage) {
					t.Errorf("next link has no translated accessible name: %q", homeText(a))
				}
			}
		}
	}
	if pagers != 1 {
		t.Errorf("%d pagers on the page, want 1", pagers)
	}
	return links, label
}

// TestHeroView_IsShowingLaterPage: showing-slide identity does not depend on the management page.
func TestHeroView_IsShowingLaterPage(t *testing.T) {
	t.Parallel()
	live := HeroSlide{ID: "live-on-second-page", Active: true, InWindow: true}
	queued := HeroSlide{ID: "queued-on-second-page", Active: true, InWindow: true}
	v := HeroView{Rows: []HeroSlide{queued, live}, Carousel: []hero.Slide{
		{Source: hero.SourceScheduled, ID: "live-on-first-page"},
		{Source: hero.SourceScheduled, ID: live.ID},
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
