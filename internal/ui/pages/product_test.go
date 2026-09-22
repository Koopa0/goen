package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestSoldOutGuidanceMatchesAvailableOptionPickers(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, withOptions := range []bool{false, true} {
			name := "no options"
			if withOptions {
				name = "with options"
			}
			t.Run(string(locale)+"/"+name, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				view := ProductView{
					Slug: "sold-out", Name: "Sold out product", VariantID: "only-variant",
					SelectionOK: true, Exact: true,
				}
				if withOptions {
					view.Options = []ProductOption{{
						Name: "colour", Label: "Colour",
						Values: []ProductOptionValue{{Value: "blue", Label: "Blue", Selected: true}},
					}}
				}
				var body strings.Builder
				if err := Product(ProductMeta(&view), &view).Render(ctx, &body); err != nil {
					t.Fatal(err)
				}
				html := body.String()
				if got := strings.Contains(html, i18n.T(ctx, i18n.KeyAllSoldOutHint)); got != withOptions {
					t.Errorf("variant-selection hint visible = %t, want %t", got, withOptions)
				}
				for _, want := range []string{
					i18n.T(ctx, i18n.KeyAllSoldOut),
					`method="post" action="/p/sold-out/notify"`,
					`name="variant" value="only-variant"`,
					i18n.T(ctx, i18n.KeyRestockSubmit),
				} {
					if !strings.Contains(html, want) {
						t.Errorf("sold-out page is missing %q", want)
					}
				}
			})
		}
	}
}
