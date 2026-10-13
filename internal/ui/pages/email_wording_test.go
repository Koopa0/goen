package pages

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestRefusedEmailFormsAttachCompleteFieldMessages(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tc := range []struct {
		name   string
		locale i18n.Locale
		key    i18n.Key
		want   string
	}{
		{name: "required Chinese", locale: i18n.ZhHant, key: i18n.KeyEmailRequired, want: "請填寫電子郵件。"},
		{name: "malformed Chinese", locale: i18n.ZhHant, key: i18n.KeyEmailMalformed, want: "電子郵件格式看起來不正確。"},
		{name: "required English", locale: i18n.En, key: i18n.KeyEmailRequired, want: "Enter an email address."},
		{name: "malformed English", locale: i18n.En, key: i18n.KeyEmailMalformed, want: "That does not look like an email address."},
	} {
		t.Run("contact/"+tc.name, func(t *testing.T) {
			t.Parallel()
			localized := i18n.WithLocale(t.Context(), tc.locale)
			view := ContactForm{Email: "invalid", Errors: map[string]string{"email": i18n.T(localized, tc.key)}}
			markup := renderComponent(t, localized, Contact(ContactMeta(localized), view))
			assertEmailRefusal(t, markup, "contact-email", tc.want)
		})
	}
	t.Run("restock", func(t *testing.T) {
		view := ProductView{Slug: "sold-out", VariantID: "v1", SelectionOK: true, Exact: true, NotifyOutcome: NotifyBadAddress, NotifyEmail: "invalid"}
		markup := renderComponent(t, ctx, productNotifyForm(&view))
		assertEmailRefusal(t, markup, "notify-email", "請填寫正確的電子郵件。")
	})
}

func assertEmailRefusal(t *testing.T, markup, inputID, sentence string) {
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
