package policy

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
)

func renderIn(t *testing.T, locale i18n.Locale, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), locale), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// testRules' windows are not the shop's: a page that printed a number of its own instead of the field's would show.
var testRules = Rules{
	FreeDeliveryCents: 300000, LowestFeeCents: 6000, PickupOffered: true,
	RescissionDays: 8, ReturnDays: 15, HoldMinutes: 61, PayStartMinutes: 28,
}

// TestRulesSectionStoredRules: the shop rules section states the stored rules
// in both languages. The four rules are read from where goen keeps them, in
// both languages; the free-delivery threshold is the shop's own figure, and a
// shop whose methods never turn free states none.
func TestRulesSectionStoredRules(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		page := renderIn(t, locale, RulesSection(testRules))
		for _, want := range []string{
			`<dl class="ui-statline ui-statline--wide">`,
			strconv.FormatInt(testRules.HoldMinutes, 10) + " <small>",
			strconv.FormatInt(testRules.RescissionDays, 10) + " <small>",
			strconv.FormatInt(testRules.ReturnDays, 10) + " <small>",
			fmt.Sprintf(i18n.T(i18n.WithLocale(t.Context(), locale), i18n.KeyRuleHoldNote),
				strconv.FormatInt(testRules.PayStartMinutes, 10)),
			`<small class="ui-statline__pre">NT$</small>3,000`,
			"NT$60",
			i18n.T(i18n.WithLocale(t.Context(), locale), i18n.KeySectionRules),
		} {
			if !strings.Contains(page, want) {
				t.Errorf("%s rules section lacks %q", locale, want)
			}
		}
		if n := strings.Count(page, "<dt>"); n != 4 {
			t.Errorf("%s rules section states %d rules, want 4", locale, n)
		}
		if strings.Contains(page, "style=") {
			t.Errorf("%s rules section carries a style attribute", locale)
		}
	}
}

// TestRulesSectionNoFreeDelivery: the shop rules omit free delivery where no
// method turns free.
func TestRulesSectionNoFreeDelivery(t *testing.T) {
	t.Parallel()
	page := renderIn(t, i18n.ZhHant, RulesSection(Rules{}))
	if n := strings.Count(page, "<dt>"); n != 3 {
		t.Errorf("a rules section with no free-delivery threshold states %d rules, want 3", n)
	}
	if strings.Contains(page, "免運門檻") {
		t.Error("the rules section states a free-delivery threshold the shop does not have")
	}
}

// TestRules_StatsPickupNote: the shop rules name pickup only where it is
// offered.
func TestRules_StatsPickupNote(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		pickup := map[i18n.Locale]string{i18n.ZhHant: "超商取貨", i18n.En: "pickup"}[locale]
		with := Rules{FreeDeliveryCents: 300000, PickupOffered: true}.Stats(ctx)[3].Note
		without := Rules{FreeDeliveryCents: 300000}.Stats(ctx)[3].Note
		if !strings.Contains(with, pickup) {
			t.Errorf("%s: the note with pickup does not name pickup: %q", locale, with)
		}
		if strings.Contains(without, pickup) {
			t.Errorf("%s: the note without pickup names pickup: %q", locale, without)
		}
	}
}
