package catalog

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/tone"
)

// The attribute is what app.css selects the ground on; a head without it is
// unthemed, and one carrying a tone outside the set would match no rule. The
// campaign head's half of this test is campaign's TestPageHeadTone.
func TestADepartmentHeadCarriesItsTone(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)

	listing := renderComponent(t, ctx, Listing(frame.Page{Title: "c"}, ListingView{Slug: "c", Name: "Books", Theme: &Theme{Tone: tone.Sage}}, nil, nil))
	if !strings.Contains(listing, `class="goen-pagehead" data-tone="sage"`) {
		t.Errorf("the category head does not carry its tone:\n%s", listing)
	}
	unset := renderComponent(t, ctx, Listing(frame.Page{Title: "c"}, ListingView{Slug: "c", Name: "Books"}, nil, nil))
	if !strings.Contains(unset, `data-tone="stone"`) {
		t.Error("a category head with no tone is not stone")
	}
}
