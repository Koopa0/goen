package queryplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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

func TestCheckBudgetNoMatchRejectsUnexpectedRows(t *testing.T) {
	b := Budget{Route: RouteSearchNoMatch, WarmMaxMS: 40, ColdMaxMS: 60, MinRows: 0, MaxRows: 0}
	err := CheckBudget(b, Result{
		Route: RouteSearchNoMatch, Warm: true, ExecutionMS: 1, ActualRows: 24,
	})
	if err == nil {
		t.Fatal("expected exact-empty rejection")
	}
	if err := CheckBudget(b, Result{
		Route: RouteSearchNoMatch, Warm: true, ExecutionMS: 1, ActualRows: 0,
	}); err != nil {
		t.Fatalf("empty result within budget: %v", err)
	}
}

func TestCheckBudgetSkipsRowChecksForCountReads(t *testing.T) {
	b := Budget{Route: RouteSearchBrand, WarmMaxMS: 40, ColdMaxMS: 60, MinRows: 1, MaxRows: 24}
	if err := CheckBudget(b, Result{
		Route: RouteSearchBrand, Warm: true, ExecutionMS: 1, ActualRows: 1, CountRead: true,
	}); err != nil {
		t.Fatalf("count read should skip row budget: %v", err)
	}
}

func TestWriteArtifactsKeepsBothScales(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "results.json")

	small := []Result{{Route: RouteHomeRecommended, Scale: ScaleSmall, Warm: false}}
	large := []Result{{Route: RouteHomeRecommended, Scale: ScaleLarge, Warm: false}}

	write := func(results []Result) {
		t.Helper()
		merged, err := mergeArtifactResults(path, results)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.MarshalIndent(merged, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, payload, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(small)
	write(large)

	got, err := readArtifactResults(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("manifest len = %d, want 2 scales", len(got))
	}
	scales := map[Scale]bool{}
	for _, r := range got {
		scales[r.Scale] = true
	}
	if !scales[ScaleSmall] || !scales[ScaleLarge] {
		t.Fatalf("missing scale in manifest: %+v", scales)
	}
}
