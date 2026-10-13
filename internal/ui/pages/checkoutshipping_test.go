package pages

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestCheckoutWithoutDeliveryKeepsTypedDraftAndBlocksPlacement(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, available := range []bool{false, true} {
			name := "unavailable"
			if available {
				name = "available"
			}
			t.Run(locale.Tag()+"/"+name, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				view := &CheckoutView{
					Address:    CheckoutAddress{Email: "draft@example.com", Name: "Draft Recipient"},
					CouponCode: "DRAFT", IdempotencyKey: "attempt-to-retain",
				}
				if available {
					view.Shipping = []ShippingChoice{{VersionID: "00000000-0000-0000-0000-000000000001", Name: "Home delivery", FeeCents: 10000}}
					view.Chosen = view.Shipping[0].VersionID
				}
				body := renderComponent(t, ctx, Checkout(CheckoutMeta(ctx), view))
				got := checkoutDeliveryState(t, body, i18n.T(ctx, view.PlaceOrderKey()))
				want := checkoutDeliveryFacts{
					PlaceButtons: 2, Email: "draft@example.com", Name: "Draft Recipient",
					Coupon: "DRAFT", Attempt: "attempt-to-retain",
				}
				if available {
					want.ShippingGroups, want.ShippingOptions = 1, 1
				} else {
					want.DisabledButtons, want.UnavailableMessages, want.UnavailableSummaries = 2, 1, 1
					want.Message = "No delivery method is available for this cart. Change the items or contact us."
					if locale == i18n.ZhHant {
						want.Message = "購物車中的商品目前沒有可用的配送方式。請調整商品，或聯絡我們。"
					}
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("checkout delivery state (-want +got):\n%s", diff)
				}
			})
		}
	}
}

type checkoutDeliveryFacts struct {
	PlaceButtons, DisabledButtons, ShippingGroups, ShippingOptions int
	UnavailableMessages, UnavailableSummaries                      int
	Message, Email, Name, Coupon, Attempt                          string
}

func checkoutDeliveryState(t *testing.T, body, placeLabel string) checkoutDeliveryFacts {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	attr := func(n *html.Node, key string) string {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
		return ""
	}
	text := func(n *html.Node) string {
		var b strings.Builder
		for child := range n.Descendants() {
			if child.Type == html.TextNode {
				b.WriteString(child.Data)
			}
		}
		return strings.TrimSpace(b.String())
	}
	var form *html.Node
	for n := range doc.Descendants() {
		if n.Type == html.ElementNode && n.Data == "form" && attr(n, "id") == "checkout-form" {
			form = n
			break
		}
	}
	if form == nil {
		t.Fatal("checkout response lost its form")
	}
	var got checkoutDeliveryFacts
	for n := range form.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		switch {
		case n.Data == "button" && text(n) == placeLabel:
			got.PlaceButtons++
			for _, a := range n.Attr {
				if a.Key == "disabled" {
					got.DisabledButtons++
				}
			}
		case attr(n, "role") == "radiogroup" && attr(n, "aria-labelledby") == "shipping-heading":
			got.ShippingGroups++
		case n.Data == "input" && attr(n, "name") == "shipping":
			got.ShippingOptions++
		case attr(n, "id") == "shipping-unavailable":
			got.UnavailableMessages++
			got.Message = text(n)
		case n.Data == "dd" && (text(n) == "Unavailable" || text(n) == "無可用方式"):
			got.UnavailableSummaries++
		case n.Data == "input":
			switch attr(n, "name") {
			case "email":
				got.Email = attr(n, "value")
			case "name":
				got.Name = attr(n, "value")
			case "coupon":
				got.Coupon = attr(n, "value")
			case "idempotency":
				got.Attempt = attr(n, "value")
			}
		}
	}
	return got
}
