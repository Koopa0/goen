package auth

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestSignInConfirmsOnlyThePasswordChangeFlag(t *testing.T) {
	t.Parallel()
	h := &Handler{google: &Google{}, log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		for _, query := range []string{"", "?changed=0", "?changed=unknown", "?changed=1"} {
			t.Run(locale.Tag()+query, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				res := httptest.NewRecorder()
				h.SignInPage(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/signin"+query, http.NoBody))
				if res.Code != http.StatusOK {
					t.Fatalf("password-change landing = %d, want 200", res.Code)
				}
				doc, err := html.Parse(strings.NewReader(res.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				found := false
				var visit func(*html.Node)
				visit = func(n *html.Node) {
					if n.Type == html.ElementNode {
						for _, attr := range n.Attr {
							if attr.Key == "role" && attr.Val == "status" && strings.Contains(passwordChangedText(n), i18n.T(ctx, i18n.KeyPasswordChanged)) {
								found = true
							}
						}
					}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						visit(c)
					}
				}
				visit(doc)
				if found != (query == "?changed=1") || strings.Contains(passwordChangedText(doc), i18n.T(ctx, i18n.KeyPasswordChanged)) != (query == "?changed=1") {
					t.Errorf("password-change status with %q = %t, want %t", query, found, query == "?changed=1")
				}
			})
		}
	}
}

func passwordChangedText(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
	return b.String()
}
