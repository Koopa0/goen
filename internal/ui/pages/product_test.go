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
