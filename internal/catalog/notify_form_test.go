package catalog

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

// TestProductNotifyForm_EmailRefusal wants of the restock form what
// TestContact_EmailRefusal wants of the contact form.
func TestProductNotifyForm_EmailRefusal(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ProductView{Slug: "sold-out", VariantID: "v1", SelectionOK: true, Exact: true, NotifyOutcome: NotifyBadAddress, NotifyEmail: "invalid"}
	markup := renderComponent(t, ctx, productNotifyForm(&view))
	assertEmailRefusal(t, markup, "notify-email", "請填寫正確的電子郵件。")
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
