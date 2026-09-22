//go:build integration

package queryplan_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/koopa0/goen/internal/catalog/queryplan"
	"github.com/koopa0/goen/internal/db/dbtest"
)

func TestRunAllRetainsFailedSamplesAndArtifacts(t *testing.T) {
	ctx := t.Context()
	pool, stop, err := dbtest.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if err := queryplan.LoadSeed(ctx, pool, queryplan.ScaleSmall); err != nil {
		t.Fatal(err)
	}
	// Empty publication deterministically breaches the nonempty-route oracle.
	if _, err := pool.Exec(ctx, `UPDATE products SET status = 'draft'`); err != nil {
		t.Fatal(err)
	}
	sha := "failure-" + uuid.NewString()
	results, runErr := queryplan.RunAll(ctx, pool, queryplan.ScaleSmall, sha)
	if runErr == nil || !strings.Contains(runErr.Error(), "below minimum") {
		t.Fatalf("empty fixture must fail the declared budgets: %v", runErr)
	}
	if len(results) != 72 {
		t.Fatalf("retained %d measurements, want all 72", len(results))
	}
	path, writeErr := queryplan.WriteArtifacts(results, sha)
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(filepath.Dir(path)); err != nil {
			t.Error(err)
		}
	})
	payload, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var recorded []queryplan.Result
	if err := json.Unmarshal(payload, &recorded); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != len(results) {
		t.Fatalf("artifact retained %d of %d measurements", len(recorded), len(results))
	}
	passed, failed := 0, 0
	for _, result := range recorded {
		if result.CommitSHA != sha || len(result.PlanJSON) == 0 || result.BudgetCheck == nil {
			t.Fatalf("sample lost its provenance, plan or verdict: %+v", result)
		}
		if result.BudgetCheck.Passed {
			passed++
		} else {
			failed++
			if result.BudgetCheck.Error == "" {
				t.Fatal("failed sample lost its diagnostic")
			}
		}
	}
	if passed == 0 || failed == 0 {
		t.Fatalf("artifact must retain both passing and failing samples: passed=%d failed=%d", passed, failed)
	}
}
