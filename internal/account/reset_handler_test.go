package account

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestResetPasswordRefusalsDoNotLeakFormattingDiagnostics(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, password, zh, en string
	}{
		{name: "required", zh: "請設定密碼", en: "Choose a password"},
		{name: "short", password: "short", zh: "密碼至少需要 10 個字元", en: "A password needs at least 10 characters"},
		{name: "long", password: strings.Repeat("a", MaxPasswordBytes+1), zh: "密碼過長", en: "That password is too long"},
	} {
		for _, locale := range i18n.Locales() {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				// Password validation returns before any token read or password write.
				h := NewHandler(&Store{}, nil, slog.New(slog.DiscardHandler), false, nil)
				ctx := i18n.WithLocale(t.Context(), locale)
				form := url.Values{"token": {"still-live-token"}, "password": {tt.password}, "confirm": {tt.password}}
				request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/reset", strings.NewReader(form.Encode()))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				response := httptest.NewRecorder()
				h.Reset(response, request)
				if response.Code != http.StatusUnprocessableEntity {
					t.Fatalf("Reset = %d, want 422", response.Code)
				}
				root, err := html.Parse(strings.NewReader(response.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				var token string
				var message strings.Builder
				for node := range root.Descendants() {
					attrs := map[string]string{}
					for _, a := range node.Attr {
						attrs[a.Key] = a.Val
					}
					if node.Type == html.ElementNode && attrs["id"] == "reset-error" {
						for part := range node.Descendants() {
							if part.Type == html.TextNode {
								message.WriteString(part.Data)
							}
						}
					}
					if node.Type == html.ElementNode && node.Data == "input" && attrs["name"] == "token" {
						token = attrs["value"]
					}
				}
				want := tt.zh
				if locale == i18n.En {
					want = tt.en
				}
				if message.String() != want {
					t.Errorf("reset refusal = %q, want %q", message.String(), want)
				}
				if token != "still-live-token" {
					t.Errorf("retained token = %q, want still-live-token", token)
				}
			})
		}
	}
}
