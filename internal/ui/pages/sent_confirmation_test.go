package pages

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestForgotSentCollapsesResendWithoutAutofocusingItsHiddenInput(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, sent := range []bool{false, true} {
			t.Run(locale.Tag()+"/"+map[bool]string{false: "fresh", true: "sent"}[sent], func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				body := renderComponent(t, ctx, Forgot(layouts.Page{}, ForgotView{Sent: sent, Address: "a***@example.com"}))
				doc, err := html.Parse(strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				var form, input, disclosure *html.Node
				for n := range doc.Descendants() {
					if n.Type != html.ElementNode {
						continue
					}
					if n.Data == "form" && sentAttribute(n, "action") == "/forgot" {
						if form != nil {
							t.Fatal("Forgot() renders more than one reset form")
						}
						form = n
					}
					if n.Data == "input" && sentAttribute(n, "id") == "email" {
						input = n
					}
					if n.Data == "details" && sentAttribute(n, "id") == "forgot-resend" {
						disclosure = n
					}
				}
				if form == nil || input == nil {
					t.Fatal("Forgot() lost the plain POST form or email input")
				}
				if !sent {
					if disclosure != nil || !sentHasAttribute(input, "autofocus") {
						t.Error("Forgot() fresh form is collapsed or lost its initial focus")
					}
					return
				}
				if disclosure == nil || form.Parent != disclosure || sentHasAttribute(disclosure, "open") {
					t.Error("Forgot() sent state does not keep its resend form in a closed native disclosure")
				}
				if sentHasAttribute(input, "autofocus") {
					t.Error("Forgot() autofocuses the closed resend form")
				}
				for _, want := range []string{"a***@example.com", `href="/forgot"`} {
					if !strings.Contains(body, want) {
						t.Errorf("Forgot() sent state omits %q", want)
					}
				}
			})
		}
	}
}

func TestEmailReentryOpensTheAccountFormAndRegistrationSentChangesHeading(t *testing.T) {
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
			sent := renderComponent(t, ctx, Register(RegisterMeta(ctx), AuthView{OffersResend: true, Email: "ada@example.com", Notice: "ada@example.com"}))
			heading := "Check your inbox"
			if locale == i18n.ZhHant {
				heading = "\u8acb\u5230\u4fe1\u7bb1\u6536\u4fe1"
			}
			if !strings.Contains(sent, `<h1 class="goen-auth__title">`+heading+`</h1>`) || !strings.Contains(sent, `href="/register"`) || !strings.Contains(sent, "ada@example.com") {
				t.Error("Register() sent state lost the inbox heading, destination or re-entry")
			}
			fresh := renderComponent(t, ctx, Register(RegisterMeta(ctx), AuthView{}))
			if strings.Contains(fresh, `<h1 class="goen-auth__title">`+heading+`</h1>`) {
				t.Error("Register() asks a new visitor to check their inbox")
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
				body := renderComponent(t, ctx, ContactPanel(ContactForm{Done: true, SignedIn: signedIn}))
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
