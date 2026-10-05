package pages_test

import (
	"context"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

func TestMembershipMultipliersRetainBasisPointPrecision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		bp   int32
		en   string
		zh   string
	}{
		{name: "100 percent", bp: 10000, en: "1×", zh: "1 倍"},
		{name: "101 percent", bp: 10100, en: "1.01×", zh: "1.01 倍"},
		{name: "125 percent", bp: 12500, en: "1.25×", zh: "1.25 倍"},
		{name: "150 percent", bp: 15000, en: "1.5×", zh: "1.5 倍"},
		{name: "300 percent", bp: 30000, en: "3×", zh: "3 倍"},
		{name: "one basis point above minimum", bp: 10001, en: "1.0001×", zh: "1.0001 倍"},
		{name: "four significant decimal places", bp: 12501, en: "1.2501×", zh: "1.2501 倍"},
		{name: "one basis point below maximum", bp: 29999, en: "2.9999×", zh: "2.9999 倍"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			formats := []struct {
				name   string
				format func(context.Context) string
			}{
				{name: "customer", format: pages.MemberStanding{MultiplierBP: tt.bp}.Multiplier},
				{name: "staff", format: admin.Tier{MultiplierBP: tt.bp}.Multiplier},
			}
			for _, format := range formats {
				for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
					t.Run(format.name+"/"+locale.Tag(), func(t *testing.T) {
						t.Parallel()
						want := tt.en
						if locale == i18n.ZhHant {
							want = tt.zh
						}
						if got := format.format(i18n.WithLocale(t.Context(), locale)); got != want {
							t.Errorf("Multiplier(%d) = %q, want %q", tt.bp, got, want)
						}
					})
				}
			}
		})
	}
}
