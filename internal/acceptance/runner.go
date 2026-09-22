package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Result is the outcome of one scenario or assertion run.
type Result struct {
	ScenarioID string
	Assertion  Assertion
	Status     Status
	Command    string
	Duration   time.Duration
	Output     string
	Stderr     string
	StartedAt  time.Time
	FinishedAt time.Time
	Artifact   string
	Err        error
}

// RunOptions controls suite execution.
type RunOptions struct {
	Root              string
	EvidenceDir       string
	ReadyOnly         bool
	WithBrowser       bool
	ScenarioID        string
	ExplicitSelection bool
}

// RunManifest executes ready scenarios and returns per-assertion results.
func RunManifest(ctx context.Context, manifest Manifest, opts RunOptions) ([]Result, error) {
	if err := Validate(manifest); err != nil {
		return nil, err
	}
	root := opts.Root
	if root == "" {
		root = repoRoot()
	}
	scenarios, err := selectedScenarios(manifest, opts.ScenarioID)
	if err != nil {
		return nil, err
	}
	evidence, err := newEvidenceStore(ctx, root, opts.EvidenceDir)
	if err != nil {
		return nil, err
	}
	var results []Result
	for i := range scenarios {
		scenarioResults := runScenario(ctx, root, &scenarios[i], opts)
		for j := range scenarioResults {
			result := &scenarioResults[j]
			if writeErr := evidence.write(result); writeErr != nil {
				return append(results, scenarioResults...), writeErr
			}
		}
		results = append(results, scenarioResults...)
	}
	if len(results) == 0 {
		return nil, errors.New("no required assertions were selected for execution")
	}
	return results, nil
}

func selectedScenarios(manifest Manifest, scenarioID string) ([]Scenario, error) {
	switch scenarioID {
	case "", "all":
		return manifest.Scenarios, nil
	default:
		scenario, err := manifest.ByID(scenarioID)
		if err != nil {
			return nil, err
		}
		return []Scenario{scenario}, nil
	}
}

func runScenario(ctx context.Context, root string, scenario *Scenario, opts RunOptions) []Result {
	if scenario.Status == StatusBlocked {
		if opts.ExplicitSelection || !opts.ReadyOnly {
			return blockedScenarioResults(scenario)
		}
		return nil
	}
	if opts.ReadyOnly && scenario.Status != StatusReady {
		return nil
	}
	var results []Result
	for _, assertion := range scenario.Assertions {
		results = append(results, runAssertion(ctx, root, scenario.ID, assertion, opts))
	}
	if len(results) == 0 {
		results = append(results, blockedResult(scenario.ID, Assertion{}, "scenario has no executable required assertions"))
	}
	return append(results, blockedExtensionResults(scenario)...)
}

func runAssertion(ctx context.Context, root, scenarioID string, assertion Assertion, opts RunOptions) Result {
	if assertion.Status == StatusBlocked || assertion.Kind == AssertionGate {
		return blockedResult(scenarioID, assertion, "required assertion is blocked: "+assertion.Evidence)
	}
	switch assertion.Kind {
	case AssertionGoTest:
		return runGoTest(ctx, root, scenarioID, assertion)
	case AssertionBrowser:
		if !opts.WithBrowser {
			return blockedResult(scenarioID, assertion, "required browser evidence was not requested; use --with-browser")
		}
		return runBrowser(ctx, root, scenarioID, assertion)
	default:
		return blockedResult(scenarioID, assertion, "required assertion has no executable runner")
	}
}

func blockedResult(scenarioID string, assertion Assertion, reason string) Result {
	now := time.Now().UTC()
	return Result{ScenarioID: scenarioID, Assertion: assertion, Status: StatusBlocked,
		StartedAt: now, FinishedAt: now, Err: errors.New(reason)}
}

func blockedScenarioResults(scenario *Scenario) []Result {
	var results []Result
	for _, assertion := range scenario.Assertions {
		results = append(results, blockedResult(scenario.ID, assertion,
			fmt.Sprintf("scenario blocked by issues %v", scenario.BlockedBy)))
	}
	if len(results) == 0 {
		results = append(results, blockedResult(scenario.ID, Assertion{}, "blocked scenario has no required assertions"))
	}
	return append(results, blockedExtensionResults(scenario)...)
}

func blockedExtensionResults(scenario *Scenario) []Result {
	results := make([]Result, 0, len(scenario.Extensions))
	for _, extension := range scenario.Extensions {
		assertion := Assertion{Kind: AssertionGate, Evidence: extension.Evidence, Status: StatusBlocked}
		results = append(results, blockedResult(scenario.ID, assertion,
			fmt.Sprintf("extension blocked: %s (#%v)", extension.Title, extension.Issues)))
	}
	return results
}

func repoRoot() string {
	path, err := ManifestPath()
	if err != nil {
		return "."
	}
	return filepath.Dir(filepath.Dir(path))
}

func runGoTest(ctx context.Context, root, scenarioID string, assertion Assertion) Result {
	start := time.Now()
	args := make([]string, 0, 6+len(assertion.BuildTags))
	args = append(args, "test")
	if len(assertion.BuildTags) > 0 {
		args = append(args, "-tags="+strings.Join(assertion.BuildTags, ","))
	}
	args = append(args,
		"-count=1",
		"-race",
		"-json",
		assertion.Package,
		"-run", "^"+assertion.Run+"$",
	)
	cmd := exec.CommandContext(ctx, "go", args...) //nolint:gosec // Arguments are manifest-owned package paths and test names.
	cmd.Dir = root
	var output, stderr bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &stderr
	err := cmd.Run()
	outputText := output.String()
	if evidenceErr := verifyRequiredGoTest(outputText, assertion.Run); evidenceErr != nil {
		if err == nil {
			err = evidenceErr
		}
	}
	return Result{
		ScenarioID: scenarioID,
		Assertion:  assertion,
		Status:     StatusReady,
		Command:    "go " + strings.Join(args, " "),
		Duration:   time.Since(start),
		Output:     outputText,
		Stderr:     stderr.String(),
		StartedAt:  start.UTC(),
		FinishedAt: time.Now().UTC(),
		Err:        err,
	}
}

type goTestEvent struct {
	Action string
	Test   string
	Output string
}

func verifyRequiredGoTest(output, testName string) error {
	if strings.TrimSpace(output) == "" {
		return fmt.Errorf("required test %q produced no go test output", testName)
	}
	var (
		sawEvent bool
		passed   bool
	)
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event goTestEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return fmt.Errorf("required test %q produced malformed go test output: %w", testName, err)
		}
		sawEvent = true
		if event.Test != testName {
			if event.Test == "" && strings.Contains(event.Output, "no tests to run") {
				return fmt.Errorf("required test %q did not run", testName)
			}
			continue
		}
		switch event.Action {
		case "pass":
			passed = true
		case "skip":
			return fmt.Errorf("required test %q was skipped", testName)
		case "fail":
			return fmt.Errorf("required test %q failed", testName)
		}
	}
	if !sawEvent {
		return fmt.Errorf("required test %q produced no structured go test events", testName)
	}
	if !passed {
		return fmt.Errorf("required test %q did not pass", testName)
	}
	return nil
}

func runBrowser(ctx context.Context, root, scenarioID string, assertion Assertion) Result {
	start := time.Now()
	if assertion.Runner != "check-layout" {
		return Result{
			ScenarioID: scenarioID,
			Assertion:  assertion,
			Status:     StatusBlocked,
			Err:        fmt.Errorf("unknown browser runner %q", assertion.Runner),
		}
	}
	chrome := os.Getenv("CHROME")
	if chrome == "" {
		return Result{
			ScenarioID: scenarioID,
			Assertion:  assertion,
			Status:     StatusBlocked,
			Err:        fmt.Errorf("browser evidence needs CHROME and a running server (make run); pages=%v", assertion.Pages),
		}
	}
	url := os.Getenv("GOEN_URL")
	if url == "" {
		url = "http://127.0.0.1:9700/"
	}
	cmd := exec.CommandContext(ctx, "make", "check-layout")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"CHROME="+chrome,
		"GOEN_URL="+url,
	)
	var output, stderr bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &stderr
	err := cmd.Run()
	return Result{
		ScenarioID: scenarioID,
		Assertion:  assertion,
		Status:     StatusReady,
		Command:    "make check-layout",
		Duration:   time.Since(start),
		Output:     output.String(),
		Stderr:     stderr.String(),
		StartedAt:  start.UTC(),
		FinishedAt: time.Now().UTC(),
		Err:        err,
	}
}

// ExitCode maps results to a process exit status.
func ExitCode(results []Result, _ bool) int {
	if len(results) == 0 {
		return 1
	}
	for i := range results {
		result := &results[i]
		if result.Err != nil {
			return 1
		}
		if result.Status == StatusBlocked {
			return 1
		}
	}
	return 0
}

// FormatResults renders human-readable output.
func FormatResults(results []Result) string {
	var b strings.Builder
	for i := range results {
		result := &results[i]
		state := resultOutcome(result)
		fmt.Fprintf(&b, "%s %s", result.ScenarioID, state)
		if result.Assertion.Run != "" {
			fmt.Fprintf(&b, " %s/%s", result.Assertion.Package, result.Assertion.Run)
		} else if result.Assertion.Runner != "" {
			fmt.Fprintf(&b, " %s", result.Assertion.Runner)
		}
		if result.Duration > 0 {
			fmt.Fprintf(&b, " (%s)", result.Duration.Round(time.Millisecond))
		}
		if result.Artifact != "" {
			fmt.Fprintf(&b, " evidence=%s", result.Artifact)
		}
		b.WriteByte('\n')
		if result.Err != nil {
			fmt.Fprintf(&b, "  %v\n", result.Err)
		}
	}
	return b.String()
}
