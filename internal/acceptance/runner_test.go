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
		EvidenceDir:       t.TempDir(),
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

func TestReadyOnlyAllRejectsAnEmptySelection(t *testing.T) {
	t.Parallel()
	manifest, err := acceptance.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	for i := range manifest.Scenarios {
		manifest.Scenarios[i].Status = acceptance.StatusBlocked
	}
	results, err := acceptance.RunManifest(t.Context(), manifest, acceptance.RunOptions{
		ScenarioID: "all", ReadyOnly: true, EvidenceDir: t.TempDir(),
	})
	if err == nil || len(results) != 0 {
		t.Fatalf("empty selection = %v, %v; want error", results, err)
	}
}

func TestExitCodeRejectsEmptyAllModeResults(t *testing.T) {
	t.Parallel()
	if acceptance.ExitCode(nil, true) == 0 || acceptance.ExitCode(nil, false) == 0 {
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
