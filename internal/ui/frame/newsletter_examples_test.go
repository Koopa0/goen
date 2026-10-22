package frame_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

func TestNewsletterExampleStaysVisibleWithItsDraftAndRefusal(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, draft := range []struct{ name, value, refusal string }{
			{name: "empty"},
			{name: "filled", value: "you+news@example.com"},
			{name: "refused empty", refusal: "Refused"},
			{name: "refused filled", value: "Submitted <& value", refusal: "Refused"},
		} {
			t.Run(locale.Tag()+"/"+draft.name, func(t *testing.T) {
				t.Parallel()
				var markup strings.Builder
				if err := frame.NewsletterForm(frame.NewsletterState{Email: draft.value, FieldRefusal: draft.refusal}).Render(i18n.WithLocale(t.Context(), locale), &markup); err != nil {
					t.Fatal(err)
				}
				type facts struct {
					Value, Placeholder, Descriptions, Hint, Refusal string
					DescriptionAttributes                           int
					HintHidden                                      bool
				}
				var got facts
				z := html.NewTokenizer(strings.NewReader(markup.String()))
				for z.Next() != html.ErrorToken {
					token := z.Token()
					if token.Data != "input" {
						continue
					}
					for _, a := range token.Attr {
						switch a.Key {
						case "value":
							got.Value = a.Val
						case "placeholder":
							got.Placeholder = a.Val
						case "aria-describedby":
							got.Descriptions = a.Val
							got.DescriptionAttributes++
						}
					}
				}
				doc, err := html.Parse(strings.NewReader(markup.String()))
				if err != nil {
					t.Fatal(err)
				}
				for n := range doc.Descendants() {
					var id string
					for _, a := range n.Attr {
						if a.Key == "id" {
							id = a.Val
						}
					}
					if id != "newsletter-email-example" && id != "newsletter-error" {
						continue
					}
					var text strings.Builder
					for child := range n.Descendants() {
						if child.Type == html.TextNode {
							text.WriteString(child.Data)
						}
					}
					if id == "newsletter-error" {
						got.Refusal = text.String()
						continue
					}
					got.Hint = text.String()
					for _, a := range n.Attr {
						got.HintHidden = got.HintHidden || a.Key == "hidden" || a.Key == "class" && strings.Contains(a.Val, "sr-only")
					}
				}
				hint := "Example: you@example.com"
				if locale == i18n.ZhHant {
					hint = "例：you@example.com"
				}
				want := facts{Value: draft.value, Descriptions: "newsletter-email-example", DescriptionAttributes: 1, Hint: hint, Refusal: draft.refusal}
				if draft.refusal != "" {
					want.Descriptions += " newsletter-error"
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("newsletter draft, hint and refusal (-want +got):\n%s", diff)
				}
			})
		}
	}
}
