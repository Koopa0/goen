package acceptance

import (
	"bytes"
	"context"
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
	Err        error
}

// RunOptions controls suite execution.
type RunOptions struct {
	Root        string
	ReadyOnly   bool
	WithBrowser bool
	ScenarioID  string
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
	var results []Result
	for i := range scenarios {
		results = append(results, runScenario(ctx, root, &scenarios[i], opts)...)
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
	if opts.ReadyOnly && scenario.Status != StatusReady {
		return nil
	}
	if scenario.Status == StatusBlocked {
		return blockedScenarioResults(scenario)
	}
	var results []Result
	ready := scenario.ReadyAssertions()
	for i := range ready {
		results = append(results, runGoTest(ctx, root, scenario.ID, ready[i]))
	}
	if opts.WithBrowser {
		browser := scenario.BrowserAssertions()
		for i := range browser {
			results = append(results, runBrowser(ctx, root, scenario.ID, browser[i]))
		}
	}
	return append(results, blockedExtensionResults(scenario)...)
}

func blockedScenarioResults(scenario *Scenario) []Result {
	results := make([]Result, 0, 1+len(scenario.Extensions))
	results = append(results, Result{
		ScenarioID: scenario.ID,
		Status:     StatusBlocked,
		Err:        fmt.Errorf("scenario blocked by issues %v", scenario.BlockedBy),
	})
	return append(results, blockedExtensionResults(scenario)...)
}

func blockedExtensionResults(scenario *Scenario) []Result {
	results := make([]Result, 0, len(scenario.Extensions))
	for i := range scenario.Extensions {
		extension := &scenario.Extensions[i]
		results = append(results, Result{
			ScenarioID: scenario.ID,
			Status:     StatusBlocked,
			Err:        fmt.Errorf("extension blocked: %s (#%v)", extension.Title, extension.Issues),
		})
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
	for _, tag := range assertion.BuildTags {
		args = append(args, "-tags="+tag)
	}
	args = append(args,
		"-count=1",
		"-race",
		assertion.Package,
		"-run", "^"+assertion.Run+"$",
	)
	cmd := exec.CommandContext(ctx, "go", args...) //nolint:gosec // Arguments are manifest-owned package paths and test names.
	cmd.Dir = root
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return Result{
		ScenarioID: scenarioID,
		Assertion:  assertion,
		Status:     StatusReady,
		Command:    "go " + strings.Join(args, " "),
		Duration:   time.Since(start),
		Output:     output.String(),
		Err:        err,
	}
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
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	status := StatusReady
	if err != nil {
		status = StatusBlocked
	}
	return Result{
		ScenarioID: scenarioID,
		Assertion:  assertion,
		Status:     status,
		Command:    "make check-layout",
		Duration:   time.Since(start),
		Output:     output.String(),
		Err:        err,
	}
}

// ExitCode maps results to a process exit status.
func ExitCode(results []Result, allMode bool) int {
	for i := range results {
		result := &results[i]
		if result.Err != nil {
			return 1
		}
		if allMode && result.Status == StatusBlocked {
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
		state := "PASS"
		if result.Err != nil {
			state = "FAIL"
		} else if result.Status == StatusBlocked {
			state = "BLOCKED"
		}
		fmt.Fprintf(&b, "%s %s", result.ScenarioID, state)
		if result.Assertion.Run != "" {
			fmt.Fprintf(&b, " %s/%s", result.Assertion.Package, result.Assertion.Run)
		} else if result.Assertion.Runner != "" {
			fmt.Fprintf(&b, " %s", result.Assertion.Runner)
		}
		if result.Duration > 0 {
			fmt.Fprintf(&b, " (%s)", result.Duration.Round(time.Millisecond))
		}
		b.WriteByte('\n')
		if result.Err != nil {
			fmt.Fprintf(&b, "  %v\n", result.Err)
		}
	}
	return b.String()
}
