package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestTheProductsPageSaysHowManyAreSold(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		loc  i18n.Locale
		n    int64
		want string
	}{
		{i18n.ZhHant, 9, "上架中 9 項商品"},
		{i18n.En, 9, "9 products published"},
		{i18n.En, 1, "1 product published"},
		{i18n.En, 0, "0 products published"},
	} {
		ctx := i18n.WithLocale(t.Context(), tc.loc)
		if html := renderComponent(t, ctx, Products(Meta(ctx), ProductsView{Published: tc.n})); !strings.Contains(html, tc.want) {
			t.Errorf("Products(Published=%d) in %v does not say %q", tc.n, tc.loc, tc.want)
		}
	}
}
