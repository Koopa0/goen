package acceptance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/acceptance"
)

func TestManifestListsEveryRequiredScenario(t *testing.T) {
	t.Parallel()
	manifest, err := acceptance.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if err := acceptance.Validate(manifest); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, scenario := range manifest.Scenarios {
		seen[scenario.ID] = true
	}
	for _, id := range acceptance.RequiredScenarioIDs {
		if !seen[id] {
			t.Fatalf("manifest is missing required scenario %s", id)
		}
	}
}

func TestManifestDetectsMissingScenario(t *testing.T) {
	t.Parallel()
	manifest, err := acceptance.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	trimmed := manifest
	trimmed.Scenarios = trimmed.Scenarios[:len(trimmed.Scenarios)-1]
	err = acceptance.Validate(trimmed)
	if err == nil {
		t.Fatal("validate accepted a manifest with a missing scenario")
	}
	if !strings.Contains(err.Error(), "missing required scenario") {
		t.Fatalf("missing scenario error = %v", err)
	}
}

func TestEveryGoTestAssertionNamesAnExistingTest(t *testing.T) {
	t.Parallel()
	manifest, err := acceptance.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	root := repoRoot(t)
	for _, scenario := range manifest.Scenarios {
		for _, assertion := range scenario.Assertions {
			if assertion.Kind != acceptance.AssertionGoTest {
				continue
			}
			status := assertion.Status
			if status == "" {
				status = acceptance.StatusReady
			}
			if status == acceptance.StatusBlocked {
				continue
			}
			pkgDir := filepath.Join(root, strings.TrimPrefix(assertion.Package, "./"))
			entries, readErr := os.ReadDir(pkgDir)
			if readErr != nil {
				t.Fatalf("%s: read %s: %v", scenario.ID, pkgDir, readErr)
			}
			found := false
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
					continue
				}
				body, err := os.ReadFile(filepath.Join(pkgDir, entry.Name())) //nolint:gosec // test inventory only.
				if err != nil {
					t.Fatalf("%s: read %s: %v", scenario.ID, entry.Name(), err)
				}
				if strings.Contains(string(body), "func "+assertion.Run) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s: test %s is not declared under %s", scenario.ID, assertion.Run, assertion.Package)
			}
		}
	}
}

func TestManifestIsStableJSON(t *testing.T) {
	t.Parallel()
	path, err := acceptance.ManifestPath()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path) //nolint:gosec // manifest path is package-owned.
	if err != nil {
		t.Fatal(err)
	}
	var decoded acceptance.Manifest
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if decoded.Epic != 331 {
		t.Fatalf("epic = %d, want 331", decoded.Epic)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	path, err := acceptance.ManifestPath()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(path))
}
