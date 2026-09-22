//go:build integration

package queryplan_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/koopa0/goen/internal/catalog/queryplan"
	"github.com/koopa0/goen/internal/db/dbtest"
)

func TestCatalogueQueryPlansSmallScale(t *testing.T) {
	runScale(t, queryplan.ScaleSmall)
}

func TestCatalogueQueryPlansLargeScale(t *testing.T) {
	if os.Getenv("GOEN_SKIP_LARGE_QUERY_PLANS") == "1" {
		t.Skip("large scale query plans skipped")
	}
	runScale(t, queryplan.ScaleLarge)
}

func TestIndexCatalogueDocumentsPredicates(t *testing.T) {
	if len(queryplan.IndexCatalogue) == 0 {
		t.Fatal("index catalogue is empty")
	}
	if len(queryplan.UnindexedPredicates) == 0 {
		t.Fatal("unindexed predicate list is empty")
	}
}

func runScale(t *testing.T, scale queryplan.Scale) {
	t.Helper()
	ctx := t.Context()
	pool, stop, err := dbtest.Start(ctx)
	if err != nil {
		t.Fatalf("start database: %v", err)
	}
	defer stop()

	if loadErr := queryplan.LoadSeed(ctx, pool, scale); loadErr != nil {
		t.Fatalf("load %s seed: %v", scale, loadErr)
	}
	var count int
	if countErr := pool.QueryRow(ctx, `SELECT count(*) FROM products WHERE status = 'active'`).Scan(&count); countErr != nil {
		t.Fatalf("count products: %v", countErr)
	}
	if count != queryplan.ProductCount(scale) {
		t.Fatalf("active products = %d, want %d for %s", count, queryplan.ProductCount(scale), scale)
	}

	sha := gitCommitSHA(t)
	results, runErr := queryplan.RunAll(ctx, pool, scale, sha)
	if runErr != nil {
		t.Errorf("%s query plans: %v", scale, runErr)
	}
	if len(results) == 0 {
		t.Fatalf("%s: no measurements recorded", scale)
	}
	for i := range results {
		if results[i].CommitSHA != sha {
			t.Errorf("%s: result commit sha %q != %q", results[i].Route, results[i].CommitSHA, sha)
		}
	}
	if os.Getenv("GOEN_WRITE_QUERY_PLAN_ARTIFACTS") == "1" {
		path, writeErr := queryplan.WriteArtifacts(results, sha)
		if writeErr != nil {
			t.Fatalf("write artifacts: %v", writeErr)
		}
		t.Logf("wrote %s (%d measurements)", path, len(results))
	}
}

func gitCommitSHA(t *testing.T) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return string(out[:len(out)-1]) // trim newline
}
