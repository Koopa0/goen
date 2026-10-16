package pages

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/contact"
	"github.com/koopa0/goen/internal/i18n"
)

func TestAccountEmailReentry(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			body := renderComponent(t, ctx, Account(AccountMeta(ctx), &AccountView{Email: "old@example.com", EmailConfirmationRequested: true, EmailReentry: true}))
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			var disclosure *html.Node
			for n := range doc.Descendants() {
				if n.Data == "details" && sentAttribute(n, "id") == "account-email-form" {
					disclosure = n
				}
			}
			if disclosure == nil || !sentHasAttribute(disclosure, "open") {
				t.Error("Account() re-entry does not open the email-change form")
			}
			if !strings.Contains(body, `href="/account?email=reenter#account-email-form"`) {
				t.Error("Account() sent state has no re-entry link to its email form")
			}
		})
	}
}

func TestContactSuccessOffersOnlyTheSignedInReadersOwnOrders(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, signedIn := range []bool{false, true} {
			t.Run(locale.Tag()+"/"+map[bool]string{false: "guest", true: "member"}[signedIn], func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				body := renderComponent(t, ctx, contact.ContactPanel(contact.ContactForm{Done: true, SignedIn: signedIn}))
				if !strings.Contains(body, `href="/"`) {
					t.Error("ContactPanel() success has no shop next step")
				}
				if got := strings.Contains(body, `href="/account#orders-heading"`); got != signedIn {
					t.Errorf("ContactPanel() own-order link = %t, want %t", got, signedIn)
				}
				if strings.Contains(body, `method="post"`) {
					t.Error("ContactPanel() success still presents the submitted form")
				}
			})
		}
	}
}

func sentAttribute(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func sentHasAttribute(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}
