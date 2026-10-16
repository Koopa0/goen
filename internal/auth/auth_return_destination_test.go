package auth

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestAuthLinksKeepTheReturnDestination(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name  string
				next  string
				query string
			}{
				{name: "empty"},
				{name: "account", next: "/account"},
				{name: "wishlist", next: "/account/wishlist", query: "?next=%2Faccount%2Fwishlist"},
				{name: "query and fragment", next: "/checkout?ship=pickup&coupon=SAVE+10#payment", query: "?next=%2Fcheckout%3Fship%3Dpickup%26coupon%3DSAVE%2B10%23payment"},
				{name: "unicode", next: "/search?q=\u6587\u5177", query: "?next=%2Fsearch%3Fq%3D%E6%96%87%E5%85%B7"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					ctx := i18n.WithLocale(t.Context(), locale)
					view := AuthView{Next: tt.next}
					for _, page := range []struct {
						name string
						body string
						key  i18n.Key
						path string
					}{
						{name: "sign in", body: renderComponent(t, ctx, SignIn(layouts.Page{}, view)), key: i18n.KeyNoAccountLink, path: "/register"},
						{name: "register", body: renderComponent(t, ctx, Register(layouts.Page{}, view)), key: i18n.KeySignIn, path: "/signin"},
						{name: "other address", body: renderComponent(t, ctx, Register(layouts.Page{}, AuthView{
							Next: tt.next, OffersResend: true, Notice: i18n.T(ctx, i18n.KeyRegisterSent),
						})), key: i18n.KeyRegisterOtherAddress, path: "/register"},
					} {
						t.Run(page.name, func(t *testing.T) {
							t.Parallel()
							doc, err := html.Parse(strings.NewReader(page.body))
							if err != nil {
								t.Fatal(err)
							}
							link := findDescendant(doc, func(n *html.Node) bool {
								return n.Type == html.ElementNode && n.Data == "a" && hasClass(n.Parent, "goen-auth__alt") && nodeText(n) == i18n.T(ctx, page.key)
							})
							if link == nil {
								t.Fatalf("authentication page lacks its %q link", i18n.T(ctx, page.key))
							}
							if got, want := attrValue(link, "href"), page.path+tt.query; got != want {
								t.Errorf("authentication return link = %q, want %q", got, want)
							}
						})
					}
				})
			}
		})
	}
}
