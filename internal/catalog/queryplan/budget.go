// Package queryplan captures catalogue read plans and checks them against
// scale-dependent budgets declared for issue #335.
package queryplan

// Scale is a disposable catalogue shape the query-plan harness loads.
type Scale string

const (
	ScaleSmall Scale = "small" // ~15 products; dev catalogue shape
	ScaleLarge Scale = "large" // 10,000 active products
)

// Route is one catalogue read the storefront issues per request.
type Route string

const (
	RouteHomeRecommended  Route = "home_recommended"
	RouteHomeCategories   Route = "home_categories"
	RouteCategoryListing  Route = "category_listing"
	RouteCategoryFiltered Route = "category_filtered"
	RouteCategoryPriceAsc Route = "category_price_asc"
	RouteCategoryDeepPage Route = "category_deep_page"
	RouteSearchNameLatin  Route = "search_name_latin"
	RouteSearchBrand      Route = "search_brand"
	RouteSearchChinese    Route = "search_chinese_broad"
	RouteSearchNoMatch    Route = "search_no_match"
)

// Budget is a declared warm read ceiling in milliseconds. Cold runs may be
// higher; see ColdMultiplier. Measurements are SQL execution time from EXPLAIN
// (ANALYZE, BUFFERS, FORMAT JSON) — pool wait is owned by #332/#333.
type Budget struct {
	Route     Route
	WarmMaxMS float64
	ColdMaxMS float64
	MinRows   int64 // sanity floor when the fixture must return something
	MaxRows   int64 // sanity ceiling for bounded pages
}

// ColdMultiplier scales warm budgets when no shared buffer cache exists yet.
const ColdMultiplier = 4.0

// Budgets returns the declared read ceilings for a scale. Targets are separate
// from measurements: a breach means either optimize or raise the budget with
// evidence, not silently widen without a ruling.
func Budgets(scale Scale) []Budget {
	switch scale {
	case ScaleSmall:
		return smallBudgets()
	case ScaleLarge:
		return largeBudgets()
	default:
		return nil
	}
}

func smallBudgets() []Budget {
	const warm = 15.0
	return []Budget{
		{Route: RouteHomeRecommended, WarmMaxMS: warm, ColdMaxMS: warm * ColdMultiplier, MinRows: 1, MaxRows: 8},
		{Route: RouteHomeCategories, WarmMaxMS: 5, ColdMaxMS: 20, MinRows: 6, MaxRows: 8},
		{Route: RouteCategoryListing, WarmMaxMS: warm, ColdMaxMS: warm * ColdMultiplier, MinRows: 1, MaxRows: 24},
		{Route: RouteCategoryFiltered, WarmMaxMS: warm, ColdMaxMS: warm * ColdMultiplier, MinRows: 0, MaxRows: 24},
		{Route: RouteCategoryPriceAsc, WarmMaxMS: warm, ColdMaxMS: warm * ColdMultiplier, MinRows: 1, MaxRows: 24},
		{Route: RouteCategoryDeepPage, WarmMaxMS: warm, ColdMaxMS: warm * ColdMultiplier, MinRows: 0, MaxRows: 24},
		{Route: RouteSearchNameLatin, WarmMaxMS: warm, ColdMaxMS: warm * ColdMultiplier, MinRows: 1, MaxRows: 24},
		{Route: RouteSearchBrand, WarmMaxMS: warm, ColdMaxMS: warm * ColdMultiplier, MinRows: 1, MaxRows: 24},
		{Route: RouteSearchChinese, WarmMaxMS: warm, ColdMaxMS: warm * ColdMultiplier, MinRows: 0, MaxRows: 24},
		{Route: RouteSearchNoMatch, WarmMaxMS: warm, ColdMaxMS: warm * ColdMultiplier, MinRows: 0, MaxRows: 0},
	}
}

func largeBudgets() []Budget {
	// Targets from PR #25 on bare metal (~70ms warm / ~276ms cold home tiles;
	// Latin trgm ~1.5ms; two-char Chinese ~8.8ms). Ceilings below include the
	// measured testcontainers envelope on this fixture (2026-09-14) with margin.
	return []Budget{
		{Route: RouteHomeRecommended, WarmMaxMS: 100, ColdMaxMS: 700, MinRows: 1, MaxRows: 8},
		{Route: RouteHomeCategories, WarmMaxMS: 5, ColdMaxMS: 20, MinRows: 6, MaxRows: 8},
		{Route: RouteCategoryListing, WarmMaxMS: 25, ColdMaxMS: 50, MinRows: 1, MaxRows: 24},
		{Route: RouteCategoryFiltered, WarmMaxMS: 25, ColdMaxMS: 50, MinRows: 0, MaxRows: 24},
		{Route: RouteCategoryPriceAsc, WarmMaxMS: 25, ColdMaxMS: 50, MinRows: 1, MaxRows: 24},
		{Route: RouteCategoryDeepPage, WarmMaxMS: 25, ColdMaxMS: 50, MinRows: 0, MaxRows: 24},
		{Route: RouteSearchNameLatin, WarmMaxMS: 50, ColdMaxMS: 80, MinRows: 1, MaxRows: 24},
		{Route: RouteSearchBrand, WarmMaxMS: 40, ColdMaxMS: 60, MinRows: 1, MaxRows: 24},
		{Route: RouteSearchChinese, WarmMaxMS: 50, ColdMaxMS: 80, MinRows: 0, MaxRows: 24},
		{Route: RouteSearchNoMatch, WarmMaxMS: 40, ColdMaxMS: 60, MinRows: 0, MaxRows: 0},
	}
}

// SeedFile returns the committed SQL fixture for a scale.
func SeedFile(scale Scale) string {
	switch scale {
	case ScaleSmall:
		return "seed/scale_catalog_small.sql"
	case ScaleLarge:
		return "seed/scale_catalog_large.sql"
	default:
		return ""
	}
}

// ProductCount is the active catalogue size each scale fixture loads.
func ProductCount(scale Scale) int {
	switch scale {
	case ScaleSmall:
		return 15
	case ScaleLarge:
		return 10_000
	default:
		return 0
	}
}
