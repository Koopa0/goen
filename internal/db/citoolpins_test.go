package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// makefilePin matches a version the Makefile pins, e.g. `SQUAWK_VERSION := 2.64.0`.
var makefilePin = regexp.MustCompile(`(?m)^([A-Z_]+_VERSION)\s*:=\s*(\S+)\s*$`)

// TestTheWorkflowReadsItsToolPinsFromTheMakefile refuses a version repeated in
// the CI workflow.
//
// The Makefile pins each tool and its target version-checks against that pin, so
// a workflow that installs its own number is a second statement of one fact. It
// drifted the first time the Makefile's squawk moved: CI installed 2.63.0
// against a Makefile demanding 2.64.0, and every push failed on a version check
// rather than on anything about the code.
func TestTheWorkflowReadsItsToolPinsFromTheMakefile(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	//nolint:gosec // G304: the repository root joined to a fixed name
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("read the Makefile: %v", err)
	}

	pins := map[string]string{}
	for _, m := range makefilePin.FindAllStringSubmatch(string(makefile), -1) {
		pins[m[1]] = m[2]
	}
	if len(pins) < 5 {
		t.Fatalf("found %d pinned versions in the Makefile; the parser is not reading it", len(pins))
	}

	workflows, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatalf("glob the workflows: %v", err)
	}
	if len(workflows) == 0 {
		t.Fatal("no workflows found; this guard has no subject")
	}

	var repeated []string
	for _, path := range workflows {
		body, readErr := os.ReadFile(path) //nolint:gosec // paths come from a fixed glob
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(body), "\n") {
			// A line that names the variable is the shape this guard wants,
			// and a comment is prose rather than an install command.
			trimmed := strings.TrimSpace(line)
			if strings.Contains(line, "_VERSION") || strings.HasPrefix(trimmed, "#") {
				continue
			}
			for name, version := range pins {
				if strings.Contains(line, version) {
					repeated = append(repeated,
						rel+":"+strconv.Itoa(i+1)+"  "+trimmed+
							"    (Makefile pins it as "+name+")")
				}
			}
		}
	}

	if len(repeated) > 0 {
		sort.Strings(repeated)
		t.Errorf("%d workflow line(s) repeat a version the Makefile pins:\n  %s\n\n"+
			"Read it instead:\n"+
			"  version=$(awk -F':= *' '$1 ~ /^NAME_VERSION/ { print $2; exit }' Makefile)",
			len(repeated), strings.Join(repeated, "\n  "))
	}
}
