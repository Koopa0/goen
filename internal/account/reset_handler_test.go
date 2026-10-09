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
	"github.com/koopa0/goen/internal/ui/pages/pagestest"
)

func TestResetPasswordRefusalsDoNotLeakFormattingDiagnostics(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, password, zh, en string
	}{
		{name: "required", zh: "請設定密碼。", en: "Choose a password."},
		{name: "short", password: "short", zh: "密碼至少需要 10 個字元。", en: "A password needs at least 10 characters."},
		{name: "long", password: strings.Repeat("a", MaxPasswordBytes+1), zh: "密碼過長。", en: "That password is too long."},
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

func TestMissingEmailedAccountLinksOfferRecovery(t *testing.T) {
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		for _, tt := range []struct {
			name        string
			path        string
			destination string
			zhHeading   string
			enHeading   string
			handler     http.HandlerFunc
		}{
			{name: "verify", zhHeading: "\u78ba\u8a8d\u96fb\u5b50\u90f5\u4ef6", enHeading: "Confirm your email address", path: "/verify", destination: "/account#email-heading", handler: h.VerifyPage},
			{name: "reset", zhHeading: "\u8a2d\u5b9a\u65b0\u5bc6\u78bc", enHeading: "Set a new password", path: "/reset", destination: "/forgot", handler: h.ResetPage},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				res := httptest.NewRecorder()
				tt.handler(res, httptest.NewRequestWithContext(ctx, http.MethodGet, tt.path, http.NoBody))
				if res.Code != http.StatusOK {
					t.Fatalf("missing link = %d, want 200", res.Code)
				}
				heading := tt.enHeading
				reason := "This link is incomplete; open it again from the button in the email."
				if locale == i18n.ZhHant {
					heading = tt.zhHeading
					reason = "\u9019\u500b\u9023\u7d50\u4e0d\u5b8c\u6574\uff0c\u8acb\u5f9e\u4fe1\u88e1\u7684\u6309\u9215\u91cd\u65b0\u6253\u958b\u3002"
				}
				pagestest.AssertEmailLink(t, res.Body.String(), heading, reason, tt.destination)
			})
		}
	}
}

func TestResetGetKeepsAnUncheckedTokenForm(t *testing.T) {
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			res := httptest.NewRecorder()
			h.ResetPage(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/reset?token=unchecked-token", http.NoBody))
			body := res.Body.String()
			for _, want := range []string{`method="post" action="/reset"`, `name="token" value="unchecked-token"`, `name="password"`, `name="confirm"`} {
				if !strings.Contains(body, want) {
					t.Errorf("live reset form omits %q", want)
				}
			}
			if res.Code != http.StatusOK || strings.Contains(body, i18n.T(ctx, i18n.KeyEmailLinkIncomplete)) || strings.Contains(body, i18n.T(ctx, i18n.KeyEmailLinkDeadTitle)) {
				t.Error("GET validated or refused an unchecked reset token")
			}
		})
	}
}
