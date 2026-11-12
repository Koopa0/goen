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
// offers the home page as its way on, without a picture and without a solid
// button: its ways on are links. The other pages' rows of this test are
// catalog's TestListingEmptyStatesHaveNoPicture.
func TestPageEmptyHasNoPicture(t *testing.T) {
	t.Parallel()
	page := renderIn(t, i18n.ZhHant, Page(frame.Page{}, View{SavedProducts: true}))
	_, rest, ok := strings.Cut(page, `class="goen-empty">`)
	if !ok {
		t.Fatal("empty cart renders no goen-empty box")
	}
	box, _, _ := strings.Cut(rest, `</div>`)
	for _, unwanted := range []string{`<img`, `goen-medallion`, `goen-btn`, `<button`} {
		if strings.Contains(box, unwanted) {
			t.Errorf("empty cart draws %q", unwanted)
		}
	}
	if want := `<a class="goen-empty__next" href="/"`; !strings.Contains(box, want) {
		t.Errorf("empty cart omits %s", want)
	}
}

// TestPageEmptyOffersWishlist holds that the empty cart says 去逛逛 / Continue
// shopping as the section's next step, the one blue link with its arrow, and
// offers the wishlist, an ink secondary link, only to a customer who has saved
// products. The arrow is drawn, not read.
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
			type found struct{ name, class string }
			link := func(page templ.Component, href string) found {
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
						return found{name: strings.TrimSpace(spokenText(a)), class: attr(a, "class")}
					}
				}
				return found{}
			}
			next := found{name: tt.shopping, class: "goen-empty__next"}
			without := Page(frame.Page{}, View{})
			if got := link(without, "/"); got != next {
				t.Errorf("the empty cart's way on is %+v, want %+v", got, next)
			}
			if got := link(without, "/account/wishlist"); got != (found{}) {
				t.Errorf("an empty cart without saved products links the wishlist as %+v", got)
			}
			with := Page(frame.Page{}, View{SavedProducts: true})
			if got, want := link(with, "/account/wishlist"), (found{name: tt.wishlist, class: "goen-empty__more"}); got != want {
				t.Errorf("the empty cart's wishlist link is %+v, want %+v", got, want)
			}
			if got := link(with, "/"); got != next {
				t.Errorf("with a wishlist the way on is %+v, want %+v", got, next)
			}
		})
	}
}

// spokenText is n's text without what is hidden from a screen reader.
func spokenText(n *nethtml.Node) string {
	if n.Type == nethtml.TextNode {
		return n.Data
	}
	if n.Type == nethtml.ElementNode && attr(n, "aria-hidden") == "true" {
		return ""
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(spokenText(c))
	}
	return b.String()
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
