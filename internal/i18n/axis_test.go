package i18n_test

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestAxisUnitAbbreviatesFromTenThousand(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		locale      i18n.Locale
		top         int64
		wantDivisor int64
		wantSuffix  string
	}{
		{i18n.ZhHant, 9_999, 1, ""},
		{i18n.ZhHant, 10_000, 10_000, "萬"},
		{i18n.ZhHant, 99_999_999, 10_000, "萬"},
		{i18n.ZhHant, 100_000_000, 100_000_000, "億"},
		{i18n.En, 9_999, 1, ""},
		{i18n.En, 10_000, 1_000, "K"},
		{i18n.En, 999_999, 1_000, "K"},
		{i18n.En, 1_000_000, 1_000_000, "M"},
	} {
		divisor, suffix := i18n.AxisUnit(i18n.WithLocale(t.Context(), tc.locale), tc.top)
		if divisor != tc.wantDivisor || suffix != tc.wantSuffix {
			t.Errorf("AxisUnit(%s, %d) = %d, %q, want %d, %q", tc.locale, tc.top, divisor, suffix, tc.wantDivisor, tc.wantSuffix)
		}
	}
}
