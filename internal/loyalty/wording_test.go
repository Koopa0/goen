package loyalty

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestPointsRuleStepInPoints: the points rule names the step in points.
func TestPointsRuleStepInPoints(t *testing.T) {
	t.Parallel()
	for loc, want := range map[i18n.Locale]string{
		i18n.ZhHant: "最少 20 點，每次以 10 點為單位兌換",
		i18n.En:     "At least 20 points, in whole multiples of 10 points.",
	} {
		ctx := i18n.WithLocale(t.Context(), loc)
		var b strings.Builder
		if err := Points(frame.Page{Title: "p"}, PointsView{PerCredit: 10, Minimum: 20, Redeemable: 40}).Render(ctx, &b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), want) {
			t.Errorf("%v: the points rule does not read %q", loc, want)
		}
	}
}
