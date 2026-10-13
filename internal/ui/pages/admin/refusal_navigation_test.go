package admin

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestDeskRefusalsNameTheirFormAndLinkToTheFirstInvalidControl(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			bad := i18n.T(ctx, i18n.KeyAdminNoticeRefused)
			cases := []struct {
				name                  string
				component             templ.Component
				action, target, first string
				title                 i18n.Key
				count                 int
			}{
				{"stock", Variants(layouts.Page{}, VariantsView{Variants: []Variant{{SKU: "TEST-SKU", DraftDelta: " +oops ", DeltaError: bad}}}), "/admin/stock/adjust#row-TEST-SKU", "row-TEST-SKU", "adj-TEST-SKU", i18n.KeyAdminRefusalStock, 1},
				{"details", ProductForm(layouts.Page{}, ProductView{Slug: "test", Name: "", Summary: "kept", Errors: map[string]string{"name": bad, "summary": bad}}), "/admin/products/test#sec-details", "sec-details", "p-name", i18n.KeyAdminProdBasics, 2},
				{"variant", ProductForm(layouts.Page{}, ProductView{Slug: "test", VariantDraft: VariantDraft{SKU: "", Price: "wrong"}, Errors: map[string]string{"sku": bad, "price": bad}}), "/admin/products/test/variants#sec-variants", "sec-variants", "v-sku", i18n.KeyAdminProdVariantAdd, 2},
				{"option", ProductForm(layouts.Page{}, ProductView{Slug: "test", Errors: map[string]string{"option": bad}}), "/admin/products/test/options#sec-options", "sec-options", "opt-name", i18n.KeyAdminProdOptionAdd, 1},
				{"spec", ProductForm(layouts.Page{}, ProductView{Slug: "test", Errors: map[string]string{"spec_label": bad, "spec_value": bad}}), "/admin/products/test/specs#sec-specs", "sec-specs", "spec-label", i18n.KeyAdminProdSpecs, 2},
				{"brand", Taxonomy(layouts.Page{}, &TaxonomyView{Which: "brands", Errors: map[string]string{"name": bad, "slug": bad}}), "/admin/taxonomy/brands#new-brands", "new-brands", "brands-name", i18n.KeyAdminTaxNewBrand, 2},
				{"category", Taxonomy(layouts.Page{}, &TaxonomyView{Which: "categories", Errors: map[string]string{"icon_key": bad}}), "/admin/taxonomy/categories#new-categories", "new-categories", "categories-icon", i18n.KeyAdminTaxNewCategory, 1},
				{"faq-edit", FAQ(layouts.Page{}, &FAQView{Rows: []FAQEntry{{ID: "faq1", Question: "stored", Answer: "stored"}}, Edit: FAQEntry{ID: "faq1", Question: "", Answer: " kept ", QuestionEn: "bad"}, EditErrors: map[string]string{"question": bad, "question_en": bad}}), "/admin/faq/faq1#faq-faq1", "faq-faq1", "q-faq1", i18n.KeyAdminRefusalFAQ, 2},
				{"hero", Home(layouts.Page{}, &HeroView{Errors: map[string]string{"headline": bad, "primary": bad}}), "/admin/home#new-hero", "new-hero", "h-headline", i18n.KeyAdminRefusalHero, 2},
				{"banner", Home(layouts.Page{}, &HeroView{Errors: map[string]string{"message_en": bad, "banner_cta": bad}}), "/admin/home/banner#new-banner", "new-banner", "b-message-en", i18n.KeyAdminRefusalBanner, 2},
				{"method", Shipping(layouts.Page{}, ShippingView{Errors: map[string]string{"code": bad, "fee": bad}}), "/admin/shipping/method#new-method", "new-method", "m-code", i18n.KeyAdminShipAddMethod, 2},
				{"zone", Shipping(layouts.Page{}, ShippingView{Errors: map[string]string{"zone_code": bad, "prefixes": bad}}), "/admin/shipping/zone#new-zone", "new-zone", "z-code", i18n.KeyAdminShipAddZone, 2},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					body := renderComponent(t, ctx, tc.component)
					doc, err := html.Parse(strings.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					var alert *html.Node
					var form, target, first *html.Node
					ids := map[string]*html.Node{}
					for n := range doc.Descendants() {
						if n.Type != html.ElementNode {
							continue
						}
						id := refusalAttr(n, "id")
						if id != "" {
							ids[id] = n
						}
						if n.Data == "form" && refusalAttr(n, "action") == tc.action {
							form = n
						}
						if id == tc.target {
							target = n
						}
						if id == tc.first {
							first = n
						}
						if refusalAttr(n, "role") == "alert" && strings.Contains(refusalText(n), i18n.T(ctx, tc.title)) {
							for link := range n.Descendants() {
								if link.Data == "a" && refusalAttr(link, "href") == "#"+tc.first {
									alert = n
								}
							}
						}
					}
					if form == nil || target == nil {
						t.Errorf("form must post to its section: %s", tc.action)
					}
					if alert == nil {
						t.Fatalf("page-top alert must name %q and link to #%s", i18n.T(ctx, tc.title), tc.first)
					}
					count := "2"
					if tc.count == 1 {
						count = "1"
					}
					if !strings.Contains(refusalText(alert), count) {
						t.Errorf("alert does not count %d invalid fields", tc.count)
					}
					for n := range doc.Descendants() {
						if n == alert {
							break
						}
						if n.Data == "form" && strings.Contains(refusalAttr(n, "class"), "goen-admin__") {
							t.Error("summary must precede the forms")
							break
						}
					}
					if first == nil || refusalAttr(first, "aria-invalid") != "true" {
						t.Errorf("first invalid control %s is not marked", tc.first)
					}
					for n := range doc.Descendants() {
						if n.Type != html.ElementNode || refusalAttr(n, "aria-invalid") != "true" {
							continue
						}
						describes := strings.Fields(refusalAttr(n, "aria-describedby"))
						if len(describes) == 0 {
							t.Errorf("invalid control %s has no description", refusalAttr(n, "id"))
						}
						for _, id := range describes {
							if ids[id] == nil || strings.TrimSpace(refusalText(ids[id])) == "" {
								t.Errorf("invalid control references missing error %s", id)
							}
						}
					}
				})
			}
		})
	}
}

func TestRefusedInspectionKeepsBlankAndUnreadableCounts(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", " +oops "} {
		view := ReturnsView{Rows: []Return{{ID: "r1", Status: "approved", Decided: true, Window: "within", Lines: []ReturnLine{{OrderLineID: "l1", Quantity: 2, Restockable: true, DraftReceived: raw, DraftRestocked: raw}}}}, Errors: map[string]string{"r1.inspect": "bad count"}}
		body := renderToString(t, Returns(layouts.Page{}, view))
		doc, err := html.Parse(strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"recv-r1-l1", "stock-r1-l1"} {
			for n := range doc.Descendants() {
				if refusalAttr(n, "id") == id {
					if refusalAttr(n, "value") != raw {
						t.Errorf("%s lost raw count %q", id, raw)
					}
					if raw != "" && refusalAttr(n, "type") == "number" {
						t.Errorf("%s lets the browser discard unreadable input", id)
					}
				}
			}
		}
		if !strings.Contains(body, `action="/admin/returns/r1/inspect#inspect-r1"`) || !strings.Contains(body, `href="#recv-r1-l1"`) {
			t.Error("inspection has no return to its first refused field")
		}
	}
}

func refusalAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func refusalText(n *html.Node) string {
	if n == nil {
		return ""
	}
	var out strings.Builder
	if n.Type == html.TextNode {
		out.WriteString(n.Data)
	}
	for child := range n.Descendants() {
		if child.Type == html.TextNode {
			out.WriteString(child.Data)
		}
	}
	return out.String()
}

func TestRefusalSummaryCountsInEnglish(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, tt := range []struct {
		count int
		want  string
	}{
		{1, "Stock adjustment was not saved. Please correct 1 field."},
		{2, "Stock adjustment was not saved. Please correct 2 fields."},
	} {
		if got := (refusalSummary{Form: i18n.KeyAdminRefusalStock, Count: tt.count}).Text(ctx); got != tt.want {
			t.Errorf("Text(%d) = %q, want %q", tt.count, got, tt.want)
		}
	}
}

func TestRefusalSummariesCountEachRefusalOnce(t *testing.T) {
	t.Parallel()
	view := ReturnsView{
		Rows: []Return{{ID: "r1", Lines: []ReturnLine{
			{OrderLineID: "l1", Restockable: true}, {OrderLineID: "l2", Restockable: true},
		}}},
		Errors: map[string]string{"r1.inspect": "bad count"},
	}
	got := view.InspectionRefusals()
	if len(got) != 1 || got[0].Count != 1 || got[0].First != "recv-r1-l1" {
		t.Errorf("InspectionRefusals() = %+v, want one refusal at recv-r1-l1", got)
	}

	product := &ProductView{
		Options: []Option{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Errors:  map[string]string{"options": "pick one"},
	}
	if got := product.VariantRefusal(); got.Count != 1 || got.First != "v-opt-a" {
		t.Errorf("VariantRefusal() = %+v, want Count 1 at v-opt-a", got)
	}
}
