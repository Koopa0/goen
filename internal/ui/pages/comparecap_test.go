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

// A description the shop wrote only in Chinese is Chinese prose on an English
// page, and is marked so (WCAG 3.1.2); a translated one, or a page already in
// Chinese, carries no lang of its own.
func TestAnUntranslatedDescriptionIsMarkedChinese(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name         string
		locale       i18n.Locale
		untranslated bool
		want         bool
	}{
		{"english page, no English description", i18n.En, true, true},
		{"english page, translated", i18n.En, false, false},
		{"chinese page", i18n.ZhHant, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := ProductView{
				Name: "Pixelight 9 Pro", Slug: "pixelight-9-pro", Description: "說明文字",
				DescriptionUntranslated: tt.untranslated,
			}
			out := renderProductInLocale(t, i18n.WithLocale(t.Context(), tt.locale), &v)
			got := strings.Contains(out, `<p class="goen-pdp__prose" lang="zh-Hant">說明文字</p>`)
			if got != tt.want {
				t.Errorf("prose marked zh-Hant = %v, want %v", got, tt.want)
			}
			if !tt.want && !strings.Contains(out, `<p class="goen-pdp__prose">說明文字</p>`) {
				t.Error("the prose is missing or carries an unexpected attribute")
			}
		})
	}
}
