package catalog

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
)

// TestProductReviewStarsHidden holds that the product page hides a review's
// star glyphs from assistive technology and says the score in words.
//
// A span has no role, so an aria-label on one is ignored and the glyphs are
// read out as five black stars before the score is read again from the
// paragraph beside them. The glyphs are hidden and the paragraph is the name.
func TestProductReviewStarsHidden(t *testing.T) {
	t.Parallel()

	v := ProductView{
		Name: "Pixelight 9 Pro", Brand: "Pixelight", Slug: "pixelight-9-pro",
		SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
		PriceCents: 3690000, Rating: 4, RatingCount: 1,
		Reviews: []ProductReview{{Rating: 4, Author: "Mina", Date: shoptime.Date{Year: 2026, Month: time.October, Day: 1}}},
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
