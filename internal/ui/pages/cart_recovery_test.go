package pages

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestCartRecoveryListsBothCartsBeforeTheNativeRetry(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			before := CartLine{Name: "Guest item", Label: "Red / Small", Quantity: 2, ImageURL: "/guest.webp", ImageAlt: "Guest photo"}
			stored := CartLine{Name: "Account item", Label: "Blue / Large", Quantity: 3, ImageURL: "/account.webp", ImageAlt: "Account photo"}
			body := renderComponent(t, ctx, CartRecovery(CartRecoveryMeta(ctx), CartRecoveryView{
				Next: "/checkout", Notice: i18n.T(ctx, i18n.KeyCartMergeFailed), Retry: i18n.T(ctx, i18n.KeyCartMergeRetry),
				Contents: CartRecoveryContents{BeforeSignIn: []CartLine{before}, Account: []CartLine{stored}},
			}))
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			for id, line := range map[string]CartLine{"cart-recovery-before": before, "cart-recovery-account": stored} {
				section := findDescendant(doc, func(n *html.Node) bool {
					return n.Type == html.ElementNode && n.Data == "section" && attrValue(n, "aria-labelledby") == id
				})
				if section == nil {
					t.Errorf("recovery has no labelled %s list", id)
					continue
				}
				list := findDescendant(section, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "ul" })
				if list == nil || findDescendant(list, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "li" }) == nil {
					t.Errorf("%s does not contain a semantic product list", id)
				}
				image := findDescendant(section, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "img" })
				if image == nil || attrValue(image, "src") != line.ImageURL || attrValue(image, "alt") != line.ImageAlt {
					t.Errorf("%s does not show its product photo", id)
				}
				text := nodeText(section)
				for _, want := range []string{line.Name, line.Label, i18n.T(ctx, i18n.KeyQuantity) + " " + line.QuantityText()} {
					if !strings.Contains(text, want) {
						t.Errorf("%s omits %q", id, want)
					}
				}
			}
			form := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "form" && attrValue(n, "action") == "/account/cart/retry"
			})
			if form == nil || attrValue(form, "method") != "post" {
				t.Fatal("recovery does not use the existing native retry POST")
			}
			continuation := findDescendant(form, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "input" && attrValue(n, "name") == "next"
			})
			if continuation == nil || attrValue(continuation, "value") != "/checkout" {
				t.Error("retry loses the original destination")
			}
			back := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "a" && attrValue(n, "href") == "/cart" && strings.Contains(attrValue(n, "class"), "notice__back")
			})
			if back == nil || !strings.Contains(attrValue(back, "class"), "goen-btn--outline") {
				t.Error("secondary cart link has no visible button boundary")
			}
			wording := []string{"Your carts weren't combined", "The items you added before signing in haven't been added to your account's cart yet.", "Add these items", "Not now, back to cart"}
			if locale == i18n.ZhHant {
				wording = []string{
					"\u8cfc\u7269\u8eca\u6c92\u6709\u5408\u5728\u4e00\u8d77",
					"\u4f60\u767b\u5165\u524d\u653e\u9032\u8cfc\u7269\u8eca\u7684\u5546\u54c1\uff0c\u9084\u6c92\u6709\u52a0\u5230\u5e33\u865f\u7684\u8cfc\u7269\u8eca\u88e1\u3002",
					"\u628a\u9019\u4e9b\u5546\u54c1\u52a0\u9032\u53bb",
					"\u5148\u4e0d\u7528\uff0c\u56de\u5230\u8cfc\u7269\u8eca",
				}
			}
			heading := findDescendant(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "h1" })
			if heading == nil || nodeText(heading) != wording[0] {
				t.Error("recovery heading does not say the carts were not combined")
			}
			submit := findDescendant(form, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "button" && attrValue(n, "type") == "submit"
			})
			if submit == nil || nodeText(submit) != wording[2] {
				t.Error("native retry submit does not name the action")
			}
			if back != nil && nodeText(back) != wording[3] {
				t.Error("secondary link does not say it returns to the cart")
			}
			for _, want := range wording {
				if !strings.Contains(nodeText(doc), want) {
					t.Errorf("recovery omits wording %q", want)
				}
			}
			if strings.Contains(body, "\u5e33\u6236") {
				t.Error("recovery uses the retired account term")
			}
		})
	}
}
