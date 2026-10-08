package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestWarrantyTermIsStatedInMonths(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale i18n.Locale
		months int
		want   string
	}{
		{i18n.ZhHant, 12, "12 個月"},
		{i18n.ZhHant, 1, "1 個月"},
		{i18n.En, 12, "12 months"},
		{i18n.En, 24, "24 months"},
		{i18n.En, 1, "1 month"},
	} {
		ctx := i18n.WithLocale(t.Context(), tc.locale)
		if got := (&CompareProduct{WarrantyMonths: tc.months}).Warranty(ctx); got != tc.want {
			t.Errorf("%v: compare page, %d months = %q, want %q", tc.locale, tc.months, got, tc.want)
		}
		if got := (WarrantyLine{HasTerm: true, Months: tc.months}).TermText(ctx); got != tc.want {
			t.Errorf("%v: warranty page, %d months = %q, want %q", tc.locale, tc.months, got, tc.want)
		}
	}
}

func TestAShippedEventHidesItsNote(t *testing.T) {
	t.Parallel()
	for kind, want := range map[order.EventKind]bool{
		order.EventShipped:   false,
		order.EventCancelled: true,
	} {
		if got := (OrderEvent{Kind: kind, Note: "黑貓宅急便 T1"}).ShowsNote(); got != want {
			t.Errorf("%s with a note: ShowsNote() = %v, want %v", kind, got, want)
		}
	}
	if (OrderEvent{Kind: order.EventCancelled}).ShowsNote() {
		t.Error("an event with no note shows one")
	}
}

func TestThePointsRuleNamesTheStepInPoints(t *testing.T) {
	t.Parallel()
	for loc, want := range map[i18n.Locale]string{
		i18n.ZhHant: "最少 20 點，每次以 10 點為單位兌換",
		i18n.En:     "At least 20 points, in whole multiples of 10 points.",
	} {
		ctx := i18n.WithLocale(t.Context(), loc)
		var b strings.Builder
		if err := Points(layouts.Page{Title: "p"}, PointsView{PerCredit: 10, Minimum: 20, Redeemable: 40}).Render(ctx, &b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), want) {
			t.Errorf("%v: the points rule does not read %q", loc, want)
		}
	}
}

func TestThePolicyFooterReadsAsOneSentence(t *testing.T) {
	t.Parallel()
	for loc, want := range map[i18n.Locale]string{
		i18n.ZhHant: `還有問題？<a href="/contact">聯絡我們</a>，或看看<a href="/faq">常見問題</a>。`,
		i18n.En:     `Still have a question? <a href="/contact">Get in touch</a>, or read the <a href="/faq">FAQ</a>.`,
	} {
		ctx := i18n.WithLocale(t.Context(), loc)
		var b strings.Builder
		if err := Policy(layouts.Page{Title: "p"}, PolicyDoc{}).Render(ctx, &b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), want) {
			t.Errorf("%v: the policy footer does not read %q", loc, want)
		}
	}
}
