package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestFindOrderGrantFailedSpeaksBothLocales(t *testing.T) {
	t.Parallel()
	tests := []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, "無法完成查詢"},
		{i18n.En, "Lookup could not be completed"},
	}
	for _, tt := range tests {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		html := renderComponent(t, ctx, FindOrderGrantFailed(layouts.Page{Title: "test"}))
		if !strings.Contains(html, tt.want) {
			t.Errorf("find-order grant failure in %s = %q, want %q", tt.locale, html, tt.want)
		}
		if !strings.Contains(html, "/orders/find") {
			t.Errorf("find-order grant failure in %s has no find-order recovery link", tt.locale)
		}
	}
}
