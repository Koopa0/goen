package products

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
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
			body := renderComponent(t, ctx, ProductForm(frame.Page{Title: "Product"}, ProductView{IsNew: true}))
			if want := `<h2 class="goen-admin__heading">` + tt.heading + `</h2>`; !strings.Contains(body, want) {
				t.Errorf("new product page lacks %q", want)
			}
			if strings.Contains(body, `placeholder="ceramic-mug"`) {
				t.Error("the slug example is a placeholder that reads as a filled value")
			}
		})
	}
}

func TestAnExampleIsItsOwnHintLine(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	pages := map[string]string{
		"product": renderComponent(t, ctx, ProductForm(frame.Page{Title: "Product"}, ProductView{IsNew: true})),
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
				"product": {renderComponent(t, ctx, ProductForm(frame.Page{Title: "Product"}, ProductView{IsNew: true})), []string{"ceramic-mug"}},
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
