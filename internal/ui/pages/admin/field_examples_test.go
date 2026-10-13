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
				title, discount, category := "Summer sale", "Summer sale — NT$200 off everything", "Orders"
				headline, message, short, cta := "What goen sells", "Free delivery site-wide over NT$3,000", "Free over 3,000", "Take a look"
				days, inspection := "0 means no end date.", "For example: box damaged, accessories missing"
				if locale == i18n.ZhHant {
					prefix = "例："
					title, discount, category = "夏季特賣", "夏季全站折 NT$200", "訂單"
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
					{"coupon", Coupons(p, CouponsView{Draft: CouponDraft{Code: typed, Description: typed, PerCustomer: typed}, Errors: errors("code", "description", "percustomer")}), map[string]string{"c-code": prefix + "SUMMER20", "c-description": prefix + discount, "c-percustomer": prefix + "1"}},
					{"FAQ category", FAQ(p, &FAQView{Draft: FAQEntry{Category: typed}, Errors: errors("category")}), map[string]string{"faq-category": prefix + category}},
					{"hero and strip", Home(p, &HeroView{
						Draft:       HeroDraft{Headline: typed, HeadlineEn: typed, PrimaryHref: typed, SecondHref: typed, Days: typed},
						BannerDraft: BannerDraft{Message: typed, Short: typed, CTALabel: typed, MessageEn: typed, CTALabelEn: typed, CTAHref: typed, Code: typed, Days: typed},
						Errors:      errors("headline", "primaryhref", "second", "days", "message", "short", "message_en", "banner_cta", "banner_days"),
					}), map[string]string{
						"h-headline": prefix + headline, "h-headline-en": prefix + "Choose one good thing.", "h-phref": prefix + "/deals", "h-shref": prefix + "/about", "h-days": days,
						"b-message": prefix + message, "b-short": prefix + short, "b-cta-label": prefix + cta, "b-message-en": prefix + "Free delivery over NT$3,000", "b-cta-label-en": prefix + "Shop", "b-cta-href": prefix + "/deals", "b-code": prefix + "FREESHIP", "b-days": days,
					}},
					{"stock adjustment", Variants(p, VariantsView{Variants: []Variant{{SKU: "sample", DraftDelta: typed, DeltaError: refusal}}}), map[string]string{"adj-sample": prefix + "+10"}},
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
