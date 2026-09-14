package queryplan

import "testing"

func TestBudgetsCoverEveryRoute(t *testing.T) {
	routes := []Route{
		RouteHomeRecommended,
		RouteHomeCategories,
		RouteCategoryListing,
		RouteCategoryFiltered,
		RouteCategoryPriceAsc,
		RouteCategoryDeepPage,
		RouteSearchNameLatin,
		RouteSearchBrand,
		RouteSearchChinese,
		RouteSearchNoMatch,
	}
	for _, scale := range []Scale{ScaleSmall, ScaleLarge} {
		budgets := Budgets(scale)
		if len(budgets) != len(routes) {
			t.Fatalf("%s: %d budgets, want %d routes", scale, len(budgets), len(routes))
		}
		for _, route := range routes {
			if _, ok := BudgetFor(scale, route); !ok {
				t.Errorf("%s: missing budget for %s", scale, route)
			}
		}
	}
}

func TestCheckBudgetRejectsSlowExecution(t *testing.T) {
	b := Budget{Route: RouteHomeRecommended, WarmMaxMS: 10, ColdMaxMS: 40, MinRows: 1, MaxRows: 8}
	if err := CheckBudget(b, Result{Route: RouteHomeRecommended, Warm: true, ExecutionMS: 11, ActualRows: 4}); err == nil {
		t.Fatal("expected warm budget breach")
	}
	if err := CheckBudget(b, Result{Route: RouteHomeRecommended, Warm: false, ExecutionMS: 41, ActualRows: 4}); err == nil {
		t.Fatal("expected cold budget breach")
	}
	if err := CheckBudget(b, Result{Route: RouteHomeRecommended, Warm: true, ExecutionMS: 5, ActualRows: 4}); err != nil {
		t.Fatalf("within budget: %v", err)
	}
}
