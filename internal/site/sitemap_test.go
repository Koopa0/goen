package site

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/db"
)

type boundedSitemapCatalogue struct {
	categories     int
	products       int
	ignoreLimit    bool
	categoryLimits []int32
	productLimits  []int32
}

func (c *boundedSitemapCatalogue) SitemapCategories(
	_ context.Context, limit int32,
) ([]db.SitemapCategoriesRow, error) {
	c.categoryLimits = append(c.categoryLimits, limit)
	n := c.categories
	if !c.ignoreLimit {
		n = min(n, int(limit))
	}
	rows := make([]db.SitemapCategoriesRow, n)
	for i := range rows {
		rows[i] = db.SitemapCategoriesRow{Slug: fmt.Sprintf("category-%d", i), UpdatedAt: time.Unix(int64(i), 0)}
	}
	return rows, nil
}

func (c *boundedSitemapCatalogue) SitemapProducts(
	_ context.Context, limit int32,
) ([]db.SitemapProductsRow, error) {
	c.productLimits = append(c.productLimits, limit)
	n := c.products
	if !c.ignoreLimit {
		n = min(n, int(limit))
	}
	rows := make([]db.SitemapProductsRow, n)
	for i := range rows {
		rows[i] = db.SitemapProductsRow{Slug: fmt.Sprintf("product-%d", i), UpdatedAt: time.Unix(int64(i), 0)}
	}
	return rows, nil
}

func TestSitemapBudgetBoundsTheWholeDocument(t *testing.T) {
	for _, tt := range []struct {
		name              string
		catalogue         *boundedSitemapCatalogue
		wantCategoryLimit int32
		wantProductLimit  int32
		wantProductCalls  int
	}{
		{
			name: "categories fill the document",
			catalogue: &boundedSitemapCatalogue{
				categories: MaxSitemapURLs + 100, products: 100, ignoreLimit: true,
			},
			wantCategoryLimit: MaxSitemapURLs - 4,
			wantProductCalls:  0,
		},
		{
			name: "products receive only the remainder",
			catalogue: &boundedSitemapCatalogue{
				categories: 100, products: MaxSitemapURLs,
			},
			wantCategoryLimit: MaxSitemapURLs - 4,
			wantProductLimit:  MaxSitemapURLs - 4 - 100,
			wantProductCalls:  1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{
				log: slog.New(slog.DiscardHandler), baseURL: "https://goen.example",
				catalogue: tt.catalogue,
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/sitemap.xml", http.NoBody)
			out := httptest.NewRecorder()
			h.Sitemap(out, req)
			if out.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", out.Code, out.Body.String())
			}
			if got := strings.Count(out.Body.String(), "<url>"); got != MaxSitemapURLs {
				t.Errorf("sitemap contains %d URLs, want total cap %d", got, MaxSitemapURLs)
			}
			if len(tt.catalogue.categoryLimits) != 1 ||
				tt.catalogue.categoryLimits[0] != tt.wantCategoryLimit {
				t.Errorf("category limits = %v, want [%d]",
					tt.catalogue.categoryLimits, tt.wantCategoryLimit)
			}
			if len(tt.catalogue.productLimits) != tt.wantProductCalls {
				t.Fatalf("product limits = %v, want %d call(s)",
					tt.catalogue.productLimits, tt.wantProductCalls)
			}
			if tt.wantProductCalls == 1 && tt.catalogue.productLimits[0] != tt.wantProductLimit {
				t.Errorf("product limit = %d, want remainder %d",
					tt.catalogue.productLimits[0], tt.wantProductLimit)
			}
		})
	}
}
