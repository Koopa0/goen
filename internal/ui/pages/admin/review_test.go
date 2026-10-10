package admin

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestReviewRowsShowNumericRatingsAndBackOfficeProducts(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			for rating := 1; rating <= 5; rating++ {
				view := ReviewsView{Rows: []Review{{Slug: "screen", Product: "Screen", Rating: rating}}}
				page := renderComponent(t, ctx, Reviews(layouts.Page{}, view))
				doc, err := html.Parse(strings.NewReader(page))
				if err != nil {
					t.Fatal(err)
				}
				format := "%d/5"
				if locale == i18n.ZhHant {
					format = "%d／5"
				}
				want := fmt.Sprintf(format, rating)
				if !hasElement(doc, func(n *html.Node) bool {
					return n.Data == "span" && attr(n, "class") != "goen-sr-only" && attr(n, "aria-hidden") != "true" && n.FirstChild != nil && n.FirstChild.Data == want
				}) {
					t.Errorf("rating %d has no visible, accessible %q", rating, want)
				}
				if !hasElement(doc, func(n *html.Node) bool {
					return n.Data == "a" && attr(n, "href") == "/admin/products/screen" && n.FirstChild != nil && n.FirstChild.Data == "Screen"
				}) {
					t.Error("review product does not link to the back office")
				}
			}
			label := "3 stars and below"
			if locale == i18n.ZhHant {
				label = "只看 3 星以下"
			}
			page := renderComponent(t, ctx, Reviews(layouts.Page{}, ReviewsView{}))
			if !strings.Contains(page, label) || !strings.Contains(page, `href="/admin/reviews?rating=3"`) {
				t.Error("reviews have no three-stars-and-below filter link")
			}
		})
	}
}

func TestReviewFilterSelectionAndEmptyState(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, filtered := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/filtered=%t", locale, filtered), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				view := ReviewsView{ThreeStarsAndBelow: filtered}
				page := renderComponent(t, ctx, Reviews(layouts.Page{}, view))
				doc, err := html.Parse(strings.NewReader(page))
				if err != nil {
					t.Fatal(err)
				}
				for _, href := range []string{"/admin/reviews", "/admin/reviews?rating=3"} {
					selected := (href == "/admin/reviews?rating=3") == filtered
					if !hasElement(doc, func(n *html.Node) bool {
						return n.Data == "a" && attr(n, "class") == "ui-filter" && attr(n, "href") == href && (attr(n, "aria-current") == "page") == selected
					}) {
						t.Errorf("filter link %s does not have selected=%t", href, selected)
					}
				}
				key := i18n.KeyAdminReviewsEmpty
				if filtered {
					key = i18n.KeyAdminReviewsFilteredEmpty
					if strings.Contains(page, i18n.T(ctx, i18n.KeyAdminReviewsEmpty)) {
						t.Error("empty filtered list says no reviews exist")
					}
				}
				if !strings.Contains(page, i18n.T(ctx, key)) {
					t.Error("missing review empty state")
				}
			})
		}
	}
}
