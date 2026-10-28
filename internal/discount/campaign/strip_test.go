package campaign

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
)

// TestStripSaysEachPeriod holds that every campaign the offers strip lists
// says its period in the shared phrase, in both languages, and that the dot
// between the product count and the phrase stays with the count.
func TestStripSaysEachPeriod(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, now.Location()) }
	for _, tt := range []struct {
		locale i18n.Locale
		want   []string
	}{
		{i18n.ZhHant, []string{"至", "10\u00a0月 31\u00a0日", "剩\u00a022\u00a0天", "明天結束"}},
		{i18n.En, []string{"until", "Oct\u00a031", "22\u00a0days left", "ends tomorrow"}},
	} {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		var b strings.Builder
		if err := strip([]Summary{
			{Slug: "far", Title: "秋日選物", Products: 6, End: NewEnd(day(32), now)},
			{Slug: "near", Title: "茶與咖啡週", Products: 4, End: NewEnd(day(11), now)},
		}).Render(ctx, &b); err != nil {
			t.Fatal(err)
		}
		got := b.String()
		for _, want := range tt.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: the offers strip lacks %q:\n%s", tt.locale, want, got)
			}
		}
		if n := strings.Count(got, `class="goen-campaigns__meta"`); n != 2 {
			t.Fatalf("%s: %d rows drawn, want 2", tt.locale, n)
		}
		if n := strings.Count(got, `&nbsp;<span aria-hidden="true">·</span>`); n != 2 {
			t.Errorf("%s: %d dots follow a no-break space after the count, want 2", tt.locale, n)
		}
		if n := strings.Count(got, "<time datetime="); n != 2 {
			t.Errorf("%s: %d time elements, want one per campaign", tt.locale, n)
		}
	}
}
