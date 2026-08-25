package pages

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestAClawbackSaysWhatWasRequestedAndWhatWasShort(t *testing.T) {
	entry := PointsEntry{
		Kind: "clawback", Points: -40, RequestedPoints: 100, ShortfallPoints: 60,
	}

	for _, tc := range []struct {
		locale i18n.Locale
		what   string
		detail string
	}{
		{i18n.ZhHant, "退貨扣回", "應扣回 100 點；實際扣回 40 點；未扣回 60 點"},
		{i18n.En, "Reversed for a return", "Requested 100 points; reversed 40; shortfall 60"},
	} {
		t.Run(tc.locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), tc.locale)
			if got := entry.What(ctx); got != tc.what {
				t.Errorf("What() = %q, want %q", got, tc.what)
			}
			if got := entry.Detail(ctx); got != tc.detail {
				t.Errorf("Detail() = %q, want %q", got, tc.detail)
			}
		})
	}

	whollyConsumed := PointsEntry{
		Kind: "clawback", Points: 0, RequestedPoints: 75, ShortfallPoints: 75,
	}
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	if got := whollyConsumed.Detail(ctx); got != "Requested 75 points; reversed 0; shortfall 75" {
		t.Errorf("zero-point clawback detail = %q", got)
	}
}
