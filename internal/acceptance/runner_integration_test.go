//go:build integration

package acceptance_test

import (
	"github.com/koopa0/goen/internal/acceptance"
	"testing"
)

func TestRunnerExecutesC06CheckoutReplayAssertion(t *testing.T) {
	if testing.Short() {
		t.Skip("runner invokes integration tests")
	}
	manifest, err := acceptance.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	results, err := acceptance.RunManifest(t.Context(), manifest, acceptance.RunOptions{
		ScenarioID:        "C06",
		ExplicitSelection: true,
		Root:              repoRoot(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if acceptance.ExitCode(results, false) != 0 {
		t.Fatalf("unexpected C06 failure:\n%s", acceptance.FormatResults(results))
	}
	found := false
	for i := range results {
		if results[i].Assertion.Run != "TestCheckoutHTTPReplayFindsTheSameOrderAfterTheCartIsEmpty" {
			continue
		}
		found = true
		if results[i].Err != nil {
			t.Fatalf("checkout replay assertion failed: %v\n%s", results[i].Err, results[i].Output)
		}
	}
	if !found {
		t.Fatal("runner did not execute TestCheckoutHTTPReplayFindsTheSameOrderAfterTheCartIsEmpty")
	}
}
