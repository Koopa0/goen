package shoptime_test

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
)

// The no-break spaces keep a date on one line; the plain space after 年 and 月
// and after the English comma is where it may break.
func TestDateTextWritesTheExactBytes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 6, 4, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		day    time.Time
		want   string
	}{
		{"zh, this year", i18n.ZhHant, time.Date(2026, 10, 30, 4, 0, 0, 0, time.UTC), "10 月 30 日"},
		{"zh, other year", i18n.ZhHant, time.Date(2028, 10, 6, 4, 0, 0, 0, time.UTC), "2028 年 10 月 6 日"},
		{"en, this year", i18n.En, time.Date(2026, 10, 30, 4, 0, 0, 0, time.UTC), "Oct 30"},
		{"en, other year", i18n.En, time.Date(2028, 10, 30, 4, 0, 0, 0, time.UTC), "Oct 30, 2028"},
	} {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		if got := shoptime.DateText(ctx, shoptime.DateOf(tt.day, now)); got != tt.want {
			t.Errorf("%s: DateText = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestDateTimeTextAddsTheShopsClock(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 6, 4, 0, 0, 0, time.UTC)
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC) // 18:00 in Taipei
	for locale, want := range map[i18n.Locale]string{
		i18n.ZhHant: "10 月 30 日 18:00",
		i18n.En:     "Oct 30 18:00",
	} {
		if got := shoptime.DateTimeText(i18n.WithLocale(t.Context(), locale), at, now); got != want {
			t.Errorf("DateTimeText(%s) = %q, want %q", locale, got, want)
		}
	}
}
