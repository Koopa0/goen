package banners

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/frame"
)

func TestBannerHistoryHasLabelledNavigationInBothLanguages(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			view := BannersView{Bound: keyset.Bound{First: "/admin/banners#banner-history", Next: "/admin/banners?after=banner-token#banner-history"}}
			doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, Banners(frame.Page{}, &view))))
			if err != nil {
				t.Fatal(err)
			}
			var links []string
			var label string
			pagers := 0
			for n := range doc.Descendants() {
				if n.Type != html.ElementNode {
					continue
				}
				if attr(n, "id") == "banner-history" {
					label = textOf(n)
				}
				if n.Data != "nav" || attr(n, "class") != "goen-pager" {
					continue
				}
				pagers++
				if id := attr(n, "aria-labelledby"); id != "banner-history" {
					t.Errorf("pager is labelled by %q, want banner-history", id)
				}
				for a := range n.Descendants() {
					if a.Type == html.ElementNode && a.Data == "a" {
						links = append(links, attr(a, "href"))
						if attr(a, "rel") == "next" && textOf(a) != i18n.T(ctx, i18n.KeyNextPage) {
							t.Errorf("next link has no translated accessible name: %q", textOf(a))
						}
					}
				}
			}
			if pagers != 1 {
				t.Errorf("%d pagers on the page, want 1", pagers)
			}
			if diff := cmp.Diff([]string{view.Bound.First, view.Bound.Next}, links); diff != "" {
				t.Errorf("banner queue navigation (-want +got):\n%s", diff)
			}
			if want := i18n.T(ctx, i18n.KeyAdminHomeBannerCurrent); label != want {
				t.Errorf("queue heading = %q, want %q", label, want)
			}
			view.Bound = keyset.Bound{PastEnd: true, First: "/admin/banners#banner-history"}
			body := renderComponent(t, ctx, Banners(frame.Page{}, &view))
			if !strings.Contains(body, i18n.T(ctx, i18n.KeyPageEmpty)) {
				t.Error("a past-end page must explain the empty page")
			}
			if strings.Contains(body, i18n.T(ctx, i18n.KeyAdminHomeBannerEmpty)) {
				t.Error("a past-end page claims the entire queue is empty")
			}
		})
	}
}
