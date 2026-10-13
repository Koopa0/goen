package admin

import (
	"fmt"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/user"
)

func TestProductVariantAndStaffCountsSelectSingularInTheRenderedPage(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, n := range []int32{1, 2} {
			ctx := i18n.WithLocale(t.Context(), locale)
			product := renderComponent(t, ctx, Products(layouts.Page{}, ProductsView{
				Rows: []Product{{Slug: "product", Name: "product", Status: pages.ProductActive, Variants: n, FromCents: 1000}},
			}))
			wantVariants := fmt.Sprintf("%d variants · from ", n)
			wantStaff := fmt.Sprintf("%d accounts have not enrolled", n)
			if n == 1 {
				wantVariants = "1 variant · from "
				wantStaff = "1 account has not enrolled"
			}
			if locale == i18n.ZhHant {
				wantVariants = fmt.Sprintf("%d 個規格 · ", n)
				wantStaff = fmt.Sprintf("還有 %d 個帳號尚未設定", n)
			}
			if !strings.Contains(product, wantVariants) {
				t.Errorf("Products(%s, %d) does not contain %q", locale, n, wantVariants)
			}
			rows := make([]StaffRow, n)
			for i := range rows {
				rows[i] = StaffRow{ID: fmt.Sprintf("staff-%d", i), Email: "staff@example.com", Role: user.RoleStaff}
			}
			staff := renderComponent(t, ctx, Staff(layouts.Page{}, StaffView{Rows: rows}))
			if !strings.Contains(staff, wantStaff) {
				t.Errorf("Staff(%s, %d) does not contain %q", locale, n, wantStaff)
			}
		}
	}
}
