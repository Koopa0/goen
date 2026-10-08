package admin

import (
	"bytes"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestTheNewProductSectionIsNotNamedLikeItsFirstField(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale  i18n.Locale
		heading string
	}{{i18n.ZhHant, "基本資料"}, {i18n.En, "Basic details"}} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			body := renderComponent(t, ctx, ProductForm(layouts.Page{Title: "Product"}, ProductView{IsNew: true}))
			if want := `<h2 class="goen-admin__heading">` + tt.heading + `</h2>`; !strings.Contains(body, want) {
				t.Errorf("new product page lacks %q", want)
			}
			if strings.Contains(body, `placeholder="ceramic-mug"`) {
				t.Error("the slug example is a placeholder that reads as a filled value")
			}
		})
	}
}

func TestATaxonomyRowSavesWhatItShows(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		want   string
	}{{i18n.ZhHant, "儲存"}, {i18n.En, "Save"}} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			view := &TaxonomyView{Brands: []Taxon{{Slug: "saelo", Name: "沙羅"}}}
			body := renderComponent(t, ctx, Taxonomy(layouts.Page{Title: "Brands"}, view))
			i := strings.Index(body, `value="rename"`)
			if i < 0 {
				t.Fatal("no row save button")
			}
			end := strings.Index(body[i:], "</button>")
			if got := body[i : i+end]; !strings.HasSuffix(strings.TrimSpace(got), tt.want) {
				t.Errorf("row button reads %q, want it to end with %q", got, tt.want)
			}
		})
	}
}

func TestAnExampleIsItsOwnHintLine(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	pages := map[string]string{
		"shipping": renderComponent(t, ctx, Shipping(layouts.Page{Title: "Shipping"}, ShippingView{})),
		"product":  renderComponent(t, ctx, ProductForm(layouts.Page{Title: "Product"}, ProductView{IsNew: true})),
		"taxonomy": renderComponent(t, ctx, Taxonomy(layouts.Page{Title: "Brands"}, &TaxonomyView{})),
		"tiers":    renderComponent(t, ctx, Tiers(layouts.Page{Title: "Tiers"}, TiersView{})),
	}
	for name, body := range pages {
		if strings.Contains(body, "。 ") {
			t.Errorf("%s page runs a sentence into the next on one line: %q", name, body[strings.Index(body, "。 ")-20:strings.Index(body, "。 ")+30])
		}
		if !strings.Contains(body, "例如 ") {
			t.Errorf("%s page has no example line", name)
		}
	}
}

func TestAMethodWithoutEnglishSaysSoAndMarksTheOtherName(t *testing.T) {
	t.Parallel()
	view := ShippingView{Methods: []ShippingMethod{
		{MethodID: "a", VersionID: "va", Destination: destination.Address, Name: "宅配到府", NameEn: "Home delivery"},
		{MethodID: "b", VersionID: "vb", Destination: destination.Address, Name: "超商取貨"},
	}}
	for _, tt := range []struct {
		locale    i18n.Locale
		otherLang string
		pills     int
	}{{i18n.En, `lang="zh-Hant">宅配到府<`, 1}, {i18n.ZhHant, `lang="en">Home delivery<`, 1}} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			var out bytes.Buffer
			if err := Shipping(layouts.Page{Title: "Shipping"}, view).Render(ctx, &out); err != nil {
				t.Fatal(err)
			}
			heads := strings.Split(out.String(), `<h2 class="goen-admin__heading">`)
			pill := i18n.T(ctx, i18n.KeyAdminUntranslated)
			got := 0
			for _, h := range heads[1:3] {
				head, _, _ := strings.Cut(h, "</h2>")
				got += strings.Count(head, pill)
			}
			if got != tt.pills {
				t.Errorf("%d %q pills in the method headings, want %d", got, pill, tt.pills)
			}
			if !strings.Contains(out.String(), `class="goen-admin__othername" `+tt.otherLang) {
				t.Errorf("other-language name is not marked %s", tt.otherLang)
			}
		})
	}
}

func TestEveryExampleHintReadsInFull(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale   i18n.Locale
		sentence func(example string) string
	}{
		{i18n.ZhHant, func(e string) string { return "<p class=\"goen-admin__hint\">例如 " + e + "。</p>" }},
		{i18n.En, func(e string) string { return "<p class=\"goen-admin__hint\">For example, " + e + ".</p>" }},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			for name, c := range map[string]struct {
				body     string
				examples []string
			}{
				"product":  {renderComponent(t, ctx, ProductForm(layouts.Page{Title: "Product"}, ProductView{IsNew: true})), []string{"ceramic-mug"}},
				"taxonomy": {renderComponent(t, ctx, Taxonomy(layouts.Page{Title: "Brands"}, &TaxonomyView{})), []string{"north-light", "cookware", "kitchen"}},
				"shipping": {renderComponent(t, ctx, Shipping(layouts.Page{Title: "Shipping"}, ShippingView{})), []string{"express_delivery", "mountain", "313 546 556"}},
				"tiers":    {renderComponent(t, ctx, Tiers(layouts.Page{Title: "Tiers"}, TiersView{})), []string{"Silver"}},
				"faq":      {renderComponent(t, ctx, FAQ(layouts.Page{Title: "FAQ"}, &FAQView{})), []string{"Orders"}},
			} {
				for _, e := range c.examples {
					if want := tt.sentence(e); !strings.Contains(c.body, want) {
						t.Errorf("%s page lacks the hint %s", name, want)
					}
				}
			}
		})
	}
}
