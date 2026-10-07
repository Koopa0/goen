package pages_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/pagestest"
)

func TestEmailLinkPageRenderContract(t *testing.T) {
	for _, tt := range []struct {
		name, destination, en, zh string
		recovery                  pages.EmailLinkRecovery
		token                     string
		envelopes                 int
	}{
		{name: "subscribe", recovery: pages.EmailLinkSubscribe, destination: "/newsletter", en: "Subscribe", zh: "訂閱"},
		{name: "contact", recovery: pages.EmailLinkContact, destination: "mailto:contact@koopa0.dev", en: "contact@koopa0.dev", zh: "contact@koopa0.dev"},
		{name: "verify", recovery: pages.EmailLinkVerify, destination: "/account#email-heading", en: "Email address", zh: "電子郵件"},
		{name: "reset", recovery: pages.EmailLinkReset, destination: "/forgot", en: "Ask for a new link", zh: "重新申請"},
		{name: "live", recovery: pages.EmailLinkNoRecovery, destination: "/newsletter/confirm", en: "Confirm", zh: "Confirm", token: "live-token", envelopes: 1},
		{name: "notice", recovery: pages.EmailLinkNoRecovery, envelopes: 1},
	} {
		for _, locale := range i18n.Locales() {
			t.Run(tt.name+"/"+locale.Tag(), func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				view := pages.EmailLinkView{
					Heading: "Email link", Body: "Open the next step.", Recovery: tt.recovery,
					Action: "/newsletter/confirm", Submit: "Confirm", Token: "live-token",
				}
				if tt.name == "notice" {
					view.Token = ""
				}
				var rendered strings.Builder
				if err := pages.EmailLinkPage(pages.EmailLinkMeta(view.Heading), view).Render(ctx, &rendered); err != nil {
					t.Fatal(err)
				}
				body := rendered.String()
				pagestest.AssertEmailLink(t, body, "Email link", "Open the next step.", tt.destination)
				doc, err := html.Parse(strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				type field struct {
					ID, Name, Type, Autocomplete, MaxLength, Value string
					Required                                       bool
				}
				type renderFacts struct {
					Headings, Envelopes       int
					DuplicateIDs              []string
					EmailFields               []field
					LabelTargets, FieldLabels []string
					Tokens                    []string
					Methods, Labels           []string
					TokenExposed              bool
				}
				got := renderFacts{TokenExposed: strings.Contains(body, "live-token")}
				ids := map[string]bool{}
				for n := range doc.Descendants() {
					if n.Type != html.ElementNode {
						continue
					}
					id := emailLinkAttr(n, "id")
					if id != "" {
						if ids[id] {
							got.DuplicateIDs = append(got.DuplicateIDs, id)
						}
						ids[id] = true
					}
					if n.Data == "h1" {
						got.Headings++
					}
					if !emailLinkInNotice(n) {
						continue
					}
					if emailLinkClass(n, "goen-medallion") {
						got.Envelopes++
					}
					if n.Data == "input" && emailLinkAttr(n, "name") == "token" {
						got.Tokens = append(got.Tokens, emailLinkAttr(n, "value"))
					}
					if n.Data == "input" && emailLinkAttr(n, "type") == "email" {
						got.EmailFields = append(got.EmailFields, field{
							ID: id, Name: emailLinkAttr(n, "name"), Type: "email", Autocomplete: emailLinkAttr(n, "autocomplete"),
							MaxLength: emailLinkAttr(n, "maxlength"), Value: emailLinkAttr(n, "value"), Required: emailLinkHasAttr(n, "required"),
						})
					}
					if n.Data == "label" {
						got.LabelTargets = append(got.LabelTargets, emailLinkAttr(n, "for"))
						got.FieldLabels = append(got.FieldLabels, emailLinkText(n))
					}
					if n.Data == "form" {
						got.Methods = append(got.Methods, emailLinkAttr(n, "method"))
					}
					if emailLinkClass(n, "goen-btn--primary") {
						got.Labels = append(got.Labels, emailLinkText(n))
					}
				}
				want := renderFacts{Headings: 1, Envelopes: tt.envelopes, TokenExposed: tt.token != ""}
				if tt.destination != "" {
					label := tt.en
					if locale == i18n.ZhHant {
						label = tt.zh
					}
					want.Labels = []string{label}
				}
				if tt.name == "subscribe" {
					want.EmailFields = []field{{ID: "newsletter-recovery-email", Name: "email", Type: "email", Autocomplete: "email", MaxLength: "254", Required: true}}
					want.LabelTargets = []string{"newsletter-recovery-email"}
					label := "Email"
					if locale == i18n.ZhHant {
						label = "電子郵件"
					}
					want.FieldLabels = []string{label}
					want.Methods = []string{"post"}
				}
				if tt.token != "" {
					want.Tokens = []string{tt.token}
					want.Methods = []string{"post"}
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("EmailLinkPage render contract (-want +got):\n%s", diff)
				}
			})
		}
	}
}

func emailLinkAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func emailLinkClass(n *html.Node, class string) bool {
	for value := range strings.FieldsSeq(emailLinkAttr(n, "class")) {
		if value == class {
			return true
		}
	}
	return false
}

func emailLinkInNotice(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if emailLinkClass(p, "notice") {
			return true
		}
	}
	return false
}

func emailLinkHasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func emailLinkText(n *html.Node) string {
	var result strings.Builder
	for child := range n.Descendants() {
		if child.Type == html.TextNode {
			result.WriteString(child.Data)
		}
	}
	return result.String()
}
