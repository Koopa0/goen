package pages

import (
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// The cap is written once, in MaxCompare; every sentence that names it is
// checked in both languages.
func TestTheCompareCapIsSaidFromMaxCompareInBothLanguages(t *testing.T) {
	t.Parallel()

	most := strconv.Itoa(MaxCompare)
	for _, tt := range []struct {
		locale i18n.Locale
		hint   string
		limit  string
	}{
		{i18n.ZhHant, `<a href="/c/tech">商品列表</a>勾選商品上的「比較」，最多 ` + most + ` 件。`, "最多比較 " + most + " 件"},
		{i18n.En, `Tick Compare on products in the <a href="/c/tech">product list</a>, up to ` + most + `.`, "Up to " + most + " at a time"},
	} {
		t.Run(string(tt.locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)

			empty := renderComponent(t, ctx, Compare(layouts.Page{Title: "Compare"}, CompareView{StartSlug: "tech"}))
			if !strings.Contains(empty, tt.hint) {
				t.Errorf("the empty comparison in %s does not read %q", tt.locale, tt.hint)
			}

			form := renderComponent(t, ctx, compareForm(true))
			if !strings.Contains(form, tt.limit) {
				t.Errorf("the compare bar in %s does not read %q", tt.locale, tt.limit)
			}
		})
	}
}
