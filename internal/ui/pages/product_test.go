package pages

import (
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
				for _, want := range []string{
					i18n.T(ctx, i18n.KeyAllSoldOut),
					`method="post" action="/p/sold-out/notify"`,
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
		want := ""
		if tagged {
			name, want = "tagged", "#gallery"
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
