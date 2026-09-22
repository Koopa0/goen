package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Outcome is the execution verdict, distinct from manifest availability.
type Outcome string

const (
	OutcomePass    Outcome = "PASS"
	OutcomeFail    Outcome = "FAIL"
	OutcomeBlocked Outcome = "BLOCKED"
)

func resultOutcome(result *Result) Outcome {
	if result.Status == StatusBlocked {
		return OutcomeBlocked
	}
	if result.Err != nil {
		return OutcomeFail
	}
	return OutcomePass
}

// Evidence records one assertion. Stream paths are relative to this JSON file.
// A dirty checkout names its base commit without claiming immutable source.
type Evidence struct {
	SchemaVersion int       `json:"schema_version"`
	ScenarioID    string    `json:"scenario_id"`
	Assertion     Assertion `json:"assertion"`
	Commit        string    `json:"commit"`
	Dirty         bool      `json:"dirty"`
	Command       string    `json:"command"`
	Executed      bool      `json:"executed"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	DurationMS    int64     `json:"duration_ms"`
	Outcome       Outcome   `json:"outcome"`
	Error         string    `json:"error,omitempty"`
	Stdout        string    `json:"stdout_path"`
	Stderr        string    `json:"stderr_path"`
	SurfaceOnly   bool      `json:"surface_only"`
}

type evidenceStore struct {
	dir, commit string
	dirty       bool
	next        int
}

func newEvidenceStore(ctx context.Context, root, base string) (*evidenceStore, error) {
	commitCommand := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	commitCommand.Dir = root
	commit, err := commitCommand.Output()
	if err != nil {
		return nil, fmt.Errorf("identify acceptance commit: %w", err)
	}
	statusCommand := exec.CommandContext(ctx, "git", "status", "--porcelain")
	statusCommand.Dir = root
	status, err := statusCommand.Output()
	if err != nil {
		return nil, fmt.Errorf("identify acceptance source state: %w", err)
	}
	if base == "" {
		base = filepath.Join(root, "tmp", "commerce-acceptance")
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return nil, fmt.Errorf("resolve evidence directory: %w", err)
	}
	if mkdirErr := os.MkdirAll(base, 0o700); mkdirErr != nil {
		return nil, fmt.Errorf("create evidence directory: %w", mkdirErr)
	}
	dir, err := os.MkdirTemp(base, "run-")
	if err != nil {
		return nil, fmt.Errorf("create evidence run: %w", err)
	}
	return &evidenceStore{dir: dir, commit: strings.TrimSpace(string(commit)), dirty: len(status) != 0}, nil
}

func (s *evidenceStore) write(result *Result) error {
	s.next++
	dir := filepath.Join(s.dir, fmt.Sprintf("%04d", s.next))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return fmt.Errorf("create assertion evidence: %w", err)
	}
	if result.StartedAt.IsZero() {
		result.StartedAt = time.Now().UTC()
		result.FinishedAt = result.StartedAt
	}
	record := Evidence{
		SchemaVersion: 1, ScenarioID: result.ScenarioID, Assertion: result.Assertion,
		Commit: s.commit, Dirty: s.dirty, Command: result.Command, Executed: result.Command != "",
		StartedAt: result.StartedAt, FinishedAt: result.FinishedAt, DurationMS: result.Duration.Milliseconds(),
		Outcome: resultOutcome(result), Stdout: "stdout.log", Stderr: "stderr.log",
		SurfaceOnly: result.Assertion.Kind == AssertionBrowser,
	}
	if result.Err != nil {
		record.Error = result.Err.Error()
	}
	for name, body := range map[string]string{record.Stdout: result.Output, record.Stderr: result.Stderr} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			return fmt.Errorf("save acceptance stream %s: %w", name, err)
		}
	}
	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode acceptance evidence: %w", err)
	}
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("save acceptance evidence: %w", err)
	}
	result.Artifact = path
	return nil
}
