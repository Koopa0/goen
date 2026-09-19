// Package acceptance publishes the executable commerce scenario suite from #331.
package acceptance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Status is whether a scenario or extension can run in this environment.
type Status string

const (
	StatusReady   Status = "ready"
	StatusBlocked Status = "blocked"
)

// AssertionKind names how evidence is collected.
type AssertionKind string

const (
	AssertionGoTest  AssertionKind = "go_test"
	AssertionBrowser AssertionKind = "browser"
	AssertionGate    AssertionKind = "gate"
)

// Assertion is one executable or documented evidence hook.
type Assertion struct {
	Kind       AssertionKind `json:"kind"`
	Package    string        `json:"package,omitempty"`
	Run        string        `json:"run,omitempty"`
	BuildTags  []string      `json:"build_tags,omitempty"`
	Runner     string        `json:"runner,omitempty"`
	Pages      []string      `json:"pages,omitempty"`
	Evidence   string        `json:"evidence"`
	Status     Status        `json:"status,omitempty"`
	EntryPoint string        `json:"entry_point,omitempty"`
}

// Extension is post-capture composition tracked separately until its issue lands.
type Extension struct {
	Issues   []int  `json:"issues"`
	Title    string `json:"title"`
	Status   Status `json:"status"`
	Evidence string `json:"evidence"`
}

// Scenario is one C01–C16 commerce journey row from #331.
type Scenario struct {
	ID           string      `json:"id"`
	Title        string      `json:"title"`
	Status       Status      `json:"status"`
	Issues       []int       `json:"issues"`
	BlockedBy    []int       `json:"blocked_by,omitempty"`
	EntryPoints  []string    `json:"entry_points"`
	Fixture      string      `json:"fixture,omitempty"`
	Fault        string      `json:"fault"`
	Assertions   []Assertion `json:"assertions"`
	Extensions   []Extension `json:"extensions,omitempty"`
	Dependencies []int       `json:"dependencies,omitempty"`
}

// Manifest is the machine-readable projection of #331 scenario work.
type Manifest struct {
	Epic      int        `json:"epic"`
	Scenarios []Scenario `json:"scenarios"`
}

// RequiredScenarioIDs is the complete C01–C16 matrix from #331.
var RequiredScenarioIDs = []string{
	"C01", "C02", "C03", "C04", "C05", "C06", "C07", "C08",
	"C09", "C10", "C11", "C12", "C13", "C14", "C15", "C16",
}

// ManifestPath returns the repository manifest location.
func ManifestPath() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("acceptance: caller frame unavailable")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(root, "acceptance", "manifest.json"), nil
}

// LoadManifest reads and decodes acceptance/manifest.json.
func LoadManifest() (Manifest, error) {
	path, err := ManifestPath()
	if err != nil {
		return Manifest{}, err
	}
	body, err := os.ReadFile(path) //nolint:gosec // path is derived from the package location.
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	return manifest, nil
}

// Validate reports structural problems with the manifest.
func Validate(m Manifest) error {
	if m.Epic != 331 {
		return fmt.Errorf("epic = %d, want 331", m.Epic)
	}
	seen := map[string]bool{}
	for _, id := range RequiredScenarioIDs {
		seen[id] = false
	}
	for i := range m.Scenarios {
		if err := validateScenario(&m.Scenarios[i], seen); err != nil {
			return err
		}
	}
	for _, id := range RequiredScenarioIDs {
		if !seen[id] {
			return fmt.Errorf("missing required scenario %s", id)
		}
	}
	return nil
}

func validateScenario(scenario *Scenario, seen map[string]bool) error {
	if !slices.Contains(RequiredScenarioIDs, scenario.ID) {
		return fmt.Errorf("unknown scenario id %q", scenario.ID)
	}
	if seen[scenario.ID] {
		return fmt.Errorf("duplicate scenario id %q", scenario.ID)
	}
	seen[scenario.ID] = true
	if scenario.Title == "" {
		return fmt.Errorf("%s: title is required", scenario.ID)
	}
	if scenario.Status != StatusReady && scenario.Status != StatusBlocked {
		return fmt.Errorf("%s: status %q is not ready or blocked", scenario.ID, scenario.Status)
	}
	if len(scenario.Assertions) == 0 {
		return fmt.Errorf("%s: at least one assertion is required", scenario.ID)
	}
	for i := range scenario.Assertions {
		if err := validateAssertion(scenario.ID, i, &scenario.Assertions[i]); err != nil {
			return err
		}
	}
	for i := range scenario.Extensions {
		extension := &scenario.Extensions[i]
		if extension.Status != StatusBlocked {
			return fmt.Errorf("%s: extension %q must stay blocked until its issue lands", scenario.ID, extension.Title)
		}
	}
	return nil
}

func validateAssertion(scenarioID string, index int, assertion *Assertion) error {
	label := fmt.Sprintf("%s assertion[%d]", scenarioID, index)
	status := assertion.Status
	if status == "" {
		status = StatusReady
	}
	if status != StatusReady && status != StatusBlocked {
		return fmt.Errorf("%s: status %q is not ready or blocked", label, assertion.Status)
	}
	if assertion.Evidence == "" {
		return fmt.Errorf("%s: evidence is required", label)
	}
	switch assertion.Kind {
	case AssertionGoTest:
		if assertion.Package == "" || assertion.Run == "" {
			return fmt.Errorf("%s: go_test needs package and run", label)
		}
	case AssertionBrowser:
		if assertion.Runner == "" || len(assertion.Pages) == 0 {
			return fmt.Errorf("%s: browser needs runner and pages", label)
		}
	case AssertionGate:
		if status != StatusBlocked {
			return fmt.Errorf("%s: gate assertions must be blocked", label)
		}
	default:
		return fmt.Errorf("%s: unknown kind %q", label, assertion.Kind)
	}
	return nil
}

// ByID returns one scenario or an error.
func (m Manifest) ByID(id string) (Scenario, error) {
	for i := range m.Scenarios {
		if m.Scenarios[i].ID == id {
			return m.Scenarios[i], nil
		}
	}
	return Scenario{}, fmt.Errorf("scenario %s is not in the manifest", id)
}

// ReadyAssertions returns executable go_test hooks for a scenario.
func (s *Scenario) ReadyAssertions() []Assertion {
	var ready []Assertion
	for i := range s.Assertions {
		assertion := &s.Assertions[i]
		if assertion.Kind != AssertionGoTest {
			continue
		}
		status := assertion.Status
		if status == "" {
			status = StatusReady
		}
		if status == StatusReady && s.Status == StatusReady {
			ready = append(ready, *assertion)
		}
	}
	return ready
}

// BrowserAssertions returns browser evidence hooks for a scenario.
func (s *Scenario) BrowserAssertions() []Assertion {
	var browser []Assertion
	for i := range s.Assertions {
		assertion := &s.Assertions[i]
		if assertion.Kind != AssertionBrowser {
			continue
		}
		status := assertion.Status
		if status == "" {
			status = StatusReady
		}
		if status == StatusReady && s.Status == StatusReady {
			browser = append(browser, *assertion)
		}
	}
	return browser
}

// SummaryLine formats one scenario for listing.
func (s *Scenario) SummaryLine() string {
	blocked := ""
	if len(s.BlockedBy) > 0 {
		parts := make([]string, len(s.BlockedBy))
		for i, issue := range s.BlockedBy {
			parts[i] = fmt.Sprintf("#%d", issue)
		}
		blocked = " blocked_by=" + strings.Join(parts, ",")
	}
	return fmt.Sprintf("%s %s status=%s assertions=%d extensions=%d%s",
		s.ID, s.Title, s.Status, len(s.Assertions), len(s.Extensions), blocked)
}
