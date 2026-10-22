package uitest_test

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/ui/uitest"
)

// recorder is a testing.TB whose Fatal and Skip record instead of stopping,
// so the paths that end a real test can be asserted. Browser returns right
// after each, so continuing past them is safe.
type recorder struct {
	testing.TB

	fatal, skip string
}

func (r *recorder) Helper() {}

func (r *recorder) Fatal(args ...any) {
	var b strings.Builder
	for _, a := range args {
		b.WriteString(a.(string))
	}
	r.fatal = b.String()
}

func (r *recorder) Skipf(format string, args ...any) {
	r.skip = fmt.Sprintf(format, args...)
}

func TestBrowserSkipsNamingEveryVariable(t *testing.T) {
	t.Setenv("UITEST_FIRST", "")
	t.Setenv("UITEST_SECOND", "")
	rec := &recorder{TB: t}
	name, value := uitest.Browser(rec, "UITEST_FIRST", "UITEST_SECOND")
	if name != "" || value != "" {
		t.Errorf("returned %q, %q when nothing is set", name, value)
	}
	if want := "set UITEST_FIRST or UITEST_SECOND to run this browser test"; rec.skip != want {
		t.Errorf("skip message = %q, want %q", rec.skip, want)
	}
	if rec.fatal != "" {
		t.Errorf("fatal = %q, want a skip", rec.fatal)
	}
}

func TestBrowserSkipsTheSubtestNotTheParent(t *testing.T) {
	t.Setenv("UITEST_FIRST", "")
	var skipped bool
	t.Run("unset", func(t *testing.T) {
		defer func() { skipped = t.Skipped() }()
		uitest.Browser(t, "UITEST_FIRST")
		t.Error("Browser returned for an unset variable")
	})
	if !skipped {
		t.Error("the subtest was not skipped")
	}
}

func TestBrowserReturnsTheSetVariable(t *testing.T) {
	t.Setenv("UITEST_FIRST", "")
	t.Setenv("UITEST_SECOND", "9222")
	name, value := uitest.Browser(t, "UITEST_FIRST", "UITEST_SECOND")
	if name != "UITEST_SECOND" || value != "9222" {
		t.Errorf("got %q, %q, want UITEST_SECOND, 9222", name, value)
	}
}

func TestBrowserPrefersTheFirstSetVariable(t *testing.T) {
	t.Setenv("UITEST_FIRST", "one")
	t.Setenv("UITEST_SECOND", "two")
	name, value := uitest.Browser(t, "UITEST_FIRST", "UITEST_SECOND")
	if name != "UITEST_FIRST" || value != "one" {
		t.Errorf("got %q, %q, want UITEST_FIRST, one", name, value)
	}
}

// TestBrowserNoVariables: calling Browser without variables is a programming
// error.
func TestBrowserNoVariables(t *testing.T) {
	rec := &recorder{TB: t}
	uitest.Browser(rec)
	if rec.fatal == "" {
		t.Error("Browser with no variables did not fail")
	}
	if rec.skip != "" {
		t.Errorf("skip = %q, want a failure, not a skip", rec.skip)
	}
}

// TestLocalChromeResolverPath: LocalChrome returns the trimmed path from the
// resolver.
func TestLocalChromeResolverPath(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH")
	}
	t.Setenv("CHROME", "/opt/uitest/chrome")
	// Found from this package's directory, which is not the repository root.
	if got := uitest.LocalChrome(t); got != "/opt/uitest/chrome" {
		t.Errorf("LocalChrome = %q, want /opt/uitest/chrome", got)
	}
}
