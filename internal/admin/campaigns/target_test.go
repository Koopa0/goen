package campaigns

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestActiveQuerySaysTheAction: a campaign switched on and one switched off
// each show their own sentence.
func TestActiveQuerySaysTheAction(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		active bool
		want   i18n.Key
	}{
		{true, i18n.KeyAdminNoticeCampaignOn},
		{false, i18n.KeyAdminNoticeCampaignOff},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/campaigns"+activeQuery(tt.active), http.NoBody)
		if got, want := widget.NoticeFrom(r, notices).Text, i18n.T(r.Context(), tt.want); got != want {
			t.Errorf("activeQuery(%v) shows %q, want %q", tt.active, got, want)
		}
	}
}

// TestFeaturedQuerySaysTheAction: adding a product to a campaign and taking one
// out each show their own sentence.
func TestFeaturedQuerySaysTheAction(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		removed bool
		want    i18n.Key
	}{
		{false, i18n.KeyAdminNoticeCampaignFeatured},
		{true, i18n.KeyAdminNoticeCampaignUnfeatured},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/campaigns/spring"+featuredQuery(tt.removed), http.NoBody)
		if got, want := widget.NoticeFrom(r, notices).Text, i18n.T(r.Context(), tt.want); got != want {
			t.Errorf("featuredQuery(%v) shows %q, want %q", tt.removed, got, want)
		}
	}
}
