package pages

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestThePageMarksTheCategoryYouAreIn holds layouts.Page.Nav, which drives the
// header's is-active class and aria-current="page". It is set where the page's
// chrome is BUILT, not by each handler: a field every caller must remember to
// fill is a field that goes unfilled.
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

// TestAComparisonCanBeBuiltFromAListing holds the one path into /compare. The
// comparison lives in the URL and nowhere else, so every link into it has to
// carry the set. The listing is where somebody chooses between candidates, so
// the listing is where the set is built — a GET form, because a comparison
// writes nothing.
func TestAComparisonCanBeBuiltFromAListing(t *testing.T) {
	t.Parallel()

	view := ListingView{
		Slug: "phones", Name: "手機",
		Products: []ProductTile{
			{Slug: "pixelight-9-pro", Name: "Pixelight 9 Pro", Comparable: true},
			{Slug: "aurora-fold-2", Name: "Aurora Fold 2", Comparable: true},
		},
	}
	html := renderToString(t, Listing(ListingMeta(i18n.WithLocale(t.Context(), i18n.ZhHant), view), view))

	// The form's action and method, not merely a checkbox: a checkbox that
	// submits to the listing filters the listing.
	if !strings.Contains(html, `method="get" action="/compare"`) {
		t.Error("the listing carries no GET form to /compare")
	}
	for _, slug := range []string{"pixelight-9-pro", "aurora-fold-2"} {
		if !strings.Contains(html, `<input type="checkbox" name="p" value="`+slug+`"`) {
			t.Errorf("no compare checkbox for %q; one product can never become two", slug)
		}
	}

	// Every control needs a name, and two dozen controls called 比較 are two
	// dozen identical announcements.
	if !strings.Contains(html, `aria-label="把 Pixelight 9 Pro 加入比較"`) {
		t.Error("the checkbox does not name its product")
	}

	// And the set survives a click back out of the table, or building a third
	// column means starting from one again.
	cmp := CompareView{Products: []CompareProduct{{Slug: "a"}, {Slug: "b"}}}
	if got, want := cmp.ProductHref("a"), "/p/a?p=a&p=b"; got != want {
		t.Errorf("ProductHref(a) = %q, want %q", got, want)
	}
}

// TestACheapestPriceSaysItIsTheCheapest holds a number that reads as a promise:
// a tile and an unchosen product page both render the cheapest BUYABLE
// variant's price, so it has to be marked as the bottom of a range.
func TestACheapestPriceSaysItIsTheCheapest(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	// The whole rendered figure, not a loose word: 最低 beside a price is also
	// the price FILTER's label two columns to the left.
	marked := fmt.Sprintf(i18n.T(ctx, i18n.KeyFromPrice), "NT$25,900")

	// One price for the product: the figure IS the price, and saying "from"
	// there would be worse than saying nothing.
	one := ListingView{Slug: "phones", Name: "手機", Products: []ProductTile{
		{Slug: "solo", Name: "Solo", PriceCents: 2590000},
	}}
	if got := renderToString(t, Listing(ListingMeta(ctx, one), one)); strings.Contains(got, marked) {
		t.Errorf("a single-priced product renders %q", marked)
	}

	many := ListingView{Slug: "phones", Name: "手機", Products: []ProductTile{
		{Slug: "spread", Name: "Spread", PriceCents: 2590000, PriceVaries: true},
	}}
	if got := renderToString(t, Listing(ListingMeta(ctx, many), many)); !strings.Contains(got, marked) {
		t.Errorf("a product spanning prices states NT$25,900 as its price, unmarked")
	}

	// And the PDP marks it only while the choice is still open: once a variant
	// is resolved the price is that variant's, whatever else the product sells.
	tests := []struct {
		name          string
		varies, exact bool
		want          bool
	}{
		{name: "nothing chosen, dearer ones exist", varies: true, exact: false, want: true},
		{name: "chosen: this is its price", varies: true, exact: true, want: false},
		{name: "nothing chosen, one price only", varies: false, exact: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := ProductView{PriceVaries: tt.varies, Exact: tt.exact}
			if got := v.PriceFrom(); got != tt.want {
				t.Errorf("PriceFrom(varies=%v, exact=%v) = %v, want %v",
					tt.varies, tt.exact, got, tt.want)
			}
		})
	}
}

// TestAProductWithNothingLeftSaysSo holds the state between "this combination
// is gone" and "choose one". SoldOut() asks about the RESOLVED variant, so it
// is false until every option is picked: "has this visitor chosen one" and "can
// anything here be bought" are different questions.
func TestAProductWithNothingLeftSaysSo(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	gone := i18n.T(ctx, i18n.KeyAllSoldOut)

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
			anySellable: true, exact: true, sellable: false, want: false,
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

// TestAProductWithNoReviewsCanReceiveItsFirst holds a bootstrap deadlock: the
// form that writes a review lives inside the reviews section, so gating that
// section on RatingCount > 0 leaves a new product unable to receive its first.
func TestAProductWithNoReviewsCanReceiveItsFirst(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	fresh := ProductView{
		Name: "Newly Listed", Brand: "Meridian", Slug: "newly-listed",
		SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
		PriceCents: 100000, SignedIn: true, CanReview: true, RatingCount: 0,
	}
	html := renderToString(t, Product(ProductMeta(&fresh), &fresh))

	if !strings.Contains(html, `action="/p/newly-listed/reviews"`) {
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

// TestQAAskSurface holds the three Q&A states as one matrix: signed-out
// visitors get a return to #questions, signed-in visitors get the form
// without a sign-in hint, and a refused draft stays in the textarea.
func TestQAAskSurface(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	base := ProductView{
		Name: "Pixelight 9 Pro", Brand: "Meridian", Slug: "pixelight-9-pro",
		SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
		PriceCents: 3690000,
	}
	askAction := `action="/p/pixelight-9-pro/questions"`
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

// TestARefundedOrderCanFileAnAllowance holds the 折讓 form's door. Without it a
// customer is refunded while the 統一發票 still records the whole sale.
func TestARefundedOrderCanFileAnAllowance(t *testing.T) {
	t.Parallel()

	refunded := AdminOrderView{
		Number: "GO-260721-000387", Status: "completed",
		Committed: true, InvoicingEnabled: true, RefundedCents: 84900,
		InvoiceDocuments: []AdminInvoiceDocument{
			{Kind: "invoice", Number: "AA12345678", Status: "issued", AmountCents: 100000},
		},
	}
	html := renderToString(t, AdminOrder(layouts.Page{Title: "x"}, &refunded))

	if !strings.Contains(html, `action="/admin/orders/GO-260721-000387/invoice/allowance"`) {
		t.Error("a refunded order offers no way to file a 折讓, so the tax document " +
			"keeps recording a sale that partly did not happen")
	}
	// Displayed from what actually went back, but never posted: the database
	// derives it again under lock, so neither an operator nor a forged form owns
	// the tax amount.
	if !strings.Contains(html, `NT$849`) {
		t.Error("the allowance form does not show the authoritative refunded delta")
	}
	if strings.Contains(html, `name="amount"`) {
		t.Error("the allowance form posts an operator-controlled money field")
	}

	// And it is not offered when nothing has been refunded — an allowance
	// relieving nothing is refused downstream, and the form would be an
	// invitation to invent a figure.
	nothingBack := refunded
	nothingBack.RefundedCents = 0
	if strings.Contains(renderToString(t, AdminOrder(layouts.Page{Title: "x"}, &nothingBack)),
		"/invoice/allowance") {
		t.Error("an order with no refund is offered a 折讓 form")
	}

	partlyRelieved := refunded
	partlyRelieved.InvoiceDocuments = append(
		slices.Clone(refunded.InvoiceDocuments),
		AdminInvoiceDocument{Kind: "allowance", Number: "2026080715227214",
			Status: "issued", AmountCents: 30000},
	)
	partialHTML := renderToString(t, AdminOrder(layouts.Page{Title: "x"}, &partlyRelieved))
	if !strings.Contains(partialHTML, `NT$549`) {
		t.Error("the allowance form did not subtract the credit note already filed")
	}
}
