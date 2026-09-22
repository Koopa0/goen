package acceptance

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequiredBlockedAssertionsRemainVisible(t *testing.T) {
	t.Parallel()
	manifest, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	manifest.Scenarios[0].Assertions = []Assertion{
		{Kind: AssertionGoTest, Package: "./missing", Run: "TestMissing", Status: StatusBlocked, Evidence: "blocked-component"},
		{Kind: AssertionGate, Status: StatusBlocked, Evidence: "dependency-gate"},
		{Kind: AssertionBrowser, Runner: "check-layout", Pages: []string{"/"}, Evidence: "browser-surface"},
	}
	results, err := RunManifest(t.Context(), manifest, RunOptions{ScenarioID: "C01", ExplicitSelection: true, EvidenceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || ExitCode(results, false) == 0 {
		t.Fatalf("required assertions disappeared: %+v", results)
	}
	for i := range results {
		result := &results[i]
		if result.Status != StatusBlocked || result.Command != "" {
			t.Fatalf("blocked assertion executed: %+v", result)
		}
		record := readEvidence(t, result.Artifact)
		if record.Outcome != OutcomeBlocked || record.Executed || record.Error == "" {
			t.Fatalf("blocked artifact = %+v", record)
		}
		if record.Assertion.Evidence != manifest.Scenarios[0].Assertions[i].Evidence {
			t.Fatalf("artifact lost assertion identity: %+v", record)
		}
	}
}

func TestRunGoTestSeparatesStreamsAndPersistsEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, stdout, exit string
		outcome            Outcome
	}{
		{"pass", `{"Action":"pass","Test":"TestPassingEvidence"}`, "0", OutcomePass},
		{"bad-json", "not JSON", "0", OutcomeFail},
		{"failed-command", `{"Action":"pass","Test":"TestPassingEvidence"}`, "1", OutcomeFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			script := "#!/bin/sh\nprintf '%s\\n' '" + tc.stdout + "'\nprintf '%s\\n' 'go: downloading fixture dependency' >&2\nexit " + tc.exit + "\n"
			// The temporary executable stands in for the Go command's stream protocol.
			if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o700); err != nil { //nolint:gosec // G306: this fixture must be executable by its owner.
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			result := runGoTest(t.Context(), runnerEvidenceFixtureRoot(t), "C06", Assertion{Kind: AssertionGoTest, Package: ".", Run: "TestPassingEvidence"})
			if got := resultOutcome(&result); got != tc.outcome {
				t.Fatalf("outcome = %s, want %s: %v", got, tc.outcome, result.Err)
			}
			if result.Output != tc.stdout+"\n" || result.Stderr != "go: downloading fixture dependency\n" {
				t.Fatalf("streams mixed: stdout=%q stderr=%q", result.Output, result.Stderr)
			}
			store, err := newEvidenceStore(t.Context(), repoRoot(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := store.write(&result); err != nil {
				t.Fatal(err)
			}
			record := readEvidence(t, result.Artifact)
			if record.Outcome != tc.outcome || record.Command != result.Command || !record.Executed {
				t.Fatalf("execution metadata = %+v", record)
			}
			if record.StartedAt.IsZero() || record.FinishedAt.Before(record.StartedAt) {
				t.Fatalf("invalid evidence times: %+v", record)
			}
			cmd := exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD")
			cmd.Dir = repoRoot()
			sha, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			if record.Commit != strings.TrimSpace(string(sha)) {
				t.Fatalf("commit = %q, want %q", record.Commit, sha)
			}
			for name, want := range map[string]string{record.Stdout: result.Output, record.Stderr: result.Stderr} {
				if filepath.IsAbs(name) || filepath.Clean(name) != filepath.Base(name) {
					t.Fatalf("non-local stream path %q", name)
				}
				body, err := os.ReadFile(filepath.Join(filepath.Dir(result.Artifact), name))
				if err != nil {
					t.Fatal(err)
				}
				if string(body) != want {
					t.Fatalf("saved %s = %q, want %q", name, body, want)
				}
			}
			if !strings.Contains(FormatResults([]Result{result}), result.Artifact) {
				t.Fatal("CLI output lost artifact location")
			}
		})
	}
}

func TestEvidenceDestinationFailureRejectsRun(t *testing.T) {
	t.Parallel()
	manifest, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	results, err := RunManifest(t.Context(), manifest, RunOptions{ScenarioID: "C02", EvidenceDir: path})
	if err == nil || len(results) != 0 {
		t.Fatalf("unwritable evidence = %v, %v; want failure before execution", results, err)
	}
}

func readEvidence(t *testing.T, path string) Evidence {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record Evidence
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatal(err)
	}
	return record
}
