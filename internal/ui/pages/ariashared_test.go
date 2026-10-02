package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// A span has no role, so an aria-label on one is ignored and the glyphs are
// read out as five black stars before the score is read again from the
// paragraph beside them. The glyphs are hidden and the paragraph is the name.
func TestReviewStarsAreHiddenFromAssistiveTechnology(t *testing.T) {
	t.Parallel()

	v := ProductView{
		Name: "Pixelight 9 Pro", Brand: "Pixelight", Slug: "pixelight-9-pro",
		SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
		PriceCents: 3690000, Rating: 4, RatingCount: 1,
		Reviews: []ProductReview{{Rating: 4, Author: "Mina", Date: "2026-10-01"}},
	}
	out := renderProductInLocale(t, i18n.WithLocale(t.Context(), i18n.En), &v)

	if !strings.Contains(out, `<span class="goen-pdp__reviewstars" aria-hidden="true">`) {
		t.Error("a review's star glyphs are not hidden from assistive technology")
	}
	if strings.Contains(out, `goen-pdp__reviewstars" aria-label`) {
		t.Error("a review's star span carries an aria-label, which a span ignores")
	}
	if !strings.Contains(out, "Rated 4") {
		t.Error("the review's score is no longer said in words")
	}
}

// The header search and the comparison picker are both search landmarks on
// /compare, and two landmarks of one kind need names that tell them apart.
func TestTheComparePickerIsANamedSearchLandmark(t *testing.T) {
	t.Parallel()

	out := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), comparePicker(CompareView{}))
	if !strings.Contains(out, `role="search" aria-label="`) {
		t.Errorf("the comparison picker is an unnamed search landmark: %s", out)
	}
}
