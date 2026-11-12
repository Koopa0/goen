package catalog

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/policy"
	"github.com/koopa0/goen/internal/ui/widget"
)

// testRules' windows are not the shop's: a page that printed a number of its own instead of the field's would show.
var testRules = policy.Rules{
	FreeDeliveryCents: 300000, LowestFeeCents: 6000, PickupOffered: true,
	RescissionDays: 8, ReturnDays: 15, PayStartMinutes: 28,
}

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

// TestProductBuyBoxNoStockHold holds that a buy box with everything sold out
// states no stock hold.
func TestProductBuyBoxNoStockHold(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		hold := "<dt>" + i18n.T(ctx, i18n.KeyRulePayStart) + "</dt>"
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

// TestProductBuyBoxSoldOutOptions holds that several sold-out options are all
// listed as sold out.
func TestProductBuyBoxSoldOutOptions(t *testing.T) {
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

// TestProductBuyBoxOneSoldOut holds that a sold-out product with nothing to
// choose offers the notify form with no note over it.
func TestProductBuyBoxOneSoldOut(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		v := ProductView{Slug: "buds", Name: "Buds", Rules: testRules, SelectionOK: true, Exact: true, VariantID: "v"}
		box := buyBox(t, locale, &v)
		if strings.Contains(box, `class="goen-pdp__notifyhead"`) {
			t.Errorf("%s: a settled sold-out choice still has a note over the notify form", locale)
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

// TestProductBuyBoxOneColour holds that one colour is a fact, not a fieldset.
func TestProductBuyBoxOneColour(t *testing.T) {
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

// TestProductView_BuyFacts holds that the buy box facts follow what the shop
// states.
func TestProductView_BuyFacts(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tc := range []struct {
		name   string
		view   ProductView
		labels []string
	}{
		{"warranty and hold", ProductView{WarrantyMonths: 24, Rules: testRules, Sellable: true, AnySellable: true, SelectionOK: true},
			[]string{"保固", "付款時限"}},
		{"no warranty", ProductView{Rules: testRules, Sellable: true, AnySellable: true, SelectionOK: true},
			[]string{"付款時限"}},
		{"nothing to hold", ProductView{WarrantyMonths: 12, Rules: testRules, SelectionOK: true},
			[]string{"保固"}},
	} {
		parts := strings.Split(renderIn(t, i18n.ZhHant, widget.StatLine(tc.view.BuyFacts(ctx), widget.StatLinePairs)), "<dt>")[1:]
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

// TestProductView_Highlights holds that the highlights are at most three
// values, and only on a product whose department compares.
func TestProductView_Highlights(t *testing.T) {
	t.Parallel()
	specs := []ProductSpec{{Value: "a"}, {Value: ""}, {Value: "b"}, {Value: "c"}, {Value: "d"}}
	v := ProductView{Specs: specs, Comparable: true}
	if got := strings.Join(v.Highlights(), ","); got != "a,b,c" {
		t.Errorf("Highlights() = %q, want a,b,c", got)
	}
	v = ProductView{Specs: specs}
	if got := v.Highlights(); got != nil {
		t.Errorf("Highlights() of a product that does not compare = %q, want none", got)
	}
}

// TestProductBuyBoxRestockNoOption holds that a restock request refused for
// want of an option marks the unpicked group.
func TestProductBuyBoxRestockNoOption(t *testing.T) {
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

// TestProductBuyBoxRestockStockBack holds that a restock refusal for want of an
// option keeps the address once something is back in stock.
//
// Stock can come back between the page and the post: the refusal still carries the form and the address.
func TestProductBuyBoxRestockStockBack(t *testing.T) {
	t.Parallel()
	v := ProductView{
		Slug: "book", Name: "Book", Rules: testRules, SelectionOK: true, VariantID: "cheapest", AnySellable: true,
		Options:       []ProductOption{capacityOption(false, "")},
		NotifyOutcome: NotifyNoOption, NotifyEmail: "me@example.com",
	}
	if box := buyBox(t, i18n.ZhHant, &v); !strings.Contains(box, `value="me@example.com"`) ||
		!strings.Contains(box, `id="notify-option-error"`) {
		t.Errorf("a refusal for want of a pick on a product with stock dropped the form or its address")
	}
	v.NotifyOutcome, v.NotifyEmail = "", ""
	if box := buyBox(t, i18n.ZhHant, &v); strings.Contains(box, `id="restock"`) {
		t.Errorf("a product with stock and nothing picked offers a notice before any request")
	}
}

// TestProductView_BuyFactsWarranty holds that the warranty term reads in the
// singular for one month.
func TestProductView_BuyFactsWarranty(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale i18n.Locale
		months int32
		want   string
	}{
		{i18n.En, 1, "1 <small>month</small>"},
		{i18n.En, 24, "24 <small>months</small>"},
		{i18n.ZhHant, 24, "24 <small>個月</small>"},
	} {
		v := ProductView{WarrantyMonths: 1, Rules: testRules}
		v.WarrantyMonths = tc.months
		got := renderIn(t, tc.locale, widget.StatLine(v.BuyFacts(i18n.WithLocale(t.Context(), tc.locale)), widget.StatLinePairs))
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s, %d months: %s, want it to contain %q", tc.locale, tc.months, got, tc.want)
		}
	}
}

// TestProductView_AwaitsChoice holds that, until every group is picked, the add
// button stays in the tab order with its reason and cannot post the cheapest
// variant by any key.
func TestProductView_AwaitsChoice(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		v := ProductView{
			Slug: "book", Name: "Book", Rules: testRules, SelectionOK: true, AnySellable: true, VariantID: "cheapest",
			Options: []ProductOption{capacityOption(false, "")},
		}
		box := buyBox(t, locale, &v)
		for _, want := range []string{
			`type="button" aria-describedby="add-to-cart-why" aria-disabled="true" id="add-to-cart"`,
			`id="add-to-cart-why">` + i18n.T(ctx, i18n.KeyChooseOptions) + `</p>`,
		} {
			if !strings.Contains(box, want) {
				t.Errorf("%s: a pending choice lacks %q", locale, want)
			}
		}
		form := markupFrom(t, box, `id="pdp-add"`, "</form>")
		button := markupFrom(t, form, `class="goen-btn goen-btn--primary goen-pdp__add"`, ">")
		if strings.Contains(form, `type="submit"`) || strings.Contains(button, " disabled") {
			t.Errorf("%s: a pending choice can post, or drops the button from the tab order: %s", locale, button)
		}
		v.Options = []ProductOption{capacityOption(false, "32GB/1TB")}
		v.Exact, v.Sellable = true, true
		if box := buyBox(t, locale, &v); strings.Contains(box, "add-to-cart-why") || !strings.Contains(box, `type="submit" id="add-to-cart"`) {
			t.Errorf("%s: a settled choice still waits, or cannot post", locale)
		}
	}
}

// TestProductView_SoldOutWho holds that a sold-out choice is named on its own
// line, the values before the existing words.
func TestProductView_SoldOutWho(t *testing.T) {
	t.Parallel()
	colour := ProductOption{Name: "colour", Label: "顏色", Values: []ProductOptionValue{
		{Value: "walnut", Label: "胡桃", Selected: true, Href: "/p/koto?c=walnut"},
		{Value: "charcoal", Label: "炭黑", Available: true, Href: "/p/koto?c=charcoal"},
	}}
	for _, tc := range []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, `<p class="goen-pdp__status">胡桃<span class="goen-pdp__soldword">已售完</span></p>`},
		{i18n.En, `<p class="goen-pdp__status">胡桃 <span class="goen-pdp__soldword">Sold out</span></p>`},
	} {
		v := ProductView{Slug: "koto", Name: "Koto", Rules: testRules, SelectionOK: true, Exact: true, AnySellable: true, VariantID: "v",
			Options: []ProductOption{colour}}
		box := buyBox(t, tc.locale, &v)
		if !strings.Contains(box, tc.want) {
			t.Errorf("%s: sold-out line missing, want %q", tc.locale, tc.want)
		}
		if strings.Contains(box, `class="goen-pdp__notifyhead"`) || !strings.Contains(box, `id="restock"`) {
			t.Errorf("%s: a settled sold-out choice keeps the note over the form, or lost the form", tc.locale)
		}
		if got := strings.Count(box, i18n.T(i18n.WithLocale(t.Context(), tc.locale), i18n.KeySoldOut)); got != 1 || strings.Contains(box, `id="add-to-cart"`) {
			t.Errorf("%s: says sold out %d times, or keeps a dead add button beside the status line", tc.locale, got)
		}
		v.Sellable = true
		if strings.Contains(buyBox(t, tc.locale, &v), "goen-pdp__status") {
			t.Errorf("%s: a choice in stock says sold out", tc.locale)
		}
	}
}

// TestProductView_FirstInvalidOption holds that a refusal for want of a pick
// frames the first unpicked group, puts its reason under it and focuses its
// first choice, and that only that group does.
func TestProductView_FirstInvalidOption(t *testing.T) {
	t.Parallel()
	colour := colourOption("石墨黑", "星霧藍")
	colour.Values[0].Selected = false
	v := ProductView{
		Slug: "book", Name: "Book", Rules: testRules, SelectionOK: true, VariantID: "cheapest",
		Options:       []ProductOption{capacityOption(true, "32GB/1TB"), colour, capacityOption(true, "")},
		NotifyOutcome: NotifyNoOption,
	}
	if got := v.FirstInvalidOption(); got != 1 {
		t.Fatalf("FirstInvalidOption() = %d, want 1", got)
	}
	box := buyBox(t, i18n.ZhHant, &v)
	if got := strings.Count(box, "autofocus"); got != 1 {
		t.Errorf("%d controls take focus, want 1", got)
	}
	focus, _, _ := strings.Cut(box, "autofocus")
	if at := strings.LastIndex(focus, "<a "); at < 0 || !strings.Contains(focus[at:], `id="swatch-1-0"`) {
		t.Errorf("focus is not on the first choice of the first unpicked group: %s", focus[max(at, 0):])
	}
	group := markupFrom(t, box, `id="swatch-1-0"`, "</fieldset>")
	if !strings.Contains(group, `id="notify-option-error"`) {
		t.Error("the reason is not under the group it refuses")
	}
	if got := strings.Count(box, `id="notify-option-error"`); got != 1 {
		t.Errorf("the reason is written %d times, want once", got)
	}
	v.NotifyOutcome = ""
	if got := v.FirstInvalidOption(); got != -1 {
		t.Errorf("FirstInvalidOption() with no refusal = %d, want -1", got)
	}
}

// TestProductView_ShippingFacts holds that the shipping section states the
// two return rules apart and then free delivery, each with its note.
func TestProductView_ShippingFacts(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	v := ProductView{Rules: testRules}
	got := renderIn(t, i18n.ZhHant, widget.StatLine(v.ShippingFacts(ctx), widget.StatLinePairs))
	order := []string{
		"<dt>猶豫期</dt>", "8\u00a0<small>天</small>", "收到次日起算",
		"<dt>未使用退貨</dt>", "15\u00a0<small>天</small>", "收到次日起算；包裝配件齊全",
		"<dt>免運門檻</dt>", "NT$</small>3,000", "NT$60 起",
	}
	at := 0
	for _, want := range order {
		i := strings.Index(got[at:], want)
		if i < 0 {
			t.Fatalf("ShippingFacts() lacks %q after byte %d: %s", want, at, got)
		}
		at += i + len(want)
	}
}

// TestProductRatingRow holds that the rating row reads as one star, the
// average and the count in words, and that a screen reader hears the sentence.
func TestProductRatingRow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale       i18n.Locale
		shown, heard string
	}{
		{i18n.ZhHant, `4.5<span class="goen-sep">·</span>2 則評價`, "評分 4.5 分，共 2 則評價"},
		{i18n.En, `4.5<span class="goen-sep">·</span>2 reviews`, "Rated 4.5 out of 5, from 2 reviews"},
	} {
		v := ProductView{Slug: "book", Name: "Book", Rules: testRules, Rating: 4.5, RatingCount: 2}
		box := buyBox(t, tc.locale, &v)
		row := markupFrom(t, box, `class="goen-pdp__rating"`, "</p>")
		for _, want := range []string{`<span aria-hidden="true">` + tc.shown + `</span>`, `<span class="goen-sr-only">` + tc.heard + `</span>`, `href="#reviews"`} {
			if !strings.Contains(row, want) {
				t.Errorf("%s: rating row lacks %q: %s", tc.locale, want, row)
			}
		}
		if got := strings.Count(row, "<svg"); got != 1 {
			t.Errorf("%s: rating row draws %d stars, want 1", tc.locale, got)
		}
	}
}

// markupFrom is the markup from start up to, not including, the first end after it.
func markupFrom(t *testing.T, markup, start, end string) string {
	t.Helper()
	_, after, ok := strings.Cut(markup, start)
	if !ok {
		t.Fatalf("no %q in the markup", start)
	}
	before, _, ok := strings.Cut(after, end)
	if !ok {
		t.Fatalf("no %q after %q", end, start)
	}
	return start + before
}
