package pages

import (
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// The four rules are read from where goen keeps them, in both languages; the
// free-delivery threshold is the shop's own figure, and a shop whose methods
// never turn free states none.
func TestShopRulesStateTheStoredRules(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		page := renderIn(t, locale, Home(layouts.Page{}, HomeView{
			Rules: ShopRules{FreeDeliveryCents: 300000, LowestFeeCents: 6000, PickupOffered: true},
		}))
		for _, want := range []string{
			`<dl class="ui-statline ui-statline--wide">`,
			strconv.Itoa(holdMinutes) + " <small>",
			strconv.Itoa(rescissionDays) + " <small>",
			strconv.Itoa(returnDays) + " <small>",
			`<small class="ui-statline__pre">NT$</small>3,000`,
			"NT$60",
			i18n.T(i18n.WithLocale(t.Context(), locale), i18n.KeySectionRules),
		} {
			if !strings.Contains(page, want) {
				t.Errorf("%s home lacks %q", locale, want)
			}
		}
		if n := strings.Count(page, "<dt>"); n != 4 {
			t.Errorf("%s home states %d rules, want 4", locale, n)
		}
		if strings.Contains(page, "style=") {
			t.Errorf("%s home carries a style attribute", locale)
		}
	}
}

func TestShopRulesOmitFreeDeliveryWhereNoMethodTurnsFree(t *testing.T) {
	t.Parallel()
	page := renderIn(t, i18n.ZhHant, Home(layouts.Page{}, HomeView{Rules: ShopRules{}}))
	if n := strings.Count(page, "<dt>"); n != 3 {
		t.Errorf("home with no free-delivery threshold states %d rules, want 3", n)
	}
	if strings.Contains(page, "免運門檻") {
		t.Error("home states a free-delivery threshold the shop does not have")
	}
}

func TestShopRulesNamePickupOnlyWhereItIsOffered(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		pickup := map[i18n.Locale]string{i18n.ZhHant: "超商取貨", i18n.En: "pickup"}[locale]
		with := ShopRules{FreeDeliveryCents: 300000, PickupOffered: true}.Stats(ctx)[3].Note
		without := ShopRules{FreeDeliveryCents: 300000}.Stats(ctx)[3].Note
		if !strings.Contains(with, pickup) {
			t.Errorf("%s: the note with pickup does not name pickup: %q", locale, with)
		}
		if strings.Contains(without, pickup) {
			t.Errorf("%s: the note without pickup names pickup: %q", locale, without)
		}
	}
}

func TestTheDepartmentPageEndsWithTheShopRules(t *testing.T) {
	t.Parallel()
	rules := ShopRules{FreeDeliveryCents: 300000}
	page := renderIn(t, i18n.ZhHant, Listing(layouts.Page{}, ListingView{Slug: "tech", Name: "3C 數位"}, &rules, nil))
	if !strings.Contains(page, `<dl class="ui-statline ui-statline--wide">`) {
		t.Error("department page lacks the shop rules")
	}
	bare := renderIn(t, i18n.ZhHant, Listing(layouts.Page{}, ListingView{Slug: "tech", Name: "3C 數位"}, nil, nil))
	if strings.Contains(bare, "ui-statline") {
		t.Error("a listing given no rules prints a stat line")
	}
}
