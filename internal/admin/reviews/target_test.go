package reviews

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestHiddenTargetSaysTheAction: a review hidden and one shown each show their own sentence.
func TestHiddenTargetSaysTheAction(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		hidden bool
		want   i18n.Key
	}{
		{true, i18n.KeyAdminNoticeReviewHidden},
		{false, i18n.KeyAdminNoticeReviewShown},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, hiddenTarget(tt.hidden), http.NoBody)
		if got, want := widget.NoticeFrom(r, notices).Text, i18n.T(r.Context(), tt.want); got != want {
			t.Errorf("HiddenTargetSaysTheAction(%v) shows %q, want %q", tt.hidden, got, want)
		}
	}
}
