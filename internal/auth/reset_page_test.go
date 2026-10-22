package auth

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestForgotSentResend: the forgot page's sent state keeps its resend form in a
// closed native disclosure and does not autofocus the closed form, while the
// fresh page keeps its single open form with its initial focus.
func TestForgotSentResend(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, sent := range []bool{false, true} {
			t.Run(locale.Tag()+"/"+map[bool]string{false: "fresh", true: "sent"}[sent], func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				body := renderComponent(t, ctx, Forgot(frame.Page{}, ForgotView{Sent: sent, Address: "a***@example.com"}))
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
