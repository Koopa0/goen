package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/productlabel"
)

func TestProductLabelRendersWithoutOtherDetails(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, set := range []bool{false, true} {
			view := ProductView{Slug: "label", Name: "Label fixture"}
			if set {
				age := int16(0)
				view.LabelFacts = &productlabel.Facts{Origin: "<script>origin</script>", NetQuantity: "0.01", NetUnit: productlabel.Gram, MinAgeMonths: &age}
			}
			var body strings.Builder
			if err := Product(ProductMeta(&view), &view).Render(ctx, &body); err != nil {
				t.Fatal(err)
			}
			markup := body.String()
			if strings.Contains(markup, `id="product-label-heading"`) != set {
				t.Errorf("%s facts=%v: section visibility wrong", locale, set)
			}
			if set && (!strings.Contains(markup, "&lt;script&gt;origin&lt;/script&gt;") || !strings.Contains(markup, "0.01 g") || !strings.Contains(markup, i18n.T(ctx, i18n.KeyLabelMinAge))) {
				t.Errorf("%s: facts missing or not escaped", locale)
			}
			if strings.Contains(markup, "<script>origin</script>") {
				t.Error("origin was not escaped")
			}
		}
	}
}
