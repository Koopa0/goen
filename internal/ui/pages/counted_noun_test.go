package pages

import (
	"fmt"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestPointsCountsSelectSingularAtTheirConsumers(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, n := range []int64{1, 2} {
			ctx := i18n.WithLocale(t.Context(), locale)
			v := PointsView{Balance: n, Redeemable: n, PerCredit: n, Minimum: n, ExpiringPoints: n, ExpiringOn: "2030-01-01"}
			unit, expiryVerb := "points", "expire"
			if n == 1 {
				unit, expiryVerb = "point", "expires"
			}
			using := fmt.Sprintf("Using %d %s", n, unit)
			rule := fmt.Sprintf("At least %d %s, in whole multiples of %d %s.", n, unit, n, unit)
			rate := fmt.Sprintf("%d %s = NT$1", n, unit)
			expiring := fmt.Sprintf("%d %s %s on 2030-01-01", n, unit, expiryVerb)
			step := fmt.Sprintf("%d %s", n, unit)
			detail := fmt.Sprintf("Requested %d %s; reversed %d; shortfall 0", n, unit, n)
			if locale == i18n.ZhHant {
				using = fmt.Sprintf("用 %d 點", n)
				rule = fmt.Sprintf("最少 %d 點，每次以 %d 點為單位兌換", n, n)
				rate = fmt.Sprintf("%d 點 = NT$1", n)
				expiring = fmt.Sprintf("%d 點會在 2030-01-01 到期", n)
				step = fmt.Sprintf("%d 點", n)
				detail = fmt.Sprintf("應扣回 %d 點；實際扣回 %d 點；未扣回 0 點", n, n)
			}
			body := renderComponent(t, ctx, Points(layouts.Page{}, v))
			for _, want := range []string{">" + using + "</span>", rule, expiring} {
				if !strings.Contains(body, want) {
					t.Errorf("Points(%s, %d) does not contain %q", locale, n, want)
				}
			}
			if got := v.RateText(ctx); got != rate {
				t.Errorf("RateText(%s, %d) = %q, want %q", locale, n, got, rate)
			}
			if got := v.StepAmountText(ctx); got != step {
				t.Errorf("StepAmountText(%s, %d) = %q, want %q", locale, n, got, step)
			}
			entry := PointsEntry{Kind: PointsClawedBack, RequestedPoints: n, Points: -n}
			if got := entry.Detail(ctx); got != detail {
				t.Errorf("Detail(%s, %d) = %q, want %q", locale, n, got, detail)
			}
		}
	}
}

func TestColourCountSelectsSingularInItsRenderedText(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, n := range []int{0, 1, 2} {
			ctx := i18n.WithLocale(t.Context(), locale)
			v := ProductTile{Colours: make([]string, n)}
			want := fmt.Sprintf("%d colours", n)
			if n == 1 {
				want = "1 colour"
			}
			if locale == i18n.ZhHant {
				want = fmt.Sprintf("%d 種顏色", n)
			}
			body := renderComponent(t, ctx, tileColours(v))
			if !strings.Contains(body, ">"+want+"</span>") {
				t.Errorf("colour text (%s, %d) does not contain %q", locale, n, want)
			}
		}
	}
}
