package acceptance_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/acceptance"
)

func TestExplicitBlockedScenarioReturnsBlockedResult(t *testing.T) {
	t.Parallel()
	manifest, err := acceptance.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if _, findErr := manifest.ByID("C02"); findErr != nil {
		t.Fatal(findErr)
	}
	results, err := acceptance.RunManifest(t.Context(), manifest, acceptance.RunOptions{
		ScenarioID:        "C02",
		ExplicitSelection: true,
		ReadyOnly:         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("explicit blocked scenario produced no results")
	}
	if results[0].Status != acceptance.StatusBlocked || results[0].Err == nil {
		t.Fatalf("result = %+v, want blocked with error", results[0])
	}
	if acceptance.ExitCode(results, false) == 0 {
		t.Fatal("explicit blocked scenario exited success")
	}
}

func TestReadyOnlyAllSkipsBlockedWithoutExplicitSelection(t *testing.T) {
	t.Parallel()
	opts := acceptance.RunOptions{
		ScenarioID:        "all",
		ReadyOnly:         true,
		ExplicitSelection: false,
	}
	scenario := acceptance.Scenario{ID: "C02", Status: acceptance.StatusBlocked}
	if scenario.Status == acceptance.StatusBlocked {
		if opts.ExplicitSelection || !opts.ReadyOnly {
			t.Fatal("ready-only all should not execute blocked scenarios")
		}
	}
}

func TestExitCodeRejectsEmptyAllModeResults(t *testing.T) {
	t.Parallel()
	if acceptance.ExitCode(nil, true) == 0 {
		t.Fatal("all mode accepted an empty result set")
	}
}

func TestFormatResultsMarksBlockedScenario(t *testing.T) {
	t.Parallel()
	out := acceptance.FormatResults([]acceptance.Result{{
		ScenarioID: "C02",
		Status:     acceptance.StatusBlocked,
		Err:        errors.New("scenario blocked by issues [330]"),
	}})
	if !strings.Contains(out, "C02 BLOCKED") {
		t.Fatalf("formatted output = %q, want blocked status", out)
	}
}

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
