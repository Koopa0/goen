package returnpage

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/orderaccess"
)

func TestInaccessibleReturnOffersOrderLookup(t *testing.T) {
	t.Parallel()
	h := &Handler{access: &orderaccess.Store{}, log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		r := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), locale), http.MethodGet, "/orders/GO-260101-000001/return", http.NoBody)
		r.SetPathValue("number", "GO-260101-000001")
		w := httptest.NewRecorder()
		h.Page(w, r)
		_, actions, ok := strings.Cut(w.Body.String(), `class="notice__actions"`)
		if w.Code != http.StatusNotFound || !ok {
			t.Fatalf("inaccessible return: status=%d, actions present=%t", w.Code, ok)
		}
		actions, _, _ = strings.Cut(actions, "</div>")
		_, first, _ := strings.Cut(actions, `href="`)
		first, _, _ = strings.Cut(first, `"`)
		if first != "/orders/find" || !strings.Contains(actions, `href="/signin"`) {
			t.Error("inaccessible return has no order lookup and sign-in recovery")
		}
	}
}
