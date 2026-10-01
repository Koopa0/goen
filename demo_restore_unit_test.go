package readme_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRestoreUnitRunsTheDocumentedScript keeps the systemd unit, the README's
// install path and the committed script on one path. A unit that points at a
// file the host does not have fails with status=203/EXEC, and no other gate
// reads the unit.
func TestRestoreUnitRunsTheDocumentedScript(t *testing.T) {
	t.Parallel()

	const checkout = "/opt/goen/"

	unit, err := os.ReadFile("deploy/demo/goen-restore.service")
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	var execStart string
	for line := range strings.SplitSeq(string(unit), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart="); ok {
			execStart = v
		}
	}
	if execStart == "" {
		t.Fatal("goen-restore.service has no ExecStart")
	}

	rel, ok := strings.CutPrefix(execStart, checkout)
	if !ok {
		t.Fatalf("ExecStart %q is not under the checkout %q", execStart, checkout)
	}
	if _, statErr := os.Stat(filepath.FromSlash(rel)); statErr != nil {
		t.Errorf("ExecStart %q resolves to %q in the repository: %v", execStart, rel, statErr)
	}

	readme, err := os.ReadFile("deploy/demo/README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if !strings.Contains(string(readme), "`"+execStart+"`") {
		t.Errorf("deploy/demo/README.md does not state the install path %q", execStart)
	}
}
