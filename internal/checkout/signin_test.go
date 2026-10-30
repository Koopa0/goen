package checkout

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

// TestPageGuestSignIn holds that a guest is asked at the head of the delivery details whether
// they already have an account, with a way to sign in that returns to the checkout, and that
// someone signed in is not asked.
func TestPageGuestSignIn(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			page := func(signedIn bool) *html.Node {
				view := couponTestView()
				view.SignedIn = signedIn
				doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, Page(Meta(ctx), &view))))
				if err != nil {
					t.Fatal(err)
				}
				return nodeByID(doc, "checkout-signin")
			}

			if page(true) != nil {
				t.Error("someone signed in is asked whether they have an account")
			}
			ask := page(false)
			if ask == nil {
				t.Fatal("a guest is not asked whether they have an account")
			}
			if got, want := nodeText(ask), i18n.T(ctx, i18n.KeyHaveAccount)+" "+i18n.T(ctx, i18n.KeySignIn); strings.Join(strings.Fields(got), " ") != want {
				t.Errorf("the guest is asked %q, want %q", got, want)
			}
			link := findDescendant(ask, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "a" })
			if link == nil || attrValue(link, "href") != "/signin?next=/checkout" || nodeText(link) != i18n.T(ctx, i18n.KeySignIn) {
				t.Errorf("the guest's way in is %v, want a link to /signin?next=/checkout reading %q", link, i18n.T(ctx, i18n.KeySignIn))
			}
		})
	}
}
