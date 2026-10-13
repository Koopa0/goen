package pages

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestGoogleOnlyAccountOffersAMailboxPasswordSetup(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, hasPassword := range []bool{false, true} {
			name := "google-only/" + locale.Tag()
			if hasPassword {
				name = "password/" + locale.Tag()
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				body := renderComponent(t, ctx, Account(AccountMeta(ctx), &AccountView{
					Email: "shopper@example.com", GoogleLinked: true, HasPassword: hasPassword, CanUnlinkGoogle: hasPassword,
				}))
				doc, err := html.Parse(strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				section := findDescendant(doc, func(n *html.Node) bool {
					return n.Type == html.ElementNode && n.Data == "section" && attrValue(n, "aria-labelledby") == "password-heading"
				})
				if section == nil {
					t.Fatal("account page lost its password section")
				}
				setup := findDescendant(section, func(n *html.Node) bool {
					return n.Type == html.ElementNode && n.Data == "form" && attrValue(n, "action") == "/account/password/setup#password-heading"
				})
				if hasPassword {
					if setup != nil {
						t.Error("an account with a password is offered first-password setup")
					}
					if findDescendant(section, func(n *html.Node) bool {
						return n.Type == html.ElementNode && n.Data == "input" && attrValue(n, "name") == "current"
					}) == nil {
						t.Error("password change no longer requires the current password")
					}
					if findDescendant(doc, func(n *html.Node) bool {
						return n.Type == html.ElementNode && n.Data == "form" && attrValue(n, "action") == "/account/google/unlink"
					}) == nil {
						t.Error("an account with a password cannot unlink Google")
					}
					return
				}
				if setup == nil {
					t.Fatal("Google-only account has no mailbox setup form")
				}
				if attrValue(setup, "method") != "post" {
					t.Error("mailbox setup is not a plain POST")
				}
				if findDescendant(section, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "input" }) != nil {
					t.Error("Google-only account is asked to type a password before mailbox proof")
				}
				label := "Send a set-up link"
				linkLabel := "Set a password"
				googleSentence := "Google is your only way to sign in, so it can't be unlinked. Set a password first."
				if locale == i18n.ZhHant {
					label = "\u5bc4\u9001\u8a2d\u5b9a\u9023\u7d50"
					linkLabel = "\u8a2d\u5b9a\u5bc6\u78bc"
					googleSentence = "Google \u662f\u76ee\u524d\u552f\u4e00\u7684\u767b\u5165\u65b9\u5f0f\uff0c\u6240\u4ee5\u4e0d\u80fd\u53d6\u6d88\u9023\u7d50\u3002\u5148\u8a2d\u5b9a\u5bc6\u78bc\u5c31\u53ef\u4ee5\u3002"
				}
				heading := findDescendant(section, func(n *html.Node) bool {
					return n.Type == html.ElementNode && n.Data == "h2" && attrValue(n, "id") == "password-heading"
				})
				if heading == nil || nodeText(heading) != linkLabel {
					t.Errorf("password-less section heading does not say %q", linkLabel)
				}
				button := findDescendant(setup, func(n *html.Node) bool {
					return n.Type == html.ElementNode && n.Data == "button" && attrValue(n, "type") == "submit"
				})
				if button == nil || nodeText(button) != label {
					t.Errorf("setup form has no submit button %q", label)
				}
				link := findDescendant(doc, func(n *html.Node) bool {
					return n.Type == html.ElementNode && n.Data == "a" && attrValue(n, "href") == "#password-heading" && nodeText(n) == linkLabel
				})
				if link == nil || nodeText(link.Parent) != googleSentence {
					t.Errorf("Google-only explanation does not link to password setup with %q", googleSentence)
				}
			})
		}
	}
}

func TestPasswordSetupNoticeNamesTheCurrentMailbox(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			body := renderComponent(t, ctx, Account(AccountMeta(ctx), &AccountView{
				Email: "shopper@example.com", GoogleLinked: true, PasswordSetupSent: true,
			}))
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			want := "A link to set your password was sent to shopper@example.com."
			if locale == i18n.ZhHant {
				want = "\u8a2d\u5b9a\u5bc6\u78bc\u7684\u9023\u7d50\u5df2\u5bc4\u5230 shopper@example.com\u3002"
			}
			notice := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && attrValue(n, "role") == "status" && nodeText(n) == want
			})
			if notice == nil {
				t.Errorf("password setup confirmation does not announce %q", want)
			}
		})
	}
}
