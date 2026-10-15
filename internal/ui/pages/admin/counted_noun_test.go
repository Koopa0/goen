package admin

import (
	"fmt"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestProductVariantCountSelectsSingularInTheRenderedPage(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, n := range []int32{1, 2} {
			ctx := i18n.WithLocale(t.Context(), locale)
			product := renderComponent(t, ctx, Products(layouts.Page{}, ProductsView{
				Rows: []Product{{Slug: "product", Name: "product", Status: pages.ProductActive, Variants: n, FromCents: 1000}},
			}))
			wantVariants := fmt.Sprintf("%d variants · from ", n)
			if n == 1 {
				wantVariants = "1 variant · from "
			}
			if locale == i18n.ZhHant {
				wantVariants = fmt.Sprintf("%d 個款式 · ", n)
			}
			if !strings.Contains(product, wantVariants) {
				t.Errorf("Products(%s, %d) does not contain %q", locale, n, wantVariants)
			}
		}
	}
}
