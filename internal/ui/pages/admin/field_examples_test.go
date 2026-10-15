package admin

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestAdminExamplesStayOutsideRefusedInputs(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, draft := range []struct{ value, refusal string }{{"", ""}, {"Submitted <& value", ""}, {"", "Refused"}, {"Submitted <& value", "Refused"}} {
			typed, refusal := draft.value, draft.refusal
			t.Run(locale.Tag()+"/"+typed+"/"+refusal, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				errors := func(fields ...string) map[string]string {
					if refusal == "" {
						return nil
					}
					result := make(map[string]string, len(fields))
					for _, field := range fields {
						result[field] = refusal
					}
					return result
				}
				prefix := "Example: "
				title, category := "Summer sale", "Orders"
				headline, message, short, cta := "What goen sells", "Free delivery site-wide over NT$3,000", "Free over 3,000", "Take a look"
				days, inspection := "0 means no end date.", "For example: box damaged, accessories missing"
				if locale == i18n.ZhHant {
					prefix = "例："
					title, category = "夏季特賣", "訂單"
					headline, message, short, cta = "goen 販售的商品", "全站滿 NT$3,000 免運", "滿 3,000 免運", "看看"
					days, inspection = "0 表示不設結束日。", "例如：外盒破損、配件缺少"
				}
				p := layouts.Page{}
				for _, page := range []struct {
					name      string
					component templ.Component
					fields    map[string]string
				}{
					{"campaign", Campaigns(p, CampaignsView{Draft: CampaignDraft{Title: typed, Slug: typed}, Errors: errors("title", "slug")}), map[string]string{"k-title": prefix + title, "k-slug": prefix + "summer-sale"}},
					{"FAQ category", FAQ(p, &FAQView{CategoryDraft: FAQCategory{Name: typed}, CategoryErrors: errors("name")}), map[string]string{"faq-new-category-name": prefix + category}},
					{"hero and strip", Home(p, &HeroView{
						Draft:       HeroDraft{Headline: typed, HeadlineEn: typed, PrimaryHref: typed, SecondHref: typed, Days: typed},
						BannerDraft: BannerDraft{Message: typed, Short: typed, CTALabel: typed, MessageEn: typed, CTALabelEn: typed, CTAHref: typed, Code: typed, Days: typed},
						Errors:      errors("headline", "primaryhref", "second", "days", "message", "short", "message_en", "banner_cta", "banner_days"),
					}), map[string]string{
						"h-headline": prefix + headline, "h-headline-en": prefix + "Choose one good thing.", "h-phref": prefix + "/deals", "h-shref": prefix + "/about", "h-days": days,
						"b-message": prefix + message, "b-short": prefix + short, "b-cta-label": prefix + cta, "b-message-en": prefix + "Free delivery over NT$3,000", "b-cta-label-en": prefix + "Shop", "b-cta-href": prefix + "/deals", "b-code": prefix + "FREESHIP", "b-days": days,
					}},
					{"return inspection", Returns(p, ReturnsView{Rows: []Return{{ID: "request", Status: returns.StatusApproved, Window: returns.WindowStatutory, Decided: true, Lines: []ReturnLine{{OrderLineID: "line", Quantity: 1, DraftNote: typed}}}}, Errors: errors("request.inspect")}), map[string]string{"note-request-line": inspection}},
				} {
					t.Run(page.name, func(t *testing.T) {
						markup := renderComponent(t, ctx, page.component)
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
						for id, example := range page.fields {
							input := exampleElementByID(doc, id)
							if input == nil || input.Data != "input" {
								t.Fatalf("refused %s has no input %s", page.name, id)
							}
							if attr(input, "placeholder") != "" || attr(input, "value") != typed {
								t.Errorf("%s placeholder/value = %q/%q, want empty/%q", id, attr(input, "placeholder"), attr(input, "value"), typed)
							}
							var linkedExample bool
							for _, description := range strings.Fields(attr(input, "aria-describedby")) {
								hint := exampleElementByID(doc, description)
								if hint == nil {
									t.Errorf("%s description %s has no text", id, description)
									continue
								}
								_, hidden := attrPresent(hint, "hidden")
								if textOf(hint) == example && !hidden && !strings.Contains(attr(hint, "class"), "sr-only") {
									linkedExample = true
								}
							}
							if !linkedExample {
								t.Errorf("refused %s has no visible linked example %q", id, example)
							}
						}
					})
				}
			})
		}
	}
}

func exampleElementByID(n *html.Node, id string) *html.Node {
	if n.Type == html.ElementNode && attr(n, "id") == id {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := exampleElementByID(child, id); found != nil {
			return found
		}
	}
	return nil
}

func TestFAQEnglishFieldsKeepVisibleTranslatedLabelsAndRefusedDrafts(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, draft := range []struct{ name, value, refusal string }{
			{name: "empty"},
			{name: "filled", value: "Submitted <& value"},
			{name: "refused empty", refusal: "Refused"},
			{name: "refused filled", value: "Submitted <& value", refusal: "Refused"},
		} {
			t.Run(locale.Tag()+"/"+draft.name, func(t *testing.T) {
				t.Parallel()
				entry := FAQEntry{ID: "existing", QuestionEn: draft.value, AnswerEn: draft.value}
				view := &FAQView{Rows: []FAQEntry{entry}}
				if draft.refusal != "" {
					view.Edit = entry
					view.EditErrors = map[string]string{"question_en": draft.refusal, "answer_en": draft.refusal}
				}
				ctx := i18n.WithLocale(t.Context(), locale)
				doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, FAQ(layouts.Page{}, view))))
				if err != nil {
					t.Fatal(err)
				}
				for _, field := range []struct{ id, name, zh, en string }{
					{id: "qe-existing", name: "question_en", zh: "問題（英文）", en: "Question (English)"},
					{id: "ae-existing", name: "answer_en", zh: "答案（英文）", en: "Answer (English)"},
				} {
					input := exampleElementByID(doc, field.id)
					if input == nil {
						t.Fatalf("FAQ has no field %q", field.id)
					}
					value := attr(input, "value")
					if input.Data == "textarea" {
						value = textOf(input)
					}
					if value != draft.value || attr(input, "placeholder") != "" {
						t.Errorf("FAQ %q value/placeholder = %q/%q, want %q/empty", field.name, value, attr(input, "placeholder"), draft.value)
					}
					wantLabel := field.en
					if locale == i18n.ZhHant {
						wantLabel = field.zh
					}
					var visibleLabels int
					for n := range doc.Descendants() {
						if n.Type == html.ElementNode && n.Data == "label" && attr(n, "for") == field.id && !strings.Contains(attr(n, "class"), "sr-only") && textOf(n) == wantLabel {
							visibleLabels++
						}
					}
					if visibleLabels != 1 {
						t.Errorf("FAQ %q has %d visible labels %q, want one", field.name, visibleLabels, wantLabel)
					}
					if draft.refusal != "" {
						errorID := "faq-err-" + field.name + "-existing"
						errorText := exampleElementByID(doc, errorID)
						if attr(input, "aria-invalid") != "true" || attr(input, "aria-describedby") != errorID || errorText == nil || textOf(errorText) != draft.refusal {
							t.Errorf("FAQ %q loses its linked refusal", field.name)
						}
					}
				}
			})
		}
	}
}
