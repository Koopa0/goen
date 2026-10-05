package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/productlabel"
)

func TestRefusedLabelFormRetainsOptionalValues(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		input := &productlabel.Input{Origin: `<origin>`, DomesticPartyName: `"maker"`, NetQuantity: "1.001", NetUnit: "oz", MinAgeMonths: "217"}
		view := ProductView{Slug: "label", LabelInput: input, Errors: input.Validate(ctx)}
		var body strings.Builder
		if err := productLabel(view).Render(ctx, &body); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`method="post"`, `action="/admin/products/label/label"`, `value="&lt;origin&gt;"`, `value="1.001"`, `value="oz" selected`, `value="217"`, `aria-invalid="true"`, `label-unit-error`, `label-age-error`} {
			if !strings.Contains(body.String(), want) {
				t.Errorf("%s: missing %s", locale, want)
			}
		}
		if strings.Contains(body.String(), " required") {
			t.Error("optional facts became required")
		}
	}
}
