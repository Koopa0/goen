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

func TestRegistrationAndResetGiveGuidanceBesideTheForm(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			h := NewHandler(&Store{}, nil, slog.New(slog.DiscardHandler), false, nil)
			res := httptest.NewRecorder()
			h.RegisterPage(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/register", http.NoBody))
			root := authGuidanceDOM(t, res.Body.String())
			for _, destination := range []string{"/terms", "/privacy"} {
				found := false
				for n := range root.Descendants() {
					if n.Data == "a" && authGuidanceAttr(n, "href") == destination {
						for p := n.Parent; p != nil; p = p.Parent {
							found = found || p.Data == "form"
						}
					}
				}
				if !found {
					t.Errorf("register form lacks policy link %s", destination)
				}
			}
			res = httptest.NewRecorder()
			h.ResetPage(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/reset?token=unchecked", http.NoBody))
			if !strings.Contains(res.Body.String(), i18n.T(ctx, i18n.KeyPasswordHint)) {
				t.Error("reset form omits the password rule")
			}
			for _, path := range []string{"/register", "/reset"} {
				form := url.Values{"email": {"ada@example.com"}, "password": {"long-password"}, "confirm": {"other-password"}, "token": {"unchecked"}}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				res = httptest.NewRecorder()
				if path == "/register" {
					h.Register(res, req)
				} else {
					h.Reset(res, req)
				}
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("%s mismatch = %d, want 422", path, res.Code)
				}
				root = authGuidanceDOM(t, res.Body.String())
				var confirm, refusal *html.Node
				for n := range root.Descendants() {
					if n.Data == "input" && authGuidanceAttr(n, "name") == "password" && authGuidanceAttr(n, "aria-invalid") == "true" {
						t.Errorf("%s mismatch marks the first password", path)
					}
					if n.Data == "input" && authGuidanceAttr(n, "name") == "confirm" {
						confirm = n
					}
					if authGuidanceAttr(n, "id") == "confirm-error" || authGuidanceAttr(n, "id") == "reset-error" {
						refusal = n
					}
				}
				if confirm == nil || refusal == nil || confirm.Parent != refusal.Parent || authGuidanceAttr(confirm, "aria-invalid") != "true" || !strings.Contains(authGuidanceAttr(confirm, "aria-describedby"), authGuidanceAttr(refusal, "id")) {
					t.Errorf("%s mismatch is not associated with the confirm field alone", path)
				}
			}
		})
	}
}

func authGuidanceDOM(t *testing.T, body string) *html.Node {
	t.Helper()
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func authGuidanceAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
