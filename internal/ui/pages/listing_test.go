package pages

import (
	"fmt"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
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

// TestAComparisonCanBeBuiltFromAListing holds the one path into /compare.
//
// The comparison lives in the URL and nowhere else, which is what makes it
// shareable and correct under the back button. That also means every link into
// it has to carry the set — and none did. The PDP's button appended the product
// it was on to whatever the PDP's own URL already held, /compare's table linked
// each column back to a bare /p/{slug}, and the too-few empty state pointed at
// the home page. So a shopper could reach /compare with exactly ONE product,
// forever: a feature with a decision record, a localized spec table and a lock
// of its own, that nothing on the site could produce a second column for.
//
// The listing is where somebody chooses between candidates, so the listing is
// where the set is built. A GET form, because a comparison writes nothing.
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
	// dozen identical announcements. check-layout asserts the name exists; only
	// this can assert it says which product.
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

// TestACheapestPriceSaysItIsTheCheapest holds a number that read as a promise.
//
// A tile and an unchosen product page both render the cheapest BUYABLE
// variant's price — the query's own column is called min_price_cents — with
// nothing marking it as the bottom of a range. Seven of the seed's seventeen
// active products span more than one price, so for 41% of the catalogue the
// figure beside the name was not the price of the thing the shopper had in
// mind, on the page where they decide whether to open it.
//
// The PDP is the sharper half: there the number sits beside a buy button with
// the option picker still unselected, and choosing the 512GB changes it.
func TestACheapestPriceSaysItIsTheCheapest(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	// The whole rendered figure, not a loose word: 最低 beside a price is also
	// the price FILTER's label two columns to the left, so asserting the word
	// alone passes on a page that never marked anything.
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
// is gone" and "choose one".
//
// SoldOut() asks about the RESOLVED variant, so it is false until somebody has
// picked every option — and on a product where every option is gone, the page
// said nothing about stock, offered a button reading 請選擇規格, and left the
// customer to work through the picker to discover that each combination it
// could build was unavailable. The information was there, spread across four
// values in the picker and absent from the two places anybody reads first.
//
// "Has this visitor chosen one" and "can anything here be bought" are different
// questions; the view knew only the first.
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

// TestAProductWithNoReviewsCanReceiveItsFirst holds a bootstrap deadlock.
//
// The reviews SECTION was rendered only when RatingCount > 0, and the form that
// writes a review lives inside it. So a product with no reviews offered no way
// to leave one, and no product could ever receive its first through the UI. The
// back office can create a product; every product it creates was born unable to
// be reviewed.
//
// Nothing caught it because the dev seed gives all 15 active products reviews,
// so the state exists in no fixture and on no page check-layout visits — the
// same reason /compare's unreachable state survived.
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

// TestARefundedOrderCanFileAnAllowance holds a capability that was claimed
// delivered and had no door.
//
// internal/invoice has filed 折讓 since the feature shipped — real ECPay call,
// real error codes, a test for the IA_Allow_No field that a review of the live
// API taught. But admin.Invoicer, the interface the back office is built
// against, declared only Documents/Issue/Void: Allowance was not in it, so no
// handler could call it even if a route had existed, and none did. README.md
// stated the 折讓 document as issued through 綠界's API.
//
// A customer was refunded while the 統一發票 still recorded the whole sale.
// That is mistake #35's shape — a feature every guard reports as wired that no
// path can reach — and the guard blind to it is the same one:
// TestEveryHardCodedLinkResolvesToARoute asks whether a link RESOLVES, never
// whether a capability has a door.
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
	// Defaulted to what actually went back: a figure typed from memory is how
	// the wrong number reaches the 財政部.
	if !strings.Contains(html, `value="849"`) {
		t.Error("the allowance amount does not default to the refunded total")
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
}
