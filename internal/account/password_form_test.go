package account

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

// TestAccountPasswordFormNamesAccount: the change-password form carries the
// signed-in email as a username ahead of its password fields, so a password
// manager files the new password under that account instead of asking which
// login it belongs to. The field is hidden by attribute and not by type, which
// password managers skip, and it never takes focus.
func TestAccountPasswordFormNamesAccount(t *testing.T) {
	t.Parallel()
	const email = "shopper+tag@example.com"
	for _, tt := range []struct {
		name string
		view View
	}{
		{name: "password", view: View{Email: email, HasPassword: true}},
		{name: "google_linked", view: View{Email: email, HasPassword: true, GoogleLinked: true, CanUnlinkGoogle: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), i18n.En)
			body := renderComponent(t, ctx, Account(Meta(ctx), &tt.view))
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			form := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "form" && attrValue(n, "action") == "/account/password"
			})
			if form == nil {
				t.Fatal("account page has no change-password form")
			}
			var autocompletes []string
			var username *html.Node
			for n := range form.Descendants() {
				if n.Type != html.ElementNode || n.Data != "input" {
					continue
				}
				autocompletes = append(autocompletes, attrValue(n, "autocomplete"))
				if attrValue(n, "autocomplete") == "username" {
					username = n
				}
			}
			if username == nil {
				t.Fatalf("change-password form names no account; input autocomplete values = %q", autocompletes)
			}
			if got := autocompletes[0]; got != "username" {
				t.Errorf("first input in the change-password form has autocomplete %q, want username ahead of the password fields", got)
			}
			if got := attrValue(username, "value"); got != email {
				t.Errorf("username value = %q, want the signed-in email %q", got, email)
			}
			if !hasAttr(username, "hidden") {
				t.Error("username input is not hidden by attribute, so it shows on the page and takes focus")
			}
			if got := attrValue(username, "type"); got != "text" {
				t.Errorf("username input type = %q, want text; type=hidden is skipped by password managers", got)
			}
		})
	}
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}
