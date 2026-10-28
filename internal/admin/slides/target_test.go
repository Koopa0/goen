package slides

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestActiveTargetSaysTheAction: a slide switched on and one switched off each show their own sentence.
func TestActiveTargetSaysTheAction(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		active bool
		want   i18n.Key
	}{
		{true, i18n.KeyAdminNoticeHeroOn},
		{false, i18n.KeyAdminNoticeHeroOff},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, activeTarget(tt.active), http.NoBody)
		if got, want := widget.NoticeFrom(r, notices).Text, i18n.T(r.Context(), tt.want); got != want {
			t.Errorf("ActiveTargetSaysTheAction(%v) shows %q, want %q", tt.active, got, want)
		}
	}
}
