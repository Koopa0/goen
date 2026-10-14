package main

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// goTestList runs go test -list in a directory with no build tags and returns
// the names it prints.
func goTestList(root, dir string) ([]string, error) {
	cmd := exec.CommandContext(context.Background(), "go", "test", "-list", ".", "./"+dir) //nolint:gosec // G204: the directory is a map row's.
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	var names []string
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "ok ") || strings.HasPrefix(line, "?") || strings.HasPrefix(line, "FAIL") {
			continue
		}
		names = append(names, line)
	}
	slices.Sort(names)
	return names, nil
}
