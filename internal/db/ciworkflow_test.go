package db_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type ciWorkflow struct {
	On          map[string]any    `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Env         map[string]string `yaml:"env"`
	Concurrency struct {
		Cancel string `yaml:"cancel-in-progress"`
	} `yaml:"concurrency"`
	Jobs map[string]ciJob `yaml:"jobs"`
}

type ciJob struct {
	Name            string            `yaml:"name"`
	If              string            `yaml:"if"`
	ContinueOnError bool              `yaml:"continue-on-error"`
	Timeout         int               `yaml:"timeout-minutes"`
	Permissions     map[string]string `yaml:"permissions"`
	Env             map[string]string `yaml:"env"`
	Steps           []ciStep          `yaml:"steps"`
	Strategy        struct {
		Matrix struct {
			Language []string `yaml:"language"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
}

type ciStep struct {
	Uses            string            `yaml:"uses"`
	Run             string            `yaml:"run"`
	If              string            `yaml:"if"`
	ContinueOnError bool              `yaml:"continue-on-error"`
	With            map[string]string `yaml:"with"`
}

func readCIWorkflow(t *testing.T, name string) ciWorkflow {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name)) //nolint:gosec // The name comes from the repository workflow inventory or a fixed literal.
	if err != nil {
		t.Fatal(err)
	}
	var workflow ciWorkflow
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secrets.") {
		t.Fatal("presubmit workflows must not consume provider secrets")
	}
	return workflow
}

func TestCIWorkflowsKeepUntrustedCodeUnprivileged(t *testing.T) {
	t.Parallel()
	pinned := regexp.MustCompile(`^[\w.-]+/[\w./-]+@[0-9a-f]{40}$`)
	paths, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("workflow inventory: %v (%d files)", err, len(paths))
	}
	for _, path := range paths {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			workflow := readCIWorkflow(t, name)
			if len(workflow.Permissions) != 1 || workflow.Permissions["contents"] != "read" {
				t.Fatal("workflow token must default to contents: read only")
			}
			for _, trigger := range []string{"pull_request_target", "workflow_run"} {
				if _, exists := workflow.On[trigger]; exists {
					t.Fatalf("privileged trigger %s is not permitted for presubmit", trigger)
				}
			}
			if workflow.Concurrency.Cancel != "${{ github.event_name == 'pull_request' }}" {
				t.Fatal("only superseded PR runs may be cancelled; main runs must finish")
			}
			for jobName, job := range workflow.Jobs {
				if job.Timeout <= 0 || job.If != "" || job.ContinueOnError {
					t.Errorf("%s must be bounded, unconditional and fail closed", jobName)
				}
				for permission, value := range job.Permissions {
					analysisUpload := name == "codeql.yml" && permission == "security-events" && value == "write"
					if value != "read" && !analysisUpload {
						t.Errorf("%s grants unexpected %s: %s", jobName, permission, value)
					}
				}
				for _, step := range job.Steps {
					if step.ContinueOnError {
						t.Errorf("%s masks a failed step", jobName)
					}
					if step.Uses != "" && !pinned.MatchString(step.Uses) {
						t.Errorf("%s uses an action without a full commit pin: %s", jobName, step.Uses)
					}
					if strings.HasPrefix(step.Uses, "actions/checkout@") && step.With["persist-credentials"] != "false" {
						t.Errorf("%s leaves checkout credentials available to repository code", jobName)
					}
					if strings.HasPrefix(step.Uses, "actions/setup-go@") {
						if step.With["go-version-file"] != "go.mod" || step.With["check-latest"] != "false" {
							t.Errorf("%s must use the declared Go toolchain", jobName)
						}
						if workflow.Env["GOTOOLCHAIN"] != "local" && job.Env["GOTOOLCHAIN"] != "local" {
							t.Errorf("%s permits an implicit toolchain upgrade", jobName)
						}
					}
				}
			}
		})
	}
}

func TestCIRulesetNamesRealFailClosedGates(t *testing.T) {
	t.Parallel()
	verify := readCIWorkflow(t, "verify.yml")
	for _, trigger := range []string{"pull_request", "push", "workflow_call"} {
		if _, exists := verify.On[trigger]; !exists {
			t.Errorf("verify is missing %s", trigger)
		}
	}
	if verify.On["pull_request"] != nil {
		t.Error("required PR gates must run on every PR update, without event or branch filters")
	}
	for _, trigger := range []string{"pull_request", "push"} {
		if config, ok := verify.On[trigger].(map[string]any); ok {
			for _, key := range []string{"paths", "paths-ignore", "branches-ignore"} {
				if _, filtered := config[key]; filtered {
					t.Errorf("required verify gates may not filter %s with %s", trigger, key)
				}
			}
		}
	}
	for jobName, command := range map[string]string{
		"verify": "make verify", "schema": "make test-integration", "vulnerabilities": "make vuln",
		"ci-policy": "make workflow-check", "commit-attribution": `bash scripts/check-commit-attribution.sh "$BASE_SHA" "$HEAD_SHA"`,
	} {
		found := false
		for _, step := range verify.Jobs[jobName].Steps {
			if step.Run == command && step.If == "" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s must invoke %s unpiped", jobName, command)
		}
	}
	codeql := readCIWorkflow(t, "codeql.yml")
	contexts := map[string]bool{}
	for name := range verify.Jobs {
		contexts[name] = true
	}
	for _, language := range codeql.Jobs["analyze"].Strategy.Matrix.Language {
		contexts[strings.ReplaceAll(codeql.Jobs["analyze"].Name, "${{ matrix.language }}", language)] = true
	}
	if !contexts["CodeQL (go)"] || !contexts["CodeQL (actions)"] {
		t.Fatal("CodeQL must analyze both production Go and workflow security")
	}
	initFound, analyzeFound, buildFound := false, false, false
	for _, step := range codeql.Jobs["analyze"].Steps {
		if strings.HasPrefix(step.Uses, "github/codeql-action/init@") {
			initFound = step.With["languages"] == "${{ matrix.language }}" && step.With["build-mode"] == "${{ matrix.language == 'go' && 'manual' || 'none' }}"
		}
		if strings.HasPrefix(step.Uses, "github/codeql-action/analyze@") {
			analyzeFound = step.If == ""
		}
		if step.Run == "go build ./..." && step.If == "matrix.language == 'go'" {
			buildFound = true
		}
	}
	if !initFound || !analyzeFound || !buildFound {
		t.Fatal("CodeQL must initialize, trace the production Go build and publish analysis")
	}
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "branch-protection.json"))
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Rules []struct {
			Type       string `json:"type"`
			Parameters struct {
				Strict bool `json:"strict_required_status_checks_policy"`
				Checks []struct {
					Context string `json:"context"`
					App     int    `json:"integration_id"`
				} `json:"required_status_checks"`
			} `json:"parameters"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(body, &policy); err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{}
	for _, rule := range policy.Rules {
		if rule.Type != "required_status_checks" {
			continue
		}
		if !rule.Parameters.Strict {
			t.Error("required checks must require an up-to-date base")
		}
		for _, check := range rule.Parameters.Checks {
			if !contexts[check.Context] || check.App != 15368 || required[check.Context] {
				t.Errorf("invalid, duplicate or untrusted required context: %s", check.Context)
			}
			required[check.Context] = true
		}
	}
	for name := range contexts {
		if !required[name] {
			t.Errorf("workflow gate %s is absent from the ruleset artifact", name)
		}
	}
}

func TestCICodeScanningBlocksHighSeverityFindings(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "branch-protection.json"))
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Rules []struct {
			Type       string `json:"type"`
			Parameters struct {
				Tools []struct {
					Tool     string `json:"tool"`
					Alerts   string `json:"alerts_threshold"`
					Security string `json:"security_alerts_threshold"`
				} `json:"code_scanning_tools"`
			} `json:"parameters"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(body, &policy); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rule := range policy.Rules {
		if rule.Type != "code_scanning" {
			continue
		}
		if len(rule.Parameters.Tools) != 1 {
			t.Fatal("exactly one CodeQL scanning rule is required")
		}
		tool := rule.Parameters.Tools[0]
		if tool.Tool != "CodeQL" || tool.Alerts != "errors" || tool.Security != "high_or_higher" {
			t.Fatal("CodeQL must block error-level and high/critical security findings")
		}
		found = true
	}
	if !found {
		t.Fatal("required CodeQL findings rule is missing")
	}
}
