package pages

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestThePageMarksTheCategoryYouAreIn holds a field that was read and never written.
//
// layouts.Page.Nav decides which top-level category the header marks as current —
// the is-active class and, more importantly, aria-current="page". It was declared
// with the header, read on every render, and assigned by NOTHING: no navigation item
// had ever been highlighted, and a screen reader was never told where the visitor
// was.
//
// That is layouts.Page.CartCount exactly, in the same struct, three fields down. The
// fix follows the same rule: it is set where the page's chrome is BUILT, not by each
// handler, because a field every caller must remember to fill is a field that goes
// unfilled.
func TestThePageMarksTheCategoryYouAreIn(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	tests := []struct {
		name string
		view ListingView
		want string
	}{
		{
			name: "a root category marks itself",
			view: ListingView{Slug: "phones", Name: "手機"},
			want: "phones",
		},
		{
			name: "a child marks its ROOT, which is what the header shows",
			view: ListingView{
				Slug: "chargers", Name: "充電與線材",
				Crumbs: []Crumb{{Slug: "accessories", Name: "周邊配件"}},
			},
			want: "accessories",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ListingMeta(ctx, tt.view).Nav; got != tt.want {
				t.Errorf("ListingMeta(%q).Nav = %q, want %q", tt.view.Slug, got, tt.want)
			}
		})
	}

	// A PDP marks the category its product is in, for the same reason: somebody who
	// followed 手機 → a phone should still see 手機 marked.
	pdp := ProductView{
		Name: "Pixelight 9 Pro", Brand: "Pixelight",
		CategorySlug: "chargers",
		Crumbs:       []Crumb{{Slug: "accessories", Name: "周邊配件"}},
	}
	if got := ProductMeta(&pdp).Nav; got != "accessories" {
		t.Errorf("ProductMeta().Nav = %q, want accessories", got)
	}

	// And a page outside the tree marks nothing. current() is false for an empty
	// slug precisely so an unrelated page never highlights a category.
	if got := (ProductMeta(&ProductView{Name: "x", Brand: "y"})).Nav; got != "" {
		t.Errorf("a product with no category marks %q", got)
	}
}
