package pages

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
