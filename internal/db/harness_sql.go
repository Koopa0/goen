// Package db exposes canonical sqlc query text for the catalogue query-plan harness.
package db

import "strings"

func stripSQLHeader(raw string) string {
	if i := strings.Index(raw, "\n"); i >= 0 {
		return strings.TrimSpace(raw[i+1:])
	}
	return strings.TrimSpace(raw)
}

// HarnessCatalogueSQL is canonical sqlc text for the query-plan harness (#335).
var HarnessCatalogueSQL = struct {
	HomeRecommendedTiles string
	RootCategories       string
	CategoryListing      string
	CategoryListingCount string
	SearchProducts       string
	SearchProductsCount  string
}{
	HomeRecommendedTiles: stripSQLHeader(homeRecommendedTiles),
	RootCategories:       stripSQLHeader(rootCategories),
	CategoryListing:      stripSQLHeader(categoryListing),
	CategoryListingCount: stripSQLHeader(categoryListingCount),
	SearchProducts:       stripSQLHeader(searchProducts),
	SearchProductsCount:  stripSQLHeader(searchProductsCount),
}
