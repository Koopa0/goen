package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestPlacementGrantFailedSpeaksBothLocales(t *testing.T) {
	t.Parallel()
	tests := []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, "已收到你的訂單，但尚未完成存取"},
		{i18n.En, "Your order was received, but access could not be set up"},
	}
	for _, tt := range tests {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		html := renderComponent(t, ctx, PlacementGrantFailed(layouts.Page{Title: "test"}))
		if !strings.Contains(html, tt.want) {
			t.Errorf("placement grant failure in %s = %q, want %q", tt.locale, html, tt.want)
		}
		if !strings.Contains(html, "/orders/find") {
			t.Errorf("placement grant failure in %s has no find-order recovery link", tt.locale)
		}
	}
}
