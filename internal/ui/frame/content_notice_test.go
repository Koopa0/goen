package frame

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestFooterContentNotice: the footer names the content language and the governing policy version.
func TestFooterContentNotice(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, "部分商品說明只有繁體中文。政策文件的中英文版本如有不同，以繁體中文版為準。"},
		{i18n.En, "Some product descriptions are in Traditional Chinese only. Where the English and Traditional Chinese policies differ, the Traditional Chinese version applies."},
	} {
		t.Run(string(tc.locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tc.locale)
			if got := i18n.T(ctx, i18n.KeyContentNotice); got != tc.want {
				t.Errorf("content notice = %q, want %q", got, tc.want)
			}
			var rendered strings.Builder
			if err := Footer(NewsletterState{}).Render(ctx, &rendered); err != nil {
				t.Fatalf("render footer: %v", err)
			}
			wantCount := 1
			if tc.locale == i18n.Default {
				wantCount = 0
			}
			if count := strings.Count(rendered.String(), tc.want); count != wantCount {
				t.Errorf("footer %s exact content notice count = %d, want %d", tc.locale, count, wantCount)
			}
		})
	}
}
