package queryplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RunAll measures every route cold then warm and checks declared budgets.
func RunAll(ctx context.Context, pool *pgxpool.Pool, scale Scale, commitSHA string) ([]Result, error) {
	results, err := MeasureAll(ctx, pool, scale, commitSHA)
	if err != nil {
		return nil, err
	}
	for i := range results {
		r := results[i]
		budget, ok := BudgetFor(scale, r.Route)
		if !ok {
			return nil, fmt.Errorf("no budget for %s at %s", r.Route, scale)
		}
		if budgetErr := CheckBudget(budget, r); budgetErr != nil {
			return nil, fmt.Errorf("%w (%s planning %.2fms execution %.2fms rows %d)",
				budgetErr, warmLabel(r.Warm), r.PlanningMS, r.ExecutionMS, r.ActualRows)
		}
	}
	return results, nil
}

// MeasureAll records cold and warm EXPLAIN timings without budget checks.
func MeasureAll(ctx context.Context, pool *pgxpool.Pool, scale Scale, commitSHA string) ([]Result, error) {
	if err := prepareCatalogue(ctx, pool); err != nil {
		return nil, err
	}
	categoryIDs, phones, err := catalogueScope(ctx, pool)
	if err != nil {
		return nil, err
	}
	queries := Queries(scale, categoryIDs, phones)

	var out []Result
	for _, q := range queries {
		// Cold is the first EXPLAIN after ANALYZE; warm repeats use a cached plan.
		// Shared-buffer cold starts are owned by #333.
		cold, err := measureQuery(ctx, pool, scale, q, false, commitSHA)
		if err != nil {
			return nil, err
		}
		out = append(out, cold...)

		warm, err := measureQuery(ctx, pool, scale, q, true, commitSHA)
		if err != nil {
			return nil, err
		}
		out = append(out, warm...)
	}
	return out, nil
}

// warmSamples is how many warm EXPLAIN runs the harness keeps per query.
const warmSamples = 3

func measureQuery(ctx context.Context, pool *pgxpool.Pool, scale Scale, q Query, warm bool, sha string) ([]Result, error) {
	if !warm {
		r, err := Measure(ctx, pool, q, scale, false, 0, sha)
		if err != nil {
			return nil, err
		}
		return []Result{r}, nil
	}
	if _, err := pool.Exec(ctx, q.SQL, q.Args...); err != nil {
		return nil, fmt.Errorf("%s warm run: %w", q.Route, err)
	}
	out := make([]Result, 0, warmSamples)
	for i := range warmSamples {
		r, err := Measure(ctx, pool, q, scale, true, i+1, sha)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func prepareCatalogue(ctx context.Context, pool *pgxpool.Pool) error {
	stmts := []string{
		"ANALYZE products",
		"ANALYZE product_variants",
		"ANALYZE product_images",
		"ANALYZE product_reviews",
		"ANALYZE product_specs",
		"ANALYZE brands",
		"ANALYZE categories",
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return nil
}

func catalogueScope(ctx context.Context, pool *pgxpool.Pool) (categoryIDs []uuid.UUID, phonesCategory uuid.UUID, err error) {
	rows, err := pool.Query(ctx, `
		WITH RECURSIVE d AS (
		    SELECT c.id FROM categories c WHERE c.slug = 'phones'
		    UNION ALL
		    SELECT c.id FROM categories c JOIN d ON c.parent_id = d.id
		)
		SELECT id FROM d`)
	if err != nil {
		return nil, uuid.Nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, uuid.Nil, scanErr
		}
		categoryIDs = append(categoryIDs, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, uuid.Nil, rowsErr
	}
	if scanErr := pool.QueryRow(ctx, `SELECT id FROM categories WHERE slug = 'phones'`).Scan(&phonesCategory); scanErr != nil {
		return nil, uuid.Nil, scanErr
	}
	return categoryIDs, phonesCategory, nil
}

// WriteArtifacts stores JSON evidence under internal/catalog/queryplan/artifacts/<sha>/.
// Each scale write merges into the manifest so small and large runs both survive.
func WriteArtifacts(results []Result, commitSHA string) (string, error) {
	root, rootErr := repoRoot()
	if rootErr != nil {
		return "", rootErr
	}
	dir := filepath.Join(root, "internal", "catalog", "queryplan", "artifacts", commitSHA)
	if mkdirErr := os.MkdirAll(dir, 0o750); mkdirErr != nil {
		return "", mkdirErr
	}
	path := filepath.Join(dir, "results.json")
	merged, err := mergeArtifactResults(path, results)
	if err != nil {
		return "", err
	}
	payload, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func mergeArtifactResults(path string, incoming []Result) ([]Result, error) {
	if len(incoming) == 0 {
		return readArtifactResults(path)
	}
	existing, err := readArtifactResults(path)
	if err != nil {
		return nil, err
	}
	replaced := map[Scale]bool{}
	for i := range incoming {
		replaced[incoming[i].Scale] = true
	}
	out := make([]Result, 0, len(existing)+len(incoming))
	for i := range existing {
		if !replaced[existing[i].Scale] {
			out = append(out, existing[i])
		}
	}
	out = append(out, incoming...)
	slices.SortFunc(out, compareArtifactResults)
	return out, nil
}

func compareArtifactResults(a, b Result) int {
	if a.Scale != b.Scale {
		if a.Scale == ScaleSmall {
			return -1
		}
		return 1
	}
	if a.Route != b.Route {
		if a.Route < b.Route {
			return -1
		}
		return 1
	}
	if a.Warm != b.Warm {
		if !a.Warm {
			return -1
		}
		return 1
	}
	if a.WarmSample != b.WarmSample {
		if a.WarmSample < b.WarmSample {
			return -1
		}
		return 1
	}
	if a.CountRead != b.CountRead {
		if !a.CountRead {
			return -1
		}
		return 1
	}
	return 0
}

func readArtifactResults(path string) ([]Result, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path joins repo root to a fixed artifact filename
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Result
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return out, nil
}

func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("repo root: no caller")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", ".."), nil
}

// LoadSeed executes a scale fixture SQL file against pool.
func LoadSeed(ctx context.Context, pool *pgxpool.Pool, scale Scale) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	path := filepath.Join(root, SeedFile(scale))
	sql, err := os.ReadFile(path) //nolint:gosec // G304: path joins repo root to a fixed seed filename
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("load %s: %w", scale, err)
	}
	return nil
}
