package catalog

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// The seed's product names already carry the brand, so a title that adds it
// again reads "棲木 石器馬克杯 — 棲木 Restwood · goen".
func TestAProductTitleIsItsNameAndTheShop(t *testing.T) {
	t.Parallel()
	if got := ProductMeta(&ProductView{Name: "棲木 石器馬克杯", Brand: "棲木 Restwood"}).Title; got != "棲木 石器馬克杯" {
		t.Errorf("product page title is %q, want the product name alone", got)
	}
}

func TestAShopAnswerKeepsASpaceBeforeItsDate(t *testing.T) {
	t.Parallel()
	v := &ProductView{Name: "Phone", Brand: "Pixelight", Questions: []Question{{
		Asker: "王小明", Body: "有保固嗎？", Asked: "2026-10-01",
		Answers: []Answer{{Author: "陳大文", IsStaff: true, Body: "有。", At: "2026-10-02"}},
	}}}
	html := renderIn(t, i18n.ZhHant, Product(ProductMeta(v), v))
	if !strings.Contains(html, "官方回覆</span> · 2026-10-02") {
		t.Error("the shop's answer runs its badge into the date")
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
