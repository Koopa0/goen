package catalog

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestProductNothingLeft holds that a product with nothing left says so.
//
// The button says 已售完 when nothing is buyable, whether every option is gone or
// only the chosen combination.
func TestProductNothingLeft(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	gone := i18n.T(ctx, i18n.KeySoldOut)

	tests := []struct {
		name        string
		anySellable bool
		exact       bool
		sellable    bool
		want        bool
	}{
		{name: "nothing chosen and nothing to choose", anySellable: false, want: true},
		{
			name:        "one combination gone, others buyable",
			anySellable: true, exact: true, sellable: false, want: true,
		},
		{name: "an ordinary product", anySellable: true, exact: true, sellable: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := ProductView{
				Name: "Meridian Book 14", Brand: "Meridian", Slug: "meridian-book-14",
				SelectionOK: true, AnySellable: tt.anySellable,
				Exact: tt.exact, Sellable: tt.sellable, PriceCents: 4290000,
			}
			html := renderToString(t, Product(ProductMeta(&v), &v))
			if got := strings.Contains(html, gone); got != tt.want {
				t.Errorf("a product (anySellable=%v, exact=%v, sellable=%v) says %q = %v, want %v",
					tt.anySellable, tt.exact, tt.sellable, gone, got, tt.want)
			}
		})
	}
}

// TestProductFirstReview holds that a product with no reviews can receive its
// first.
//
// It holds a bootstrap deadlock: the form that writes a review lives inside the
// reviews section, so gating that section on RatingCount > 0 leaves a new
// product unable to receive its first.
func TestProductFirstReview(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	fresh := ProductView{
		Name: "Newly Listed", Brand: "Meridian", Slug: "newly-listed",
		SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
		PriceCents: 100000, SignedIn: true, ReviewStanding: ReviewOpen, RatingCount: 0,
	}
	html := renderToString(t, Product(ProductMeta(&fresh), &fresh))

	if !strings.Contains(html, `action="/p/newly-listed/reviews#write-review"`) {
		t.Error("a product with no reviews offers no way to write one, so it can " +
			"never have any")
	}
	if !strings.Contains(html, i18n.T(ctx, i18n.KeyNoReviewsYet)) {
		t.Error("the section renders with no reviews and says nothing about why it is empty")
	}
	// The score summary is what depends on there being ratings. Rendering
	// "0.0" above an empty bar chart is worse than saying nobody has reviewed it.
	if strings.Contains(html, `class="goen-pdp__score"`) {
		t.Error("a product with no reviews renders a score")
	}

	rated := fresh
	rated.RatingCount = 3
	rated.Rating = 4.5
	withScore := renderToString(t, Product(ProductMeta(&rated), &rated))
	if !strings.Contains(withScore, `class="goen-pdp__score"`) {
		t.Error("a rated product lost its score summary")
	}
}

// TestProductOpinionsNoneShareRow holds that the reviews and questions sections
// share one row while nobody has reviewed or asked, and stack as before once
// either has something to show.
func TestProductOpinionsNoneShareRow(t *testing.T) {
	t.Parallel()
	const row = `<div class="goen-pdp__opinions">`

	tests := []struct {
		name string
		view ProductView
		want bool
	}{
		{name: "no review and no question", want: true},
		{
			name: "one review",
			view: ProductView{RatingCount: 1, Rating: 5, Reviews: []ProductReview{{Rating: 5, Body: "Good."}}},
		},
		{name: "one question", view: ProductView{Questions: []Question{{Body: "Is it waterproof?"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := tt.view
			v.Name, v.Slug, v.PriceCents = "Sample", "sample", 100000
			html := renderToString(t, Product(ProductMeta(&v), &v))
			start := strings.Index(html, row)
			if got := start >= 0; got != tt.want {
				t.Fatalf("%s: %q present = %v, want %v", tt.name, row, got, tt.want)
			}
			if !tt.want {
				return
			}
			end := strings.Index(html[start:], `id="related-heading"`)
			if end < 0 {
				end = len(html) - start
			}
			inside := html[start : start+end]
			for _, id := range []string{`id="reviews"`, `id="questions"`} {
				if !strings.Contains(inside, id) {
					t.Errorf("the shared row does not hold %s", id)
				}
			}
		})
	}
}

// TestProductRatingBarsBeyondList holds that the distribution is drawn only when
// there are more ratings than the page lists; otherwise every rating is already
// a listed review.
func TestProductRatingBarsBeyondList(t *testing.T) {
	t.Parallel()
	review := ProductReview{Rating: 4, Body: "Good."}
	tests := []struct {
		name     string
		count    int64
		reviews  []ProductReview
		wantBars bool
	}{
		{name: "two ratings, two listed", count: 2, reviews: []ProductReview{review, review}},
		{name: "as many ratings as the page lists", count: ReviewCount, reviews: make([]ProductReview, ReviewCount)},
		{name: "nine ratings, six listed", count: 9, reviews: make([]ProductReview, ReviewCount), wantBars: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := ProductView{
				Name: "Sample", Slug: "sample", PriceCents: 100000,
				RatingCount: tt.count, Rating: 4, Reviews: tt.reviews,
			}
			html := renderToString(t, Product(ProductMeta(&v), &v))
			if !strings.Contains(html, `class="goen-pdp__score"`) {
				t.Error("the score summary is gone")
			}
			if got := strings.Contains(html, `class="goen-pdp__bars"`); got != tt.wantBars {
				t.Errorf("bars drawn = %v, want %v", got, tt.wantBars)
			}
		})
	}
}

// TestProductAskStates holds the three Q&A states of the product page as one
// matrix.
//
// It holds the three Q&A states as one matrix: signed-out visitors get a return
// to #questions, signed-in visitors get the form without a sign-in hint, and a
// refused draft stays in the textarea.
func TestProductAskStates(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	base := ProductView{
		Name: "Pixelight 9 Pro", Brand: "Meridian", Slug: "pixelight-9-pro",
		SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
		PriceCents: 3690000,
	}
	askAction := `action="/p/pixelight-9-pro/questions#questions"`
	signInHint := i18n.T(ctx, i18n.KeySignInToAsk)

	out := base
	html := renderToString(t, Product(ProductMeta(&out), &out))
	if strings.Contains(html, askAction) {
		t.Error("signed-out Q&A still renders the ask form")
	}
	if !strings.Contains(html, out.AskSignInHref()) {
		t.Errorf("signed-out Q&A has no return to #questions; want href %q", out.AskSignInHref())
	}

	in := base
	in.SignedIn = true
	signedIn := renderToString(t, Product(ProductMeta(&in), &in))
	if !strings.Contains(signedIn, askAction) {
		t.Error("signed-in Q&A lost the ask form")
	}
	if strings.Contains(signedIn, in.AskSignInHref()) {
		t.Error("signed-in Q&A still offers the sign-in link")
	}
	if strings.Contains(signedIn, signInHint) {
		t.Error("signed-in Q&A still tells the customer to sign in")
	}

	refused := in
	refused.AskOutcome = "bad"
	refused.AskDraft = "這個草稿在 422 之後還必須留在欄位裡"
	rejected := renderToString(t, Product(ProductMeta(&refused), &refused))
	if !strings.Contains(rejected, refused.AskDraft) {
		t.Error("rejected Q&A lost the submitted draft")
	}
	if !strings.Contains(rejected, i18n.T(ctx, i18n.KeyQuestionRefused)) {
		t.Error("rejected Q&A has no field error")
	}
	if !strings.Contains(rejected, `aria-invalid="true"`) {
		t.Error("rejected Q&A does not mark the textarea invalid")
	}
	if strings.Contains(rejected, signInHint) {
		t.Error("rejected Q&A tells a signed-in customer to sign in")
	}
}
