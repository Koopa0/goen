package account

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

// TestAccountSignOutOtherDevices: the account page offers signing out other
// devices as a plain POST that asks for no password.
func TestAccountSignOutOtherDevices(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		label  string
	}{
		{locale: i18n.En, label: "Sign out other devices"},
		{locale: i18n.ZhHant, label: "\u767b\u51fa\u5176\u4ed6\u88dd\u7f6e"},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			body := renderComponent(t, ctx, Account(Meta(ctx), &View{
				Email: "shopper@example.com", GoogleLinked: true,
			}))
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			form := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "form" && attrValue(n, "action") == "/account/sessions/others/end"
			})
			if form == nil {
				t.Fatal("account page has no form to sign out other devices")
			}
			if got := attrValue(form, "method"); got != "post" {
				t.Errorf("other-device form method = %q, want post", got)
			}
			button := findDescendant(form, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "button" && attrValue(n, "type") == "submit"
			})
			if button == nil || nodeText(button) != tt.label {
				t.Errorf("other-device form lacks submit button %q", tt.label)
			}
			for n := range form.Descendants() {
				if n.Type == html.ElementNode && (n.Data == "input" || n.Data == "select" || n.Data == "textarea") {
					t.Errorf("other-device form asks for %q; the signed-in session supplies its authority", attrValue(n, "name"))
				}
			}
		})
	}
}
