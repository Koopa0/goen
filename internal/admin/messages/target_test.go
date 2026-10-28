package messages

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestHandledTargetSaysTheAction: a message marked handled and one reopened each show their own sentence.
func TestHandledTargetSaysTheAction(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		handled bool
		want    i18n.Key
	}{
		{true, i18n.KeyAdminNoticeMessageHandled},
		{false, i18n.KeyAdminNoticeMessageReopened},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, handledTarget(tt.handled), http.NoBody)
		if got, want := widget.NoticeFrom(r, notices).Text, i18n.T(r.Context(), tt.want); got != want {
			t.Errorf("HandledTargetSaysTheAction(%v) shows %q, want %q", tt.handled, got, want)
		}
	}
}
