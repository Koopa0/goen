package contact_test

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/contact"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestContactOwnedEmail: the public contact page and footer use the owned email
// and no unowned contact detail.
func TestContactOwnedEmail(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for name, component := range map[string]templ.Component{
			"contact": contact.Contact(frame.Page{}, contact.Form{}),
			"footer":  frame.Footer(frame.NewsletterState{}),
		} {
			t.Run(string(locale)+"/"+name, func(t *testing.T) {
				var b strings.Builder
				if err := component.Render(i18n.WithLocale(t.Context(), locale), &b); err != nil {
					t.Fatal(err)
				}
				out := b.String()
				if !strings.Contains(out, `href="mailto:contact@koopa0.dev">contact@koopa0.dev</a>`) && name == "footer" {
					t.Error("footer does not expose the owned mailbox")
				}
				if !strings.Contains(out, `href="mailto:contact@koopa0.dev"`) || !strings.Contains(out, `>contact@koopa0.dev<`) {
					t.Error("contact destination or visible mailbox is missing")
				}
				for _, forbidden := range []string{"support@goen.tw", "02-2700-1234", "tel:+886227001234"} {
					if strings.Contains(out, forbidden) {
						t.Errorf("unowned contact detail %q remains", forbidden)
					}
				}
			})
		}
	}
}

// TestContactOrderNumberExample: the order number field shows the format goen
// issues.
func TestContactOrderNumberExample(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		var b strings.Builder
		if err := contact.Contact(frame.Page{Title: "contact"}, contact.Form{}).Render(i18n.WithLocale(t.Context(), locale), &b); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		if !strings.Contains(out, `id="contact-order_ref-example"`) || !strings.Contains(out, "GO-260101-000001") {
			t.Errorf("%s: the order number field does not show an order number in the issued format", locale)
		}
		if strings.Contains(out, `placeholder="#GO-"`) {
			t.Errorf("%s: the order number field still shows #GO-", locale)
		}
	}
}

// TestPanelConfirmationFocus: the contact confirmation takes focus from the
// form it replaces. The form swaps itself for the confirmation, and the button
// that had focus goes with it. The form names the element that takes focus
// next; the confirmation has to be that element, a status message the script
// can focus.
func TestPanelConfirmationFocus(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			render := func(f contact.Form) *html.Node {
				var b strings.Builder
				if err := contact.Panel(f).Render(ctx, &b); err != nil {
					t.Fatal(err)
				}
				doc, err := html.Parse(strings.NewReader(b.String()))
				if err != nil {
					t.Fatal(err)
				}
				return doc
			}
			form := elementByTag(render(contact.Form{}), "form")
			if form == nil {
				t.Fatal("the contact panel has no form")
			}
			target := nodeAttr(form, "data-focus-after-swap")
			if target == "" {
				t.Fatal("the contact form names no element to take focus after it is swapped away")
			}
			done := elementByID(render(contact.Form{Done: true}), target)
			if done == nil {
				t.Fatalf("the confirmation has no element with id %q", target)
			}
			if got := nodeAttr(done, "tabindex"); got != "-1" {
				t.Errorf("the confirmation's tabindex = %q, want -1: a script cannot focus it otherwise", got)
			}
			if got := nodeAttr(done, "role"); got != "status" {
				t.Errorf("the confirmation's role = %q, want status", got)
			}
		})
	}
}

func elementByTag(n *html.Node, tag string) *html.Node {
	for d := range n.Descendants() {
		if d.Type == html.ElementNode && d.Data == tag {
			return d
		}
	}
	return nil
}

func elementByID(n *html.Node, id string) *html.Node {
	for d := range n.Descendants() {
		if d.Type == html.ElementNode && nodeAttr(d, "id") == id {
			return d
		}
	}
	return nil
}

func nodeAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
