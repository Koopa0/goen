package pages

import (
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestOptionFormIsReplacedByTheNoteOnceAProductHasVariants(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		note := i18n.T(ctx, i18n.KeyFormOptionBeforeVariants)
		for _, tt := range []struct {
			name     string
			variants []AdminProductVariant
			wantForm bool
		}{{"no variants", nil, true}, {"one variant", []AdminProductVariant{{SKU: "A"}}, false}} {
			v := AdminProductView{Slug: "p", Variants: tt.variants}
			var b strings.Builder
			if err := adminProductOptions(v).Render(ctx, &b); err != nil {
				t.Fatal(err)
			}
			page := b.String()
			if got := strings.Contains(page, `name="name_en"`) && strings.Contains(page, `id="opt-name"`); got != tt.wantForm {
				t.Errorf("%s %s: add-option form present = %v, want %v", locale, tt.name, got, tt.wantForm)
			}
			if got := strings.Contains(page, "opt-frozen"); got == tt.wantForm {
				t.Errorf("%s %s: note present = %v", locale, tt.name, got)
			}
			if !tt.wantForm && !strings.Contains(page, html.EscapeString(note)) {
				t.Errorf("%s: note text missing", locale)
			}
		}
		if strings.Contains(note, "規格軸") {
			t.Error("staff copy must say 規格項目, not 規格軸")
		}
	}
}
