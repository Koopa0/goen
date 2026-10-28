package stock

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestActiveQuerySaysTheAction: a variant switched on and one switched off each show their own sentence.
func TestActiveQuerySaysTheAction(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		active bool
		want   i18n.Key
	}{
		{true, i18n.KeyAdminNoticeVariantOn},
		{false, i18n.KeyAdminNoticeVariantOff},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/stock/SKU"+activeQuery(tt.active), http.NoBody)
		if got, want := widget.NoticeFrom(r, skuNotices).Text, i18n.T(r.Context(), tt.want); got != want {
			t.Errorf("ActiveQuerySaysTheAction(%v) shows %q, want %q", tt.active, got, want)
		}
	}
}
