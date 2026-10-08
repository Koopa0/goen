package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"golang.org/x/net/html"
)

func TestRefusedEmailFormsKeepTheChineseFieldName(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tc := range []struct {
		name string
		key  i18n.Key
		want string
	}{
		{"required", i18n.KeyEmailRequired, "請填寫電子郵件"},
		{"malformed", i18n.KeyEmailMalformed, "電子郵件格式看起來不正確"},
	} {
		t.Run("checkout/"+tc.name, func(t *testing.T) {
			view := couponTestView()
			view.Address.Email = "invalid"
			view.Errors = map[string]string{"email": i18n.T(ctx, tc.key)}
			markup := renderComponent(t, ctx, Checkout(CheckoutMeta(ctx), &view))
			assertChineseEmailRefusal(t, markup, "email", tc.want)
		})
	}
	t.Run("restock", func(t *testing.T) {
		view := ProductView{Slug: "sold-out", VariantID: "v1", SelectionOK: true, Exact: true, NotifyOutcome: NotifyBadAddress, NotifyEmail: "invalid"}
		markup := renderComponent(t, ctx, productNotifyForm(&view))
		assertChineseEmailRefusal(t, markup, "notify-email", "請填寫正確的電子郵件。")
	})
}

func assertChineseEmailRefusal(t *testing.T, markup, inputID, sentence string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	input := findDescendant(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "input" && attrValue(n, "id") == inputID
	})
	if input == nil || attrValue(input, "aria-invalid") != "true" {
		t.Fatal("refused email field is not marked invalid")
	}
	errorID := attrValue(input, "aria-describedby")
	attached := false
	for _, id := range strings.Fields(errorID) {
		target := findDescendant(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && attrValue(n, "id") == id })
		if target == nil {
			continue
		}
		var text strings.Builder
		for n := range target.Descendants() {
			if n.Type == html.TextNode {
				text.WriteString(n.Data)
			}
		}
		if strings.TrimSpace(text.String()) == sentence {
			attached = true
		}
	}
	if !attached {
		t.Errorf("email refusal does not attach %q to its field", sentence)
	}
}
