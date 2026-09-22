package acceptance

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func runnerEvidenceFixtureRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller frame unavailable")
	}
	return filepath.Join(filepath.Dir(file), "testdata", "runner_evidence")
}

func TestRunGoTestRejectsMissingRequiredEvidence(t *testing.T) {
	t.Parallel()
	result := runGoTest(t.Context(), runnerEvidenceFixtureRoot(t), "C06", Assertion{
		Kind:    AssertionGoTest,
		Package: ".",
		Run:     "TestMissingEvidence",
	})
	if result.Err == nil {
		t.Fatalf("missing required test was reported as PASS:\n%s", result.Output)
	}
	if ExitCode([]Result{result}, false) == 0 {
		t.Fatal("missing required test exited success")
	}
	if formatted := FormatResults([]Result{result}); !strings.Contains(formatted, "C06 FAIL") || !strings.Contains(formatted, "TestMissingEvidence") {
		t.Fatalf("formatted output = %q, want failure for missing test", formatted)
	}
}

func TestRunGoTestRejectsSkippedRequiredEvidence(t *testing.T) {
	t.Parallel()
	result := runGoTest(t.Context(), runnerEvidenceFixtureRoot(t), "C06", Assertion{
		Kind:    AssertionGoTest,
		Package: ".",
		Run:     "TestSkippedEvidence",
	})
	if result.Err == nil {
		t.Fatalf("skipped required test was reported as PASS:\n%s", result.Output)
	}
	if ExitCode([]Result{result}, false) == 0 {
		t.Fatal("skipped required test exited success")
	}
	if formatted := FormatResults([]Result{result}); !strings.Contains(formatted, "C06 FAIL") || !strings.Contains(formatted, "TestSkippedEvidence") {
		t.Fatalf("formatted output = %q, want failure for skipped test", formatted)
	}
}

func TestRunGoTestAcceptsPassingRequiredEvidence(t *testing.T) {
	t.Parallel()
	result := runGoTest(t.Context(), runnerEvidenceFixtureRoot(t), "C06", Assertion{
		Kind:    AssertionGoTest,
		Package: ".",
		Run:     "TestPassingEvidence",
	})
	if result.Err != nil {
		t.Fatalf("passing required test failed: %v\n%s", result.Err, result.Output)
	}
	if ExitCode([]Result{result}, false) != 0 {
		t.Fatal("passing required test exited failure")
	}
	if formatted := FormatResults([]Result{result}); !strings.Contains(formatted, "C06 PASS") || !strings.Contains(formatted, "TestPassingEvidence") {
		t.Fatalf("formatted output = %q, want pass for executed test", formatted)
	}
}
