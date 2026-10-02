package pages

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestSoldOutGuidanceMatchesAvailableOptionPickers(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, withOptions := range []bool{false, true} {
			name := "no options"
			if withOptions {
				name = "with options"
			}
			t.Run(string(locale)+"/"+name, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				view := ProductView{
					Slug: "sold-out", Name: "Sold out product", VariantID: "only-variant",
					SelectionOK: true, Exact: true,
				}
				if withOptions {
					view.Options = []ProductOption{{
						Name: "colour", Label: "Colour",
						Values: []ProductOptionValue{{Value: "blue", Label: "Blue", Selected: true}},
					}}
				}
				var body strings.Builder
				if err := Product(ProductMeta(&view), &view).Render(ctx, &body); err != nil {
					t.Fatal(err)
				}
				markup := body.String()
				if got := strings.Contains(markup, i18n.T(ctx, i18n.KeyAllSoldOutHint)); got != withOptions {
					t.Errorf("variant-selection hint visible = %t, want %t", got, withOptions)
				}
				// The request carries the selection so the answer lands on the same page.
				notify := "/p/sold-out/notify"
				if withOptions {
					notify += "?colour=blue"
				}
				for _, want := range []string{
					i18n.T(ctx, i18n.KeyAllSoldOut),
					`method="post" action="` + notify + `"`,
					`name="variant" value="only-variant"`,
					i18n.T(ctx, i18n.KeyRestockSubmit),
				} {
					if !strings.Contains(markup, want) {
						t.Errorf("sold-out page is missing %q", want)
					}
				}
			})
		}
	}
}

func TestWishlistUsesSignInNavigationUntilAuthenticated(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, signedIn := range []bool{false, true} {
			for _, saved := range []bool{false, true} {
				ctx := i18n.WithLocale(t.Context(), locale)
				view := ProductView{Slug: "sample-product", Name: "Sample", VariantID: "sample-variant", SelectionOK: true, Exact: true, Available: 5, Sellable: true, SignedIn: signedIn, Saved: saved}
				var body strings.Builder
				if err := Product(ProductMeta(&view), &view).Render(ctx, &body); err != nil {
					t.Fatal(err)
				}
				markup := body.String()
				if strings.Contains(markup, `action="/account/wishlist"`) != signedIn {
					t.Fatalf("signedIn=%t: wishlist mutation form availability disagrees with authentication", signedIn)
				}
				if !signedIn {
					if wishlistSignInDestination(t, markup, i18n.T(ctx, i18n.KeyWishlistSignIn)) != "/signin?next=/p/sample-product" {
						t.Fatal("guest wishlist lacks explicit sign-in link returning to the product")
					}
					if strings.Contains(markup, `aria-label="`+i18n.T(ctx, i18n.KeyWishlistAdd)+`"`) || strings.Contains(markup, `aria-label="`+i18n.T(ctx, i18n.KeyWishlistRemove)+`"`) {
						t.Fatal("guest wishlist still promises a save/remove action")
					}
					continue
				}
				if !strings.Contains(markup, `name="slug" value="sample-product"`) || !strings.Contains(markup, `aria-pressed="`+view.SavedText()+`"`) {
					t.Fatal("authenticated wishlist lost its product or saved state")
				}
				if strings.Contains(markup, `name="action" value="remove"`) != saved {
					t.Fatal("saved wishlist does not offer removal")
				}
			}
		}
	}
}

func wishlistSignInDestination(t *testing.T, raw, label string) string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	link := findDescendant(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != "a" {
			return false
		}
		return findDescendant(n, func(child *html.Node) bool {
			return child.Type == html.TextNode && strings.TrimSpace(child.Data) == label
		}) != nil
	})
	if link == nil {
		t.Fatal("guest wishlist lacks its visible sign-in link")
	}
	return attrValue(link, "href")
}

// TestTheGalleryRidesTheSwapOnlyWhenAPhotographShowsAValue holds both halves of
// the swatch's swap. A product whose photographs show it whichever value is
// chosen keeps its gallery out of the swap; one with a photograph of a single
// value brings the gallery along, or choosing that value leaves the old picture
// on screen until a reload.
func TestTheGalleryRidesTheSwapOnlyWhenAPhotographShowsAValue(t *testing.T) {
	t.Parallel()
	for _, tagged := range []bool{false, true} {
		name := "untagged"
		want := "#buybar"
		if tagged {
			name, want = "tagged", "#gallery,#buybar"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			view := ProductView{
				Slug: "two-colours", Name: "Two colours", VariantID: "v1", SelectionOK: true,
				Images: []ProductImage{
					{URL: "/static/a.webp", Alt: "a"},
					{URL: "/static/b.webp", Alt: "b", ShowsOption: tagged},
				},
				Options: []ProductOption{{Name: "colour", Label: "Colour", Values: []ProductOptionValue{
					{Value: "blue", Label: "Blue", Href: "/p/two-colours?colour=blue", Available: true},
					{Value: "black", Label: "Black", Href: "/p/two-colours?colour=black", Available: true},
				}}},
			}
			var body strings.Builder
			if err := Product(ProductMeta(&view), &view).Render(t.Context(), &body); err != nil {
				t.Fatal(err)
			}
			doc, err := html.Parse(strings.NewReader(body.String()))
			if err != nil {
				t.Fatal(err)
			}

			var swatches []*html.Node
			galleries := 0
			var walk func(*html.Node)
			walk = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "a" && hasClass(n, "goen-swatch") {
					swatches = append(swatches, n)
				}
				if n.Type == html.ElementNode && attrValue(n, "id") == "gallery" {
					galleries++
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
			}
			walk(doc)

			if galleries != 1 {
				t.Fatalf("the page has %d #gallery elements, want exactly 1 for the swap to select", galleries)
			}
			if len(swatches) != 2 {
				t.Fatalf("found %d swatches, want 2", len(swatches))
			}
			for _, a := range swatches {
				if got := attrValue(a, "hx-select"); got != "#buybox" {
					t.Errorf("swatch %s selects %q, want #buybox", attrValue(a, "href"), got)
				}
				if got := attrValue(a, "hx-select-oob"); got != want {
					t.Errorf("swatch %s carries hx-select-oob=%q, want %q", attrValue(a, "href"), got, want)
				}
			}
		})
	}
}

// htmx puts focus back after a swap only on an element it can find again by id,
// so a control that swaps its own region has to carry one, or focus falls to the
// body and the next Tab leaves the buy box.
func TestControlsThatSwapTheirOwnRegionKeepAStableID(t *testing.T) {
	t.Parallel()
	view := ProductView{
		Slug: "sample-product", Name: "Sample", VariantID: "v", SelectionOK: true, Exact: true,
		Available: 5, Sellable: true, AnySellable: true,
		Options: []ProductOption{{
			Name: "colour", Label: "Colour",
			Values: []ProductOptionValue{
				{Value: "blue", Label: "Blue", Selected: true, Available: true, Href: "/p/sample-product?colour=blue"},
				{Value: "red", Label: "Red", Available: true, Href: "/p/sample-product?colour=red"},
			},
		}},
	}
	markup := renderToString(t, Product(ProductMeta(&view), &view))
	for _, id := range []string{"swatch-0-0", "swatch-0-1", "add-to-cart"} {
		if !strings.Contains(markup, `id="`+id+`"`) {
			t.Errorf("no element carries id=%q", id)
		}
	}

	contact := renderToString(t, ContactPanel(ContactForm{}))
	if !strings.Contains(contact, `id="contact-submit"`) {
		t.Error("the contact form's submit button has no id, so a refused submit drops focus to the body")
	}
}

func TestTheReviewFormWarnsBeforeSubmittingAndRefusesToTheForm(t *testing.T) {
	t.Parallel()
	view := ProductView{Slug: "sample-product", Name: "Sample", SignedIn: true, ReviewStanding: ReviewOpen}
	var body strings.Builder
	if err := Product(ProductMeta(&view), &view).Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &body); err != nil {
		t.Fatal(err)
	}
	markup := body.String()
	for _, want := range []string{
		`<form id="write-review" method="post" action="/p/sample-product/reviews#write-review">`,
		`minlength="` + strconv.Itoa(ReviewBodyMinRunes) + `"`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("the review form lacks %s", want)
		}
	}
}

func TestTheReviewFormIsReplacedUntilTheProductArrives(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ProductView{Slug: "sample-product", Name: "Sample", SignedIn: true, ReviewStanding: ReviewNotDelivered}
	var body strings.Builder
	if err := Product(ProductMeta(&view), &view).Render(ctx, &body); err != nil {
		t.Fatal(err)
	}
	markup := body.String()
	if strings.Contains(markup, `id="write-review"`) {
		t.Error("a customer who has not received the product is offered the review form")
	}
	if !strings.Contains(markup, i18n.T(ctx, i18n.KeyReviewAfterDelivery)) {
		t.Error("the page does not say when they can review")
	}
}

func TestTheRatingIsAKeyboardOperableStarGroupAndTheHintStatesTheMinimum(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ProductView{Slug: "sample-product", Name: "Sample", SignedIn: true, ReviewStanding: ReviewOpen}
	var body strings.Builder
	if err := Product(ProductMeta(&view), &view).Render(ctx, &body); err != nil {
		t.Fatal(err)
	}
	markup := body.String()
	if got := strings.Count(markup, `name="rating"`); got != 5 {
		t.Errorf("%d rating radios, want 5 sharing one name", got)
	}
	if strings.Count(markup, `type="radio"`) != 5 {
		t.Error("the stars are not radio inputs, so the arrow keys would not move the choice")
	}
	if !strings.Contains(markup, `id="review-body-hint"`) ||
		!strings.Contains(markup, `aria-describedby="review-body-hint"`) {
		t.Error("the minimum length is not stated in a hint the field points at")
	}
	if !strings.Contains(markup, view.ReviewBodyHint(ctx)) {
		t.Error("the hint text is missing")
	}
}

func TestTheRestockFormPostsFromTheChosenOptions(t *testing.T) {
	t.Parallel()
	view := ProductView{Slug: "book", Options: []ProductOption{
		{Name: "顏色", Values: []ProductOptionValue{{Value: "太空銀", Selected: true}, {Value: "黑"}}},
		{Name: "容量", Values: []ProductOptionValue{{Value: "16GB/512GB", Selected: true}}},
	}}
	want := "/p/book/notify?" + url.Values{"顏色": {"太空銀"}, "容量": {"16GB/512GB"}}.Encode()
	if got := view.NotifyAction(); got != want {
		t.Errorf("NotifyAction = %q; want %q", got, want)
	}
	if got := (&ProductView{Slug: "charger"}).NotifyAction(); got != "/p/charger/notify" {
		t.Errorf("a product without options posts to %q", got)
	}
}

func renderProduct(t *testing.T, v *ProductView, locale i18n.Locale) string {
	t.Helper()
	var body strings.Builder
	if err := Product(ProductMeta(v), v).Render(i18n.WithLocale(t.Context(), locale), &body); err != nil {
		t.Fatal(err)
	}
	return body.String()
}

func TestTheRestockFormStartsWithTheSignedInAddressAndTheConfirmationNamesIt(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	soldOut := func() ProductView {
		return ProductView{
			Slug: "book", Name: "Book", SelectionOK: true, Exact: true, VariantID: "v1",
			SignedIn: true, AccountEmail: "me@example.com",
		}
	}

	view := soldOut()
	if got := renderProduct(t, &view, i18n.ZhHant); !strings.Contains(got, `value="me@example.com"`) {
		t.Error("a signed-in customer's restock form does not start with their address")
	}

	view = soldOut()
	view.NotifyEmail = "typed@example.com"
	got := renderProduct(t, &view, i18n.ZhHant)
	if !strings.Contains(got, `value="typed@example.com"`) || strings.Contains(got, `value="me@example.com"`) {
		t.Error("a refused address was replaced by the account's")
	}

	view = soldOut()
	view.NotifyOutcome = NotifyRecordedForAccount
	if got := renderProduct(t, &view, i18n.ZhHant); !strings.Contains(got, "me@example.com") ||
		strings.Contains(got, i18n.T(ctx, i18n.KeyRestockDone)) {
		t.Error("the confirmation does not name the account's address")
	}

	view = soldOut()
	view.NotifyOutcome = NotifyRecorded
	if got := renderProduct(t, &view, i18n.ZhHant); !strings.Contains(got, i18n.T(ctx, i18n.KeyRestockDone)) ||
		strings.Contains(got, "me@example.com") {
		t.Error("a request for another address was confirmed as the account's")
	}
}

func TestEverySoldOutIsSaidOnce(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ProductView{
		Slug: "book", Name: "Book", SelectionOK: true, VariantID: "v1",
		Options: []ProductOption{{Name: "顏色", Label: "顏色", Values: []ProductOptionValue{{Value: "黑", Label: "黑"}}}},
	}
	got := renderProduct(t, &view, i18n.ZhHant)
	if n := strings.Count(got, i18n.T(ctx, i18n.KeyAllSoldOut)); n != 1 {
		t.Errorf("%q appears %d times, want once", i18n.T(ctx, i18n.KeyAllSoldOut), n)
	}
}

func TestAddingToTheCartLeavesTheAddressBarAlone(t *testing.T) {
	t.Parallel()
	view := ProductView{Slug: "book", Name: "Book", SelectionOK: true, VariantID: "v1", Available: 5, AnySellable: true, Sellable: true}
	got := renderProduct(t, &view, i18n.ZhHant)
	i := strings.Index(got, `hx-post="/cart/items"`)
	if i < 0 {
		t.Fatal("the add form lost its htmx post")
	}
	form := got[i : i+strings.Index(got[i:], ">")]
	if !strings.Contains(form, `hx-push-url="false"`) {
		t.Errorf("the add form pushes the redirect's address, ?added=added included: %s", form)
	}
}

func TestTheChosenOptionIsNamedNextToItsLabel(t *testing.T) {
	t.Parallel()
	view := ProductView{
		Slug: "book", Name: "Book", SelectionOK: true, VariantID: "v1",
		Options: []ProductOption{{Name: "顏色", Label: "顏色", Values: []ProductOptionValue{
			{Value: "黑", Label: "曜石黑", Selected: true}, {Value: "白", Label: "雲母白"},
		}}},
	}
	got := renderProduct(t, &view, i18n.ZhHant)
	if !strings.Contains(got, "顏色：") || !strings.Contains(got, `goen-pdp__optchosen">曜石黑`) {
		t.Error("the chosen option's name is not shown next to its label")
	}
	if en := renderProduct(t, &view, i18n.En); !strings.Contains(en, "顏色:") {
		t.Error("the English separator is not used in English")
	}
}

func buyBarMarkup(t *testing.T, v *ProductView) string {
	t.Helper()
	got := renderProduct(t, v, i18n.ZhHant)
	i := strings.Index(got, `id="buybar"`)
	if i < 0 {
		t.Fatal("the page has no #buybar for a swap to replace")
	}
	return got[i : i+strings.Index(got[i:], "</div>")]
}

func TestTheBottomBarSubmitsThePagesOwnAddForm(t *testing.T) {
	t.Parallel()
	view := ProductView{
		Slug: "book", Name: "Book", SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
		VariantID: "v1", Available: 5, PriceCents: 120000,
	}
	page := renderProduct(t, &view, i18n.ZhHant)
	if strings.Count(page, `id="pdp-add"`) != 1 || !strings.Contains(page, `action="/cart/items"`) {
		t.Fatal("the page's add form has no id for the bar to name")
	}
	bar := buyBarMarkup(t, &view)
	if !strings.Contains(bar, `type="submit"`) || !strings.Contains(bar, `form="pdp-add"`) {
		t.Errorf("the bar does not submit the page's own form: %s", bar)
	}
	if strings.Contains(bar, "<form") || strings.Contains(bar, "/cart/items") {
		t.Errorf("the bar carries a cart path of its own: %s", bar)
	}
	if !strings.Contains(bar, `data-follows="add-to-cart"`) || !strings.Contains(page, `id="add-to-cart"`) {
		t.Error("the bar does not follow the main add button")
	}
	if !strings.Contains(bar, "NT$") {
		t.Errorf("the bar does not show the price: %s", bar)
	}
	if en := renderProduct(t, &view, i18n.En); !strings.Contains(en, `form="pdp-add"`) ||
		!strings.Contains(en, "<span>"+i18n.T(i18n.WithLocale(t.Context(), i18n.En), i18n.KeyAddToCart)+"</span>") {
		t.Error("the English bar does not offer the add button")
	}
}

func TestTheBottomBarOffersRestockInsteadOfBuyingWhenSoldOut(t *testing.T) {
	t.Parallel()
	view := ProductView{Slug: "book", Name: "Book", SelectionOK: true, Exact: true, VariantID: "v1"}
	bar := buyBarMarkup(t, &view)
	if strings.Contains(bar, `type="submit"`) {
		t.Errorf("a sold-out product's bar offers to buy: %s", bar)
	}
	if !strings.Contains(bar, `href="#restock"`) || !strings.Contains(bar, `data-follows="restock"`) {
		t.Errorf("the bar does not offer the restock action: %s", bar)
	}
	if !strings.Contains(renderProduct(t, &view, i18n.ZhHant), `id="restock"`) {
		t.Error("the restock link points at nothing")
	}
}

func TestTheBottomBarIsEmptyUntilAnOptionIsChosen(t *testing.T) {
	t.Parallel()
	view := ProductView{
		Slug: "book", Name: "Book", SelectionOK: true, AnySellable: true, VariantID: "v1",
		Options: []ProductOption{{Name: "顏色", Label: "顏色", Values: []ProductOptionValue{{Value: "黑", Label: "黑", Available: true}}}},
	}
	if bar := buyBarMarkup(t, &view); strings.Contains(bar, "<button") || strings.Contains(bar, "<a ") {
		t.Errorf("the bar offers an action before there is a variant: %s", bar)
	}
}

func TestTheBottomBarSaysWhatAnAddDidWhereTheNoticeIsOutOfSight(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ProductView{
		Slug: "book", Name: "Book", SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
		VariantID: "v1", Available: 5, AddedOutcome: "added",
	}
	bar := buyBarMarkup(t, &view)
	if !strings.Contains(bar, i18n.T(ctx, i18n.KeyAddedToCart)) || !strings.Contains(bar, `href="/cart"`) {
		t.Errorf("the bar does not confirm the add: %s", bar)
	}
}

func TestEverySwapOfTheBuyBoxReplacesTheBottomBarToo(t *testing.T) {
	t.Parallel()
	view := ProductView{
		Slug: "book", Name: "Book", SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
		VariantID: "v1", Available: 5,
	}
	page := renderProduct(t, &view, i18n.ZhHant)
	if !strings.Contains(page, `hx-select-oob="#cart-link,#buybar"`) {
		t.Error("an add leaves the bar showing the page it replaced")
	}
}

func TestTheBottomBarIsDrivenByAnIntersectionObserver(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "assets", "js", "goen.js"))
	if err != nil {
		t.Fatal(err)
	}
	js := string(src)
	if !strings.Contains(js, "function buyBar()") || !strings.Contains(js, "new IntersectionObserver") {
		t.Error("goen.js does not observe the add button's visibility")
	}
	if strings.Contains(js, `addEventListener("scroll"`) {
		t.Error("goen.js listens to scroll")
	}
}
