package cart

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/orderaccess"
)

func TestInaccessibleOrderActionsOfferLookupAndSignIn(t *testing.T) {
	t.Parallel()
	h := &Handler{access: &orderaccess.Store{}, log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		for _, route := range []struct {
			path    string
			method  string
			handler http.HandlerFunc
		}{
			{"", http.MethodGet, h.OrderPage},
			{"/cancel", http.MethodPost, h.CancelOrder},
			{"/reorder", http.MethodPost, h.ReorderItems},
		} {
			r := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), locale), route.method, "/orders/GO-260101-000001"+route.path, http.NoBody)
			r.SetPathValue("number", "GO-260101-000001")
			w := httptest.NewRecorder()
			route.handler(w, r)
			_, actions, ok := strings.Cut(w.Body.String(), `class="notice__actions"`)
			if w.Code != http.StatusNotFound || !ok {
				t.Fatalf("inaccessible order: status=%d actions present=%t", w.Code, ok)
			}
			actions, _, _ = strings.Cut(actions, "</div>")
			_, href, _ := strings.Cut(actions, `href="`)
			href, _, _ = strings.Cut(href, `"`)
			if href != "/orders/find" || !strings.Contains(actions, `href="/signin"`) {
				t.Errorf("inaccessible order actions=%s, want order lookup and sign-in", actions)
			}
		}
	}
}
