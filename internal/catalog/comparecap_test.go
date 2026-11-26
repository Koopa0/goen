package catalog

import (
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestCompareCapWording holds that the compare cap is said from MaxCompare in
// both languages.
//
// The cap is written once, in MaxCompare; every sentence that names it is
// checked in both languages.
func TestCompareCapWording(t *testing.T) {
	t.Parallel()

	most := strconv.Itoa(MaxCompare)
	for _, tt := range []struct {
		locale i18n.Locale
		hint   string
		full   string
	}{
		{i18n.ZhHant, `<a href="/c/tech">商品列表</a>按下商品上的「比較」，最多 ` + most + ` 件。`, "比較已滿 " + most + " 個"},
		{i18n.En, `Press Compare on products in the <a href="/c/tech">product list</a>, up to ` + most + `.`, "The comparison holds " + most + " products."},
	} {
		t.Run(string(tt.locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)

			empty := renderComponent(t, ctx, Compare(frame.Page{Title: "Compare"}, CompareView{StartSlug: "tech"}))
			if !strings.Contains(empty, tt.hint) {
				t.Errorf("the empty comparison in %s does not read %q", tt.locale, tt.hint)
			}

			refused := withCompareRefused(frame.WithCompareCount(ctx, MaxCompare))
			if toggle := renderComponent(t, refused, CompareToggle("a", "A")); !strings.Contains(toggle, tt.full) {
				t.Errorf("the refused toggle in %s does not read %q", tt.locale, tt.full)
			}
		})
	}
}
