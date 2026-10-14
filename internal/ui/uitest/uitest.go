// Package uitest is shared support for tests of rendered pages: the one way
// a browser test learns it may run, and (later) helpers that read rendered
// HTML. Only test files import it.
package uitest

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Browser returns the name and value of the first of vars that is set in the
// environment, and skips the test when none is: a browser test runs only where
// CI or the developer provides a browser. The skip message names every
// variable, so the reason is the same everywhere.
func Browser(tb testing.TB, vars ...string) (name, value string) {
	tb.Helper()
	if len(vars) == 0 {
		tb.Fatal("uitest.Browser needs at least one environment variable name")
		return "", ""
	}
	for _, candidate := range vars {
		if found := os.Getenv(candidate); found != "" {
			return candidate, found
		}
	}
	tb.Skipf("set %s to run this browser test", strings.Join(vars, " or "))
	return "", ""
}

// LocalChrome returns the path of a Chrome binary found by
// scripts/resolve-chrome.sh, and skips the test under -short, without Chrome,
// or without node on PATH.
func LocalChrome(tb testing.TB) string {
	tb.Helper()
	if testing.Short() {
		tb.Skip("needs Chrome and Node")
	}
	resolver := exec.CommandContext(tb.Context(), "./scripts/resolve-chrome.sh")
	resolver.Dir = repoRoot(tb)
	chrome, err := resolver.Output()
	if err != nil {
		tb.Skip("Chrome unavailable")
	}
	if _, err := exec.LookPath("node"); err != nil {
		tb.Skip("Node unavailable")
	}
	return string(bytes.TrimSpace(chrome))
}

// repoRoot walks up from the working directory to the directory holding
// go.mod, so callers at any depth find scripts/.
func repoRoot(tb testing.TB) string {
	tb.Helper()
	dir, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatal("go.mod not found above the working directory")
		}
		dir = parent
	}
}
