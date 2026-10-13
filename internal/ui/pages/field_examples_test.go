package pages

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestCustomerExamplesStayOutsideRefusedInputs(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, draft := range []struct{ value, refusal string }{{"", ""}, {"Submitted <& value", ""}, {"", "Refused"}, {"Submitted <& value", "Refused"}} {
			typed, refusal := draft.value, draft.refusal
			t.Run(locale.Tag()+"/"+typed+"/"+refusal, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				errors := func(fields ...string) map[string]string {
					if refusal == "" {
						return nil
					}
					result := make(map[string]string, len(fields))
					for _, field := range fields {
						result[field] = refusal
					}
					return result
				}
				name, label := "Alex Chen", "Home, work"
				message, coupon := "Tell us what happened…", "Have a discount code?"
				prefix := "Example: "
				if locale == i18n.ZhHant {
					name, label, prefix = "王小明", "家、公司", "例："
					message, coupon = "請描述你遇到的狀況…", "有折扣碼嗎？"
				}
				for _, page := range []struct {
					name      string
					component templ.Component
					fields    map[string]string
				}{
					{"contact", ContactPanel(ContactForm{Name: typed, Email: typed, OrderRef: typed, Message: typed, Errors: errors("name", "email", "order_ref", "message")}), map[string]string{"contact-name": name, "contact-email": "you@example.com", "contact-order": "GO-260101-000001", "contact-message": message}},
					{"address", Account(layouts.Page{}, &AccountView{AddressDraft: &AddressDraft{Label: typed}, AddressErrors: errors("label")}), map[string]string{"addr-label": label}},
					{"find order", FindOrder(layouts.Page{}, FindOrderView{Number: typed, Error: refusal}), map[string]string{"find-number": "GO-260101-000001"}},
					{"coupon", Checkout(layouts.Page{}, &CheckoutView{CouponCode: typed, Errors: errors("coupon")}), map[string]string{"coupon": coupon}},
					{"mobile barcode", Checkout(layouts.Page{}, &CheckoutView{Invoice: CheckoutInvoice{Type: invoice.PreferenceMobile, MobileBarcode: typed}, Errors: errors("invoice_carrier")}), map[string]string{"invoice_carrier": "/ABC+123"}},
				} {
					t.Run(page.name, func(t *testing.T) {
						markup := renderComponent(t, ctx, page.component)
						z := html.NewTokenizer(strings.NewReader(markup))
						for z.Next() != html.ErrorToken {
							token := z.Token()
							if token.Data != "input" && token.Data != "textarea" {
								continue
							}
							var descriptions int
							for _, attribute := range token.Attr {
								if attribute.Key == "aria-describedby" {
									descriptions++
								}
							}
							if descriptions > 1 {
								t.Error("an input repeats aria-describedby")
							}
						}
						doc, err := html.Parse(strings.NewReader(markup))
						if err != nil {
							t.Fatal(err)
						}
						for id, example := range page.fields {
							input := findDescendant(doc, func(n *html.Node) bool {
								return n.Type == html.ElementNode && (n.Data == "input" || n.Data == "textarea") && attrValue(n, "id") == id
							})
							if input == nil {
								t.Fatalf("refused %s has no input %s", page.name, id)
							}
							value := attrValue(input, "value")
							if input.Data == "textarea" {
								value = nodeText(input)
							}
							if id != "contact-message" && id != "coupon" {
								example = prefix + example
							}
							if attrValue(input, "placeholder") != "" || value != typed {
								t.Errorf("%s placeholder/value = %q/%q, want empty/%q", id, attrValue(input, "placeholder"), value, typed)
							}
							var linkedExample bool
							for _, description := range strings.Fields(attrValue(input, "aria-describedby")) {
								hint := findDescendant(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && attrValue(n, "id") == description })
								if hint == nil {
									t.Errorf("%s description %s has no text", id, description)
									continue
								}
								var hidden bool
								for _, attribute := range hint.Attr {
									if attribute.Key == "hidden" {
										hidden = true
									}
								}
								if nodeText(hint) == example && !hidden && !strings.Contains(attrValue(hint, "class"), "sr-only") {
									linkedExample = true
								}
							}
							if !linkedExample {
								t.Errorf("refused %s has no visible linked example %q", id, example)
							}
						}
					})
				}
			})
		}
	}
}

func TestBackInStockEmailKeepsAVisibleLabelWhenRefused(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, productNotifyForm(&ProductView{NotifyEmail: "refused@example.com", NotifyOutcome: NotifyBadAddress}))))
			if err != nil {
				t.Fatal(err)
			}
			label := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "label" && attrValue(n, "for") == "notify-email"
			})
			if label == nil || strings.Contains(attrValue(label, "class"), "sr-only") || nodeText(label) != i18n.T(ctx, i18n.KeyFieldEmail) {
				t.Error("back-in-stock email does not have its visible label")
			}
			input := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "input" && attrValue(n, "id") == "notify-email"
			})
			if input == nil || attrValue(input, "placeholder") != "" || attrValue(input, "value") != "refused@example.com" || attrValue(input, "aria-invalid") != "true" {
				t.Error("refused back-in-stock email loses its value, refusal or empty placeholder")
			}
		})
	}
}
