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

func TestMissingEmailedAccountLinksOfferRecovery(t *testing.T) {
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		for _, tt := range []struct {
			name, path, destination string
			handler http.HandlerFunc
		}{
			{name: "verify", path: "/verify", destination: "/account#email-heading", handler: h.VerifyPage},
			{name: "reset", path: "/reset", destination: "/forgot", handler: h.ResetPage},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				res := httptest.NewRecorder()
				tt.handler(res, httptest.NewRequestWithContext(ctx, http.MethodGet, tt.path, http.NoBody))
				if res.Code != http.StatusOK {
					t.Fatalf("missing link = %d, want 200", res.Code)
				}
				reason := "This link is incomplete; open it again from the button in the email."
				if locale == i18n.ZhHant {
					reason = "\u9019\u500b\u9023\u7d50\u4e0d\u5b8c\u6574\uff0c\u8acb\u5f9e\u4fe1\u88e1\u7684\u6309\u9215\u91cd\u65b0\u6253\u958b\u3002"
				}
				assertEmailLinkRecovery(t, res.Body.String(), reason, tt.destination)
			})
		}
	}
}

func assertEmailLinkRecovery(t *testing.T, body, reason, destination string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	attr := func(n *html.Node, key string) string {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
		return ""
	}
	var panel *html.Node
	ids := map[string]bool{}
	for n := range doc.Descendants() {
		if id := attr(n, "id"); id != "" {
			if ids[id] {
				t.Errorf("duplicate id %q", id)
			}
			ids[id] = true
		}
		if n.Type == html.ElementNode && n.Data == "h1" {
			panel = n.Parent
		}
	}
	if panel == nil {
		t.Fatal("recovery has no heading")
	}
	var text strings.Builder
	primary := 0
	for n := range panel.Descendants() {
		if n.Type == html.TextNode {
			text.WriteString(n.Data)
		}
		if attr(n, "name") == "token" || strings.Contains(attr(n, "class"), "goen-medallion") {
			t.Errorf("failure retains %s %q", n.Data, attr(n, "class"))
		}
		if strings.Contains(attr(n, "class"), "goen-btn--primary") {
			primary++
			got := attr(n, "href")
			if n.Data == "button" {
				for p := n.Parent; p != nil; p = p.Parent {
					if p.Data == "form" {
						got = attr(p, "action")
						if destination == "/newsletter" && attr(p, "method") != "post" {
							t.Error("newsletter recovery is not a plain POST form")
						}
						break
					}
				}
			}
			if got != destination {
				t.Errorf("primary destination = %q, want %q", got, destination)
			}
		}
	}
	if primary != 1 {
		t.Errorf("recovery primaries = %d, want 1", primary)
	}
	if !strings.Contains(text.String(), reason) {
		t.Errorf("recovery reason = %q, want %q", text.String(), reason)
	}
}
