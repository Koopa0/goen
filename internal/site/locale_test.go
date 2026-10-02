package site

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/web"
)

func TestTheReturnTargetFallsBackToThisFeaturesOwnPage(t *testing.T) {
	const fallback = "/"
	if got := web.SitePathOr("//evil.example", fallback); got != fallback {
		t.Errorf("a refused target gave %q, want %q", got, fallback)
	}
	if got := web.SitePathOr("/deals", fallback); got != "/deals" {
		t.Errorf("a same-site path gave %q", got)
	}
}

func TestSetLocaleReturnsToTheSameURLAndRefusesAForeignOne(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ret    string
		want   string
		locale string
	}{
		{"search keeps its query", "/search?q=pixelight", "/search?q=pixelight", "en"},
		{"filtered listing", "/c/phones?sort=price&brand=aurora", "/c/phones?sort=price&brand=aurora", "zh-Hant"},
		{"product options", "/p/aurora-slate?%E9%A1%8F%E8%89%B2=%E9%8A%80", "/p/aurora-slate?%E9%A1%8F%E8%89%B2=%E9%8A%80", "en"},
		{"absolute", "https://evil.example/", "/", "en"},
		{"protocol-relative", "//evil.example/x?q=1", "/", "en"},
		{"backslash", `/\evil.example`, "/", "en"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := &Handler{}
			form := url.Values{"locale": {tt.locale}, "return": {tt.ret}}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/locale", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			h.SetLocale(rec, req)

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303", rec.Code)
			}
			if got := rec.Header().Get("Location"); got != tt.want {
				t.Errorf("Location = %q, want %q", got, tt.want)
			}
		})
	}
}
