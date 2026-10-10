package productlabel

import (
	"fmt"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestMinimumAgeCountsMonthsInBothLanguages(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, n := range []int16{1, 2} {
			rows := (&Facts{MinAgeMonths: &n}).Rows(i18n.WithLocale(t.Context(), locale))
			want := fmt.Sprintf("%d months and over", n)
			if n == 1 {
				want = "1 month and over"
			}
			if locale == i18n.ZhHant {
				want = fmt.Sprintf("%d 個月以上", n)
			}
			if len(rows) != 1 || rows[0].Value != want {
				t.Errorf("minimum age (%s, %d) = %+v, want %q", locale, n, rows, want)
			}
		}
	}
}
