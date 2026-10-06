package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
)

var testRules = ShopRules{FreeDeliveryCents: 300000, LowestFeeCents: 6000, PickupOffered: true}

func capacityOption(sold bool, selected string) ProductOption {
	opt := ProductOption{Name: "capacity", Label: "容量"}
	for _, v := range []string{"32GB/1TB", "64GB/2TB"} {
		opt.Values = append(opt.Values, ProductOptionValue{
			Value: v, Label: v, Selected: v == selected, Available: !sold, Href: "/p/book?capacity=" + v,
		})
	}
	return opt
}

func colourOption(labels ...string) ProductOption {
	opt := ProductOption{Name: "colour", Label: "顏色"}
	for i, l := range labels {
		opt.Values = append(opt.Values, ProductOptionValue{Value: l, Label: l, Selected: i == 0, Available: true, Href: "/p/book?colour=" + l})
	}
	return opt
}

func buyBox(t *testing.T, locale i18n.Locale, v *ProductView) string {
	t.Helper()
	page := renderIn(t, locale, Product(ProductMeta(v), v))
	i := strings.Index(page, `id="buybox"`)
	if i < 0 {
		t.Fatal("no buy box")
	}
	return page[i:]
}

func TestAllSoldOutBuyBoxHasNoStockHold(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		hold := "<dt>" + i18n.T(ctx, i18n.KeyRuleHold) + "</dt>"
		selling := ProductView{Slug: "book", Name: "Book", Rules: testRules, SelectionOK: true, Exact: true, Sellable: true, AnySellable: true, VariantID: "v"}
		if !strings.Contains(buyBox(t, locale, &selling), hold) {
			t.Errorf("%s: a product in stock states no stock hold", locale)
		}
		for name, out := range map[string]ProductView{
			"all sold out":        {Slug: "book", Name: "Book", Rules: testRules, SelectionOK: true, Exact: true, VariantID: "v"},
			"combination is gone": {Slug: "book", Name: "Book", Rules: testRules},
		} {
			if strings.Contains(buyBox(t, locale, &out), hold) {
				t.Errorf("%s, %s: no stock to hold, yet the box states a hold", locale, name)
			}
		}
	}
}

func TestSeveralSoldOutOptionsAreAllListedAsSoldOut(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		v := ProductView{Slug: "book", Name: "Book", Rules: testRules, SelectionOK: true, VariantID: "v", Options: []ProductOption{capacityOption(true, "")}}
		box := buyBox(t, locale, &v)
		for _, val := range v.Options[0].Values {
			if !strings.Contains(box, `href="`+val.Href+`"`) {
				t.Errorf("%s: option %q is not listed", locale, val.Label)
			}
		}
		if got := strings.Count(box, ">"+i18n.T(ctx, i18n.KeySoldOut)+"</small>"); got != len(v.Options[0].Values) {
			t.Errorf("%s: %d options say sold out, want %d", locale, got, len(v.Options[0].Values))
		}
		if !strings.Contains(box, i18n.T(ctx, i18n.KeyRestockPick)) {
			t.Errorf("%s: no note asking for a pick", locale)
		}
		if !strings.Contains(box, `id="restock"`) || !strings.Contains(box, `name="variant" value=""`) {
			t.Errorf("%s: before a pick the notify form is missing or names a variant", locale)
		}
	}
}

func TestOneSoldOutOptionHasOnlyTheOneLineNote(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		v := ProductView{Slug: "buds", Name: "Buds", Rules: testRules, SelectionOK: true, Exact: true, VariantID: "v"}
		box := buyBox(t, locale, &v)
		if !strings.Contains(box, ">"+i18n.T(ctx, i18n.KeyRestockNote)+"<") {
			t.Errorf("%s: no one-line note", locale)
		}
		if strings.Contains(box, i18n.T(ctx, i18n.KeyRestockPick)) {
			t.Errorf("%s: a single option asks for a pick", locale)
		}
		if strings.Contains(box, "<fieldset") {
			t.Errorf("%s: a single option has a chooser", locale)
		}
		if !strings.Contains(box, `id="restock"`) {
			t.Errorf("%s: no notify form", locale)
		}
	}
}

func TestOneColourIsAFactNotAFieldset(t *testing.T) {
	t.Parallel()
	v := ProductView{
		Slug: "book", Name: "Book", Rules: testRules, SelectionOK: true, Exact: true, Sellable: true, AnySellable: true, VariantID: "v",
		Options: []ProductOption{colourOption("石墨黑")},
	}
	box := buyBox(t, i18n.ZhHant, &v)
	if strings.Contains(box, "<fieldset") {
		t.Error("one colour has a fieldset")
	}
	if !strings.Contains(box, `顏色：<span class="goen-pdp__optchosen">石墨黑</span>`) {
		t.Error("one colour is not stated in one line")
	}

	v.Options = []ProductOption{colourOption("石墨黑", "星霧藍")}
	if box := buyBox(t, i18n.ZhHant, &v); strings.Count(box, "<fieldset") != 1 || !strings.Contains(box, "<legend") {
		t.Error("two colours lack a fieldset with a legend")
	}
}

func TestBuyBoxFactsFollowWhatTheShopStates(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tc := range []struct {
		name   string
		view   ProductView
		labels []string
	}{
		{"all four", ProductView{WarrantyMonths: 24, Rules: testRules, Sellable: true, AnySellable: true, SelectionOK: true},
			[]string{"保固", "猶豫期", "庫存保留", "免運門檻"}},
		{"no warranty", ProductView{Rules: testRules, Sellable: true, AnySellable: true, SelectionOK: true},
			[]string{"猶豫期", "庫存保留", "免運門檻"}},
		{"no free delivery", ProductView{WarrantyMonths: 12, Sellable: true, AnySellable: true, SelectionOK: true},
			[]string{"保固", "猶豫期", "庫存保留"}},
	} {
		parts := strings.Split(renderIn(t, i18n.ZhHant, components.StatLine(tc.view.BuyFacts(ctx), components.StatLinePairs)), "<dt>")[1:]
		got := make([]string, 0, len(parts))
		for _, part := range parts {
			label, _, ok := strings.Cut(part, "</dt>")
			if !ok {
				t.Fatalf("%s: a term is not closed: %q", tc.name, part)
			}
			got = append(got, label)
		}
		if strings.Join(got, ",") != strings.Join(tc.labels, ",") {
			t.Errorf("%s: facts = %v, want %v", tc.name, got, tc.labels)
		}
	}
}

func TestHighlightsAreAtMostThreeValues(t *testing.T) {
	t.Parallel()
	v := ProductView{Specs: []ProductSpec{{Value: "a"}, {Value: ""}, {Value: "b"}, {Value: "c"}, {Value: "d"}}}
	if got := strings.Join(v.Highlights(), ","); got != "a,b,c" {
		t.Errorf("Highlights() = %q, want a,b,c", got)
	}
}

func TestRefusedRestockWithNoOptionMarksTheUnpickedGroup(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		v := ProductView{
			Slug: "book", Name: "Book", Rules: testRules, SelectionOK: true, VariantID: "cheapest",
			Options:       []ProductOption{capacityOption(true, "")},
			NotifyOutcome: NotifyNoOption, NotifyEmail: "me@example.com",
		}
		box := buyBox(t, locale, &v)
		for _, want := range []string{
			`aria-invalid="true"`,
			`aria-describedby="notify-option-error"`,
			`id="notify-option-error">` + i18n.T(ctx, i18n.KeyRestockNoOption),
			`value="me@example.com"`,
			`name="variant" value=""`,
		} {
			if !strings.Contains(box, want) {
				t.Errorf("%s: refused restock lacks %q", locale, want)
			}
		}
		v.NotifyOutcome = ""
		if box := buyBox(t, locale, &v); strings.Contains(box, "aria-invalid") || !strings.Contains(box, `id="restock"`) {
			t.Errorf("%s: an unrefused page marks a group invalid or has no form to refuse", locale)
		}
	}
}

func TestWarrantyTermReadsInTheSingularForOneMonth(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale i18n.Locale
		months int32
		want   string
	}{
		{i18n.En, 1, "1&nbsp;<small>month</small>"},
		{i18n.En, 24, "24&nbsp;<small>months</small>"},
		{i18n.ZhHant, 24, "24&nbsp;<small>個月</small>"},
	} {
		v := ProductView{WarrantyMonths: 1, Rules: testRules}
		v.WarrantyMonths = tc.months
		got := renderIn(t, tc.locale, components.StatLine(v.BuyFacts(i18n.WithLocale(t.Context(), tc.locale)), components.StatLinePairs))
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s, %d months: %s, want it to contain %q", tc.locale, tc.months, got, tc.want)
		}
	}
}
