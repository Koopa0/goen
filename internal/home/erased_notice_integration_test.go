//go:build integration

package home_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/i18n"
)

// TestHandler_IndexErasureFlag holds that Index confirms the account erasure
// only for ?erased=1, not for an absent, zero or unknown value.
func TestHandler_IndexErasureFlag(t *testing.T) {
	h := home.NewHandler(home.NewStore(pool), slog.New(slog.DiscardHandler))
	for _, locale := range i18n.Locales() {
		for _, query := range []string{"", "?erased=0", "?erased=unknown", "?erased=1"} {
			t.Run(locale.Tag()+query, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				res := httptest.NewRecorder()
				h.Index(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/"+query, http.NoBody))
				if res.Code != http.StatusOK {
					t.Fatalf("erasure landing = %d, want 200", res.Code)
				}
				doc, err := html.Parse(strings.NewReader(res.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				found := false
				var visit func(*html.Node)
				visit = func(n *html.Node) {
					if n.Type == html.ElementNode {
						id, role := "", ""
						for _, attr := range n.Attr {
							if attr.Key == "id" {
								id = attr.Val
							}
							if attr.Key == "role" {
								role = attr.Val
							}
						}
						if id == "account-erased" {
							found = role == "status"
						}
					}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						visit(c)
					}
				}
				visit(doc)
				present := strings.Contains(res.Body.String(), i18n.T(ctx, i18n.KeyAccountErased))
				if found != (query == "?erased=1") || present != (query == "?erased=1") {
					t.Errorf("erasure result with %q: status=%t sentence=%t, want both %t", query, found, present, query == "?erased=1")
				}
			})
		}
	}
}
