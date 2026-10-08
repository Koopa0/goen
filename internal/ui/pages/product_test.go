package pages

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestSoldOutGuidanceMatchesAvailableOptionPickers(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, tc := range []struct {
			name     string
			selected url.Values
		}{
			{"no options", url.Values{}},
			{"one option", url.Values{"colour": {"blue"}}},
			{"multiple encoded options", url.Values{"colour": {"blue & white"}, "size": {"XL/2"}}},
		} {
			t.Run(string(locale)+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				view := soldOutProduct(tc.selected)
				var body strings.Builder
				if err := Product(ProductMeta(&view), &view).Render(ctx, &body); err != nil {
					t.Fatal(err)
				}
				markup := body.String()
				if !strings.Contains(markup, i18n.T(ctx, i18n.KeySoldOut)) {
					t.Error("sold-out guidance is missing")
				}
				doc, err := html.Parse(strings.NewReader(markup))
				if err != nil {
					t.Fatal(err)
				}
				if err := restockFormProblem(doc, tc.selected, i18n.T(ctx, i18n.KeyRestockSubmit)); err != nil {
					t.Error(err)
				}
			})
		}
	}
}

func soldOutProduct(selected url.Values) ProductView {
	view := ProductView{Slug: "sold-out", Name: "Sold out product", VariantID: "only-variant", SelectionOK: true, Exact: true}
	for _, name := range slices.Sorted(maps.Keys(selected)) {
		view.Options = append(view.Options, ProductOption{Name: name, Label: name, Values: []ProductOptionValue{{Value: selected.Get(name), Label: selected.Get(name), Selected: true}}})
	}
	return view
}

func restockFormProblem(doc *html.Node, selected url.Values, label string) error {
	form := findDescendant(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "form" && attrValue(n, "id") == "restock"
	})
	if form == nil {
		return errors.New("restock form is missing")
	}
	if !strings.EqualFold(attrValue(form, "method"), "post") {
		return fmt.Errorf("restock form method = %q, want POST", attrValue(form, "method"))
	}
	target, err := url.Parse(attrValue(form, "action"))
	if err != nil {
		return fmt.Errorf("parse restock action: %w", err)
	}
	if target.Scheme != "" || target.Host != "" || target.Path != "/p/sold-out/notify" || target.Fragment != "" {
		return fmt.Errorf("restock action = %q, want the product's local notify path", target)
	}
	query, err := url.ParseQuery(target.RawQuery)
	if err != nil {
		return fmt.Errorf("parse selected options: %w", err)
	}
	if !reflect.DeepEqual(query, selected) {
		return fmt.Errorf("restock options = %v, want %v", query, selected)
	}
	owned := func(n *html.Node) bool {
		if owner := attrValue(n, "form"); owner != "" && owner != "restock" {
			return false
		}
		for _, attr := range n.Attr {
			if attr.Key == "disabled" {
				return false
			}
		}
		return true
	}
	variant := findDescendant(form, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "input" && attrValue(n, "name") == "variant" && attrValue(n, "type") == "hidden" && attrValue(n, "value") == "only-variant" && owned(n)
	})
	if variant == nil {
		return errors.New("restock form does not own the selected variant")
	}
	email := findDescendant(form, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "input" && attrValue(n, "name") == "email" && attrValue(n, "type") == "email" && owned(n)
	})
	if email == nil {
		return errors.New("restock form does not own an email control")
	}
	button := findDescendant(form, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != "button" || attrValue(n, "type") != "submit" || !owned(n) {
			return false
		}
		return findDescendant(n, func(child *html.Node) bool {
			return child.Type == html.TextNode && strings.TrimSpace(child.Data) == label
		}) != nil
	})
	if button == nil {
		return errors.New("restock form lacks its submit affordance")
	}
	return nil
}

func TestTheRestockOracleAcceptsEquivalentMarkupAndRejectsChangedBehavior(t *testing.T) {
	t.Parallel()
	selected := url.Values{"colour": {"blue & white"}, "size": {"XL/2"}}
	for _, tc := range []struct {
		name    string
		change  func(*html.Node, *html.Node)
		refused bool
	}{
		{"attribute and query order", func(_ *html.Node, form *html.Node) {
			slices.Reverse(form.Attr)
			setRestockAttribute(form, "action", "/p/sold-out/notify?size=XL%2F2&colour=blue%20%26%20white")
			setRestockAttribute(form, "method", "POST")
		}, false},
		{"wrong method", func(_ *html.Node, form *html.Node) { setRestockAttribute(form, "method", "get") }, true},
		{"wrong path", func(_ *html.Node, form *html.Node) {
			setRestockAttribute(form, "action", "/p/other/notify?"+selected.Encode())
		}, true},
		{"missing option", func(_ *html.Node, form *html.Node) {
			setRestockAttribute(form, "action", "/p/sold-out/notify?colour=blue+%26+white")
		}, true},
		{"changed option", func(_ *html.Node, form *html.Node) {
			setRestockAttribute(form, "action", "/p/sold-out/notify?size=XL%2F3&colour=blue+%26+white")
		}, true},
		{"variant outside form", func(doc *html.Node, form *html.Node) {
			input := findDescendant(form, func(n *html.Node) bool { return n.Type == html.ElementNode && attrValue(n, "name") == "variant" })
			input.Parent.RemoveChild(input)
			body := findDescendant(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "body" })
			body.AppendChild(input)
		}, true},
		{"email owned elsewhere", func(_ *html.Node, form *html.Node) {
			input := findDescendant(form, func(n *html.Node) bool { return n.Type == html.ElementNode && attrValue(n, "name") == "email" })
			setRestockAttribute(input, "form", "other")
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			view := soldOutProduct(selected)
			ctx := i18n.WithLocale(t.Context(), i18n.En)
			var body strings.Builder
			if err := Product(ProductMeta(&view), &view).Render(ctx, &body); err != nil {
				t.Fatal(err)
			}
			doc, err := html.Parse(strings.NewReader(body.String()))
			if err != nil {
				t.Fatal(err)
			}
			form := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "form" && attrValue(n, "id") == "restock"
			})
			if form == nil {
				t.Fatal("rendered product has no restock form")
			}
			tc.change(doc, form)
			err = restockFormProblem(doc, selected, i18n.T(ctx, i18n.KeyRestockSubmit))
			if (err != nil) != tc.refused {
				t.Errorf("oracle refusal = %v, want refused=%t", err, tc.refused)
			}
		})
	}
}

func setRestockAttribute(n *html.Node, key, value string) {
	for i, attr := range n.Attr {
		if attr.Key == key {
			n.Attr[i].Val = value
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: value})
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
					if wishlistSignInDestination(t, markup, i18n.T(ctx, i18n.KeyWishlistSignIn)) != "/signin?next=/p/sample-product%23wishlist" {
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
	options := []ProductOption{{Name: "顏色", Label: "顏色", Values: []ProductOptionValue{{Value: "黑", Label: "黑"}}}}
	for name, view := range map[string]ProductView{
		"every option gone":    {Slug: "book", Name: "Book", SelectionOK: true, VariantID: "v1", Options: options},
		"one combination gone": {Slug: "book", Name: "Book", SelectionOK: true, VariantID: "v1", Exact: true, AnySellable: true, Options: options},
	} {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			ctx := i18n.WithLocale(t.Context(), locale)
			want := i18n.T(ctx, i18n.KeySoldOut)
			if got := strings.Count(renderProduct(t, &view, locale), want); got != 1 {
				t.Errorf("%s (%s): %q appears %d times, want once", name, locale, want, got)
			}
		}
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
	if !strings.Contains(got, `顏色：<span class="goen-pdp__optchosen">曜石黑`) {
		t.Error("the chosen option's name does not follow its label with the full-width colon and nothing between")
	}
	if en := renderProduct(t, &view, i18n.En); !strings.Contains(en, `顏色: <span class="goen-pdp__optchosen">曜石黑`) {
		t.Error("the English separator is not a colon and a space before the chosen name")
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

func TestAHalfStarIsShownAsAHalf(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		rating  float64
		halves  int
		full    int
		hasHalf bool
	}{
		{0, 0, 0, false},
		{4, 8, 4, false},
		{4.5, 9, 4, true},
		{4.7, 9, 4, true},
		{4.8, 10, 5, false},
		{5, 10, 5, false},
		{7, 10, 5, false},
	} {
		v := ProductView{Rating: tc.rating}
		if got := v.StarHalves(); got != tc.halves {
			t.Errorf("rating %v: StarHalves() = %d, want %d", tc.rating, got, tc.halves)
		}
		drawn := renderToString(t, ratingStars(tc.halves))
		if got := strings.Count(drawn, `class="goen-pdp__rate--on"`); got != tc.full {
			t.Errorf("rating %v: %d full stars, want %d", tc.rating, got, tc.full)
		}
		if got := strings.Count(drawn, `class="goen-pdp__rate--half"`) == 1; got != tc.hasHalf {
			t.Errorf("rating %v: half star shown = %v, want %v", tc.rating, got, tc.hasHalf)
		}
	}
}

func TestASingleSimilarProductDoesNotMakeARow(t *testing.T) {
	t.Parallel()
	tile := ProductTile{Slug: "a", Name: "A", PriceCents: 100}
	one := ProductView{Slug: "x", Name: "X", SelectionOK: true, Related: []ProductTile{tile}}
	if strings.Contains(renderProduct(t, &one, i18n.En), i18n.T(i18n.WithLocale(t.Context(), i18n.En), i18n.KeySectionRelated)) {
		t.Error("a similar-products row with one card is rendered")
	}
	two := ProductView{Slug: "x", Name: "X", SelectionOK: true, Related: []ProductTile{tile, tile}}
	if !strings.Contains(renderProduct(t, &two, i18n.En), i18n.T(i18n.WithLocale(t.Context(), i18n.En), i18n.KeySectionRelated)) {
		t.Error("a similar-products row with two cards is not rendered")
	}
}

func TestChoosingOptionsIsSaidOnce(t *testing.T) {
	t.Parallel()
	view := ProductView{
		Slug: "tee", Name: "Tee", SelectionOK: true, AnySellable: true,
		Options: []ProductOption{{Name: "color", Label: "Color", Values: []ProductOptionValue{{Value: "b", Label: "Black"}, {Value: "w", Label: "White"}}}},
	}
	got := renderProduct(t, &view, i18n.En)
	if n := strings.Count(strings.ToLower(got), "choose"); n != 1 {
		t.Errorf("the page asks the visitor to choose %d times, want once", n)
	}
}
