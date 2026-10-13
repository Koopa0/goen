package site

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

func TestUnreadableAddressRendersTheShopBeforeRouting(t *testing.T) {
	t.Parallel()
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		for _, target := range []string{"/p/%00", "/p/%E9", "/search?q=%00", "/search?q=%E9"} {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
			r.Header.Set("Accept-Language", locale.Tag())
			w := httptest.NewRecorder()
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unreadable address reached routing") })
			web.RefuseUnstorableText(next, false, http.HandlerFunc(h.BadRequest)).ServeHTTP(w, r)
			body := w.Body.String()
			if w.Code != http.StatusBadRequest || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
				t.Errorf("%s: status=%d content-type=%q", target, w.Code, w.Header().Get("Content-Type"))
			}
			for _, want := range []string{`<html lang="` + locale.Tag() + `"`, `class="notice__actions"`, `href="/"`, i18n.T(i18n.WithLocale(t.Context(), locale), i18n.KeyAddressUnreadable)} {
				if !strings.Contains(body, want) {
					t.Errorf("%s: shop error lacks %q", target, want)
				}
			}
		}
	}
}
