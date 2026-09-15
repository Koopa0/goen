package pages

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestPlacementGrantFailedSpeaksBothLocales(t *testing.T) {
	t.Parallel()
	tests := []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, "訂單已成立,但尚未完成存取"},
		{i18n.En, "Your order was received, but access could not be set up"},
	}
	for _, tt := range tests {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		html := renderNotice(t, PlacementGrantFailed(layouts.Page{Title: "test"}), ctx)
		if !strings.Contains(html, tt.want) {
			t.Errorf("placement grant failure in %s = %q, want %q", tt.locale, html, tt.want)
		}
		if !strings.Contains(html, "/orders/find") {
			t.Errorf("placement grant failure in %s has no find-order recovery link", tt.locale)
		}
	}
}

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
		html := renderNotice(t, FindOrderGrantFailed(layouts.Page{Title: "test"}), ctx)
		if !strings.Contains(html, tt.want) {
			t.Errorf("find-order grant failure in %s = %q, want %q", tt.locale, html, tt.want)
		}
		if !strings.Contains(html, "/orders/find") {
			t.Errorf("find-order grant failure in %s has no find-order recovery link", tt.locale)
		}
	}
}

func renderNotice(t *testing.T, c templ.Component, ctx context.Context) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(ctx, &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}
