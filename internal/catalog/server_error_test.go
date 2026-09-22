package catalog

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestServerErrorNamesItsHTTPStatus(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)
			res := httptest.NewRecorder()
			h := Handler{log: slog.New(slog.DiscardHandler)}
			h.serverError(res, req)
			if res.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", res.Code)
			}
			if !strings.Contains(res.Body.String(), `class="notice__code" aria-hidden="true">500</span>`) {
				t.Error("the server-error page has no visible 500 status")
			}
		})
	}
}
