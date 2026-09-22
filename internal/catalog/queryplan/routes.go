package queryplan

import (
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
)

// Query is one measured catalogue read.
type Query struct {
	Route     Route
	SQL       string
	Args      []any
	CountRead bool // count queries share a route budget but skip row cardinality.
}

// Queries returns every route the acceptance harness measures.
func Queries(scale Scale, categoryIDs []uuid.UUID, phonesCategory uuid.UUID) []Query {
	pageSize := int32(catalog.PageSize)
	deepOffset := int32(4800)
	if scale == ScaleSmall {
		deepOffset = 0
	}
	searchLatin := "%Pixelight%"
	searchChinese := "%\u8033%"
	if scale == ScaleLarge {
		searchLatin = "%Scale%"
		searchChinese = "%\u91cf\u6e2c%"
	}
	locale := string(i18n.Default)
	emptyBrands := []uuid.UUID{}

	listing := func(route Route, filterVariants, inStockOnly bool, minPrice, maxPrice int64, sort string, offset int32) []Query {
		listArgs := []any{
			locale, categoryIDs, emptyBrands, filterVariants, inStockOnly,
			minPrice, maxPrice, sort, offset, pageSize,
		}
		countArgs := []any{
			categoryIDs, emptyBrands, filterVariants, inStockOnly, minPrice, maxPrice,
		}
		return []Query{
			{Route: route, SQL: db.HarnessCatalogueSQL.CategoryListing, Args: listArgs},
			{Route: route, SQL: db.HarnessCatalogueSQL.CategoryListingCount, Args: countArgs, CountRead: true},
		}
	}

	search := func(route Route, pattern string) []Query {
		listArgs := []any{locale, pattern, int32(0), pageSize}
		return []Query{
			{Route: route, SQL: db.HarnessCatalogueSQL.SearchProducts, Args: listArgs},
			{Route: route, SQL: db.HarnessCatalogueSQL.SearchProductsCount, Args: []any{pattern}, CountRead: true},
		}
	}

	out := make([]Query, 0, 20)
	out = append(out,
		Query{
			Route: RouteHomeRecommended,
			SQL:   db.HarnessCatalogueSQL.HomeRecommendedTiles,
			Args:  []any{int32(8), locale},
		},
		Query{
			Route: RouteHomeCategories,
			SQL:   db.HarnessCatalogueSQL.RootCategories,
			Args:  []any{locale},
		},
	)
	out = append(out, listing(RouteCategoryListing, false, false, 0, 0, "", 0)...)
	out = append(out, listing(RouteCategoryFiltered, true, true, 100_000, 0, "", 0)...)
	out = append(out, listing(RouteCategoryPriceAsc, false, false, 0, 0, "price_asc", 0)...)
	out = append(out, listing(RouteCategoryDeepPage, false, false, 0, 0, "", deepOffset)...)
	out = append(out, search(RouteSearchNameLatin, searchLatin)...)
	out = append(out, search(RouteSearchBrand, "%Meridian%")...)
	out = append(out, search(RouteSearchChinese, searchChinese)...)
	out = append(out, search(RouteSearchNoMatch, "%zzznomatchzz%")...)
	return out
}
