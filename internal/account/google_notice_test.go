package account

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestDisabledGoogleOffersPasswordSignIn(t *testing.T) {
	t.Parallel()
	h := &Handler{google: &Google{}, log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		for _, handler := range []http.HandlerFunc{h.GoogleSignIn, h.GoogleCallback} {
			r := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), locale), http.MethodGet, "/auth/google", http.NoBody)
			w := httptest.NewRecorder()
			handler(w, r)
			body := w.Body.String()
			if w.Code != http.StatusNotFound || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
				t.Errorf("disabled Google: status=%d content-type=%q", w.Code, w.Header().Get("Content-Type"))
			}
			_, actions, ok := strings.Cut(body, `class="notice__actions"`)
			actions, _, _ = strings.Cut(actions, "</div>")
			_, href, _ := strings.Cut(actions, `href="`)
			href, _, _ = strings.Cut(href, `"`)
			if !ok || href != "/signin" {
				t.Error("disabled Google has no shop-framed sign-in recovery")
			}
		}
	}
}
