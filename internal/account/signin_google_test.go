package account

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestARefusedSignInKeepsTheConfiguredWaysIn(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, enabled := range []bool{false, true} {
			h := rateLimitedAccountHandler(deadAccountStore(t))
			if enabled {
				h.google = &Google{clientID: "configured"}
			}
			ctx := i18n.WithLocale(t.Context(), locale)
			page := httptest.NewRecorder()
			h.SignInPage(page, httptest.NewRequestWithContext(ctx, http.MethodGet, "/signin?next=/orders", http.NoBody))
			values := url.Values{"email": {"nobody@example.com"}, "password": {strings.Repeat("x", MaxPasswordBytes+1)}, "next": {"/orders"}}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/signin", strings.NewReader(values.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			failed := httptest.NewRecorder()
			h.SignIn(failed, req)
			if failed.Code != http.StatusUnprocessableEntity {
				t.Fatalf("refused sign-in status = %d, want 422", failed.Code)
			}
			for name, body := range map[string]string{"initial": page.Body.String(), "refused": failed.Body.String()} {
				if got := strings.Contains(body, `href="/auth/google?next=%2Forders"`); got != enabled {
					t.Errorf("%s page, locale %s: Google offered = %t, want %t", name, locale, got, enabled)
				}
			}
			if !strings.Contains(failed.Body.String(), `value="nobody@example.com"`) || !strings.Contains(failed.Body.String(), `name="next" value="/orders"`) {
				t.Error("refused sign-in lost the submitted address or return path")
			}
		}
	}
}
