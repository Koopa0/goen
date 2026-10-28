package products

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

// TestProductEditorFieldExamples: the editor's examples sit under their fields, never in
// a placeholder, so a refused form that re-renders an empty field does not read as a value.
func TestProductEditorFieldExamples(t *testing.T) {
	t.Parallel()
	examples := map[string]string{
		"p-alt":           "白色陶瓷馬克杯，側面，把手朝右",
		"p-alt-en":        "White ceramic mug, side view, handle on the right",
		"opt-name":        "顏色",
		"opt-name-en":     "Colour",
		"optval-value":    "星霧藍",
		"optval-value-en": "Mist Blue",
		"optval-swatch":   "#1c1c1e",
		"spec-label":      "容量",
		"spec-value":      "350 ml",
		"spec-label-en":   "Capacity",
		"spec-value-en":   "350 ml",
	}
	for _, locale := range i18n.Locales() {
		for _, draft := range []struct{ value, refusal string }{{"", ""}, {"Submitted <& value", ""}, {"", "Refused"}, {"Submitted <& value", "Refused"}} {
			typed, refusal := draft.value, draft.refusal
			t.Run(locale.Tag()+"/"+typed+"/"+refusal, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				prefix := "Example: "
				if locale == i18n.ZhHant {
					prefix = "例："
				}
				view := ProductView{
					Slug:    "editor-test",
					Options: []Option{{ID: "option-1", Name: "Colour"}},
					ImageUploadDraft: ImageUploadDraft{
						Alt: typed, AltEn: typed,
					},
					VariantDraft: VariantDraft{
						OptionName: typed, OptionNameEn: typed,
						Value: typed, ValueEn: typed, Swatch: typed,
					},
					SpecDraft: SpecDraft{Label: typed, Value: typed, LabelEn: typed, ValueEn: typed},
				}
				if refusal != "" {
					view.Errors = map[string]string{}
					for _, field := range []string{
						"alt", "alt_en", "option", "option_en", "value", "value_en", "swatch_hex",
						"spec_label", "spec_value", "spec_label_en", "spec_value_en",
					} {
						view.Errors[field] = refusal
					}
				}
				markup := renderComponent(t, ctx, productImages(view)) +
					renderComponent(t, ctx, productOptions(view)) +
					renderComponent(t, ctx, productSpecs(view))
				z := html.NewTokenizer(strings.NewReader(markup))
				for z.Next() != html.ErrorToken {
					token := z.Token()
					if token.Data != "input" {
						continue
					}
					var descriptions int
					for _, attribute := range token.Attr {
						if attribute.Key == "aria-describedby" {
							descriptions++
						}
					}
					if descriptions > 1 {
						t.Error("an input repeats aria-describedby")
					}
				}
				doc, err := html.Parse(strings.NewReader(markup))
				if err != nil {
					t.Fatal(err)
				}
				for id, example := range examples {
					input := exampleByID(doc, id)
					if input == nil || input.Data != "input" {
						t.Fatalf("no input %s", id)
					}
					if attr(input, "placeholder") != "" || attr(input, "value") != typed {
						t.Errorf("%s placeholder/value = %q/%q, want empty/%q", id, attr(input, "placeholder"), attr(input, "value"), typed)
					}
					var linked bool
					for _, description := range strings.Fields(attr(input, "aria-describedby")) {
						hint := exampleByID(doc, description)
						if hint == nil {
							t.Errorf("%s description %s has no text", id, description)
							continue
						}
						_, hidden := attrPresent(hint, "hidden")
						if description == id+"-example" && refusalText(hint) == prefix+example && !hidden && hint.Data == "p" {
							linked = true
						}
					}
					if !linked {
						t.Errorf("%s has no visible linked example %q", id, prefix+example)
					}
					wantID := id + "-example"
					if refusal != "" {
						wantID += " " + id + "-error"
					}
					if got := attr(input, "aria-describedby"); got != wantID {
						t.Errorf("%s aria-describedby = %q, want %q", id, got, wantID)
					}
					// admin.css pins a field's direct hint to one grid row, so a second
					// one would draw on top of the first: they share one wrapper.
					field := input.Parent
					var hints []*html.Node
					for child := field.FirstChild; child != nil; child = child.NextSibling {
						if child.Type == html.ElementNode && (hasClass(child, "goen-admin__hint") || hasClass(child, "goen-admin__hints")) {
							hints = append(hints, child)
						}
					}
					if len(hints) != 1 {
						t.Errorf("%s field has %d direct hints, want 1", id, len(hints))
						continue
					}
					twoHints := id == "p-alt" || id == "p-alt-en"
					if twoHints != hasClass(hints[0], "goen-admin__hints") {
						t.Errorf("%s hint wrapper = %q, want a wrapper only for the fields with their own hint", id, attr(hints[0], "class"))
					}
					if twoHints && exampleByID(hints[0], id+"-example") == nil {
						t.Errorf("%s example sits outside the hints wrapper", id)
					}
				}
			})
		}
	}
}

func hasClass(n *html.Node, class string) bool {
	return slices.Contains(strings.Fields(attr(n, "class")), class)
}

func exampleByID(n *html.Node, id string) *html.Node {
	if n.Type == html.ElementNode && attr(n, "id") == id {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := exampleByID(child, id); found != nil {
			return found
		}
	}
	return nil
}
