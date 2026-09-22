//go:build integration

package product_test

import (
	"encoding/json"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/product"
)

func TestProductBreadcrumbNamesEveryVisibleCategory(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, tt := range []struct {
			slug       string
			categories []string
		}{
			{"nimbus-buds-pro", []string{"audio"}},
			{"meridian-book-sleeve-14", []string{"accessories", "cases"}},
		} {
			t.Run(string(locale)+"/"+tt.slug, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				h := product.NewHandler(product.NewStore(pool), slog.New(slog.DiscardHandler), "https://goen.example/")
				r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/p/"+tt.slug, http.NoBody)
				r.SetPathValue("slug", tt.slug)
				w := httptest.NewRecorder()
				h.Detail(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("product returned %d", w.Code)
				}
				body := w.Body.String()
				nav := regexp.MustCompile(`<nav class="ui-crumbs"[^>]*>(.*?)</nav>`).FindStringSubmatch(body)
				if len(nav) != 2 {
					t.Fatal("no visible breadcrumb")
				}
				visible := regexp.MustCompile(`<a[^>]+href="/c/([^"]+)"[^>]*>([^<]+)</a>`).FindAllStringSubmatch(nav[1], -1)
				current := regexp.MustCompile(`<span[^>]+aria-current="page"[^>]*>([^<]+)</span>`).FindStringSubmatch(nav[1])
				if len(visible) != len(tt.categories) || len(current) != 2 {
					t.Fatalf("visible breadcrumb missing categories or current product: %s", nav[1])
				}
				script := regexp.MustCompile(`<script type="application/ld\+json">(.*?)</script>`).FindStringSubmatch(body)
				if len(script) != 2 {
					t.Fatal("no structured data")
				}
				var docs []struct {
					Type  string `json:"@type"`
					Items []struct {
						Position int    `json:"position"`
						Name     string `json:"name"`
						URL      string `json:"item"`
					} `json:"itemListElement"`
				}
				if err := json.Unmarshal([]byte(script[1]), &docs); err != nil {
					t.Fatal(err)
				}
				for _, doc := range docs {
					if doc.Type != "BreadcrumbList" {
						continue
					}
					if len(doc.Items) != len(tt.categories)+1 {
						t.Fatalf("structured breadcrumb has %d items, want %d (all categories plus product)", len(doc.Items), len(tt.categories)+1)
					}
					for i, slug := range tt.categories {
						item := doc.Items[i]
						if visible[i][1] != slug || item.URL != "https://goen.example/c/"+slug || item.Name != html.UnescapeString(visible[i][2]) || item.Position != i+1 {
							t.Errorf("category %d: visible %v and structured %+v disagree with %s", i, visible[i][1:], item, slug)
						}
					}
					last := doc.Items[len(doc.Items)-1]
					if last.Name != html.UnescapeString(strings.TrimSpace(current[1])) || last.URL != "" || last.Position != len(doc.Items) {
						t.Errorf("current product does not match visible breadcrumb: %+v", last)
					}
					return
				}
				t.Fatal("structured data has no BreadcrumbList")
			})
		}
	}
}
