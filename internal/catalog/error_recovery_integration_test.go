//go:build integration

package catalog_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
)

func TestMissingCampaignOffersCurrentDeals(t *testing.T) {
	h := catalog.NewHandler(catalog.NewStore(pool), slog.New(slog.DiscardHandler))
	for _, locale := range i18n.Locales() {
		r := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), locale), http.MethodGet, "/s/missing", http.NoBody)
		r.SetPathValue("slug", "missing-"+uuid.NewString())
		w := httptest.NewRecorder()
		h.Campaign(w, r)
		_, actions, ok := strings.Cut(w.Body.String(), `class="notice__actions"`)
		if w.Code != http.StatusNotFound || !ok {
			t.Fatalf("missing campaign: status=%d actions present=%t", w.Code, ok)
		}
		actions, _, _ = strings.Cut(actions, "</div>")
		_, href, _ := strings.Cut(actions, `href="`)
		href, _, _ = strings.Cut(href, `"`)
		if href != "/deals" || !strings.Contains(actions, i18n.T(r.Context(), i18n.KeyCurrentDeals)) {
			t.Errorf("missing campaign actions = %s, want current deals", actions)
		}
		if strings.Contains(w.Body.String(), `class="notice__code"`) {
			t.Error("missing campaign still shows a status code")
		}
	}
}
