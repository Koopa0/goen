package pages

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestProductUpdateAuditLabelIsLocalized(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
	}{
		{name: "Traditional Chinese", locale: i18n.ZhHant, want: "修改商品"},
		{name: "English", locale: i18n.En, want: "Edit product"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			if got := (AuditEntry{Action: "product.update"}).Label(ctx); got != tt.want {
				t.Errorf("product.update label = %q, want %q", got, tt.want)
			}
		})
	}
}
