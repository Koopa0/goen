package cart

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
	nethtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/user"
)

// TestPageEmptyHasNoPicture holds that an empty cart says so in words and
// offers the home page as its way on, without a picture. The other pages' rows
// of this test are catalog's TestListingEmptyStatesHaveNoPicture.
func TestPageEmptyHasNoPicture(t *testing.T) {
	t.Parallel()
	page := renderIn(t, i18n.ZhHant, Page(frame.Page{}, View{}))
	_, rest, ok := strings.Cut(page, `class="goen-empty">`)
	if !ok {
		t.Fatal("empty cart renders no goen-empty box")
	}
	box, _, _ := strings.Cut(rest, `</div>`)
	for _, picture := range []string{`<img`, `goen-medallion`} {
		if strings.Contains(box, picture) {
			t.Errorf("empty cart draws %q", picture)
		}
	}
	if want := `<a class="goen-btn goen-btn--primary goen-btn--lg" href="/"`; !strings.Contains(box, want) {
		t.Errorf("empty cart omits %s", want)
	}
}

// TestPageEmptyOffersWishlist holds that the empty cart says 去逛逛 / Continue
// shopping, and offers the wishlist only to a customer who has saved products.
func TestPageEmptyOffersWishlist(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale   i18n.Locale
		shopping string
		wishlist string
	}{
		{i18n.ZhHant, "去逛逛", "看看願望清單"},
		{i18n.En, "Continue shopping", "See your wishlist"},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			// The header links the home page and the wishlist too; only the box counts.
			link := func(page templ.Component, href string) string {
				doc, err := nethtml.Parse(strings.NewReader(renderIn(t, tt.locale, page)))
				if err != nil {
					t.Fatal(err)
				}
				box := elementByClass(doc, "div", "goen-empty")
				if box == nil {
					t.Fatal("the empty cart renders no goen-empty box")
				}
				for a := range box.Descendants() {
					if a.Type == nethtml.ElementNode && a.Data == "a" && attr(a, "href") == href {
						return strings.TrimSpace(text(a))
					}
				}
				return ""
			}
			without := Page(frame.Page{}, View{})
			if got := link(without, "/"); got != tt.shopping {
				t.Errorf("the empty cart's way on reads %q, want %q", got, tt.shopping)
			}
			if got := link(without, "/account/wishlist"); got != "" {
				t.Errorf("an empty cart without saved products links the wishlist as %q", got)
			}
			with := Page(frame.Page{}, View{SavedProducts: true})
			if got := link(with, "/account/wishlist"); got != tt.wishlist {
				t.Errorf("the empty cart's wishlist link reads %q, want %q", got, tt.wishlist)
			}
			if got := link(with, "/"); got != tt.shopping {
				t.Errorf("with a wishlist the way on reads %q, want %q", got, tt.shopping)
			}
		})
	}
}

// TestPageCheckoutEmptyNotice holds that a cart /checkout turned away for holding
// nothing says so and says checkout cannot continue, then offers the way to an order
// already placed: the account's orders to a signed-in reader, the order lookup to a
// guest. A cart that was not turned away says none of it.
func TestPageCheckoutEmptyNotice(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		locale     i18n.Locale
		signedIn   bool
		turnedAway bool
		sentence   string
		href       string
		label      string
	}{
		"guest": {
			locale: i18n.ZhHant, turnedAway: true,
			sentence: "購物車是空的，結帳無法繼續。", href: "/orders/find", label: "查詢訂單",
		},
		"member": {
			locale: i18n.ZhHant, signedIn: true, turnedAway: true,
			sentence: "購物車是空的，結帳無法繼續。", href: "/account#orders-heading", label: "我的訂單",
		},
		"guest in english": {
			locale: i18n.En, turnedAway: true,
			sentence: "Your cart is empty, so checkout cannot continue.", href: "/orders/find", label: "Find your order",
		},
		"not turned away": {locale: i18n.ZhHant},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			if tt.signedIn {
				ctx = user.NewContext(ctx, user.User{ID: "018f0000-0000-7000-8000-000000000001"})
			}
			doc, err := nethtml.Parse(strings.NewReader(renderComponent(t, ctx, Page(frame.Page{}, View{CheckoutEmpty: tt.turnedAway}))))
			if err != nil {
				t.Fatal(err)
			}
			var notice *nethtml.Node
			for n := range doc.Descendants() {
				if n.Type == nethtml.ElementNode && attr(n, "id") == "cart-checkout-empty" {
					notice = n
				}
			}
			if notice == nil {
				if tt.turnedAway {
					t.Fatal("the cart says nothing of why checkout cannot continue")
				}
				return
			}
			if !tt.turnedAway {
				t.Fatal("a cart that was not turned away says checkout cannot continue")
			}
			if got := strings.TrimSpace(text(notice)); !strings.HasPrefix(got, tt.sentence) {
				t.Errorf("the notice reads %q, want it to begin %q", got, tt.sentence)
			}
			var links []string
			for a := range notice.Descendants() {
				if a.Type == nethtml.ElementNode && a.Data == "a" {
					links = append(links, attr(a, "href")+" "+strings.TrimSpace(text(a)))
				}
			}
			if want := tt.href + " " + tt.label; len(links) != 1 || links[0] != want {
				t.Errorf("the notice links %q, want one link %q", links, want)
			}
		})
	}
}
