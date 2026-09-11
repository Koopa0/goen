package layoutcheck_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestTheLayoutGateResolvesChromeAcrossPlatforms refuses a macOS-only CHROME
// default in the Makefile.
//
// check-layout sits outside verify because it needs a real browser; a default
// that only names the macOS app bundle makes the documented gate unrunnable on
// Linux CI and on machines where Chromium is google-chrome-stable.
func TestTheLayoutGateResolvesChromeAcrossPlatforms(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	root := filepath.Join("..", "..")
	makefilePath := filepath.Join(root, "Makefile")
	//nolint:gosec // G304: the repository root joined to a fixed name
	makefile, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read the Makefile: %v", err)
	}
	body := string(makefile)
	if strings.Contains(body, "CHROME ?= /Applications/Google Chrome.app") {
		t.Fatal("the Makefile hard-codes a macOS-only CHROME default; use scripts/resolve-chrome.sh")
	}
	if !strings.Contains(body, "scripts/resolve-chrome.sh") {
		t.Fatal("check-layout must resolve Chrome through scripts/resolve-chrome.sh")
	}

	cmd := exec.CommandContext(ctx, "make", "-n", "check-layout")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n check-layout: %v\n%s", err, out)
	}
	dryRun := string(out)
	if !strings.Contains(dryRun, "scripts/resolve-chrome.sh") {
		t.Fatalf("make -n check-layout does not name the resolver:\n%s", dryRun)
	}
	if strings.Contains(dryRun, `test -x "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"`) {
		t.Fatalf("make -n check-layout still probes only the macOS app bundle:\n%s", dryRun)
	}

	resolver := filepath.Join(root, "scripts", "resolve-chrome.sh")
	if runtime.GOOS == "linux" {
		assertLinuxChromeResolver(t, ctx, resolver)
	}
	assertExplicitChromeIsRespected(t, ctx, resolver)
}

func assertLinuxChromeResolver(t *testing.T, ctx context.Context, resolver string) {
	t.Helper()

	got, err := exec.CommandContext(ctx, resolver).Output()
	if err != nil {
		t.Fatalf("resolve-chrome.sh on Linux: %v", err)
	}
	chrome := strings.TrimSpace(string(got))
	if chrome == "" {
		t.Fatal("resolve-chrome.sh returned an empty path on Linux")
	}
	if strings.Contains(chrome, "/Applications/Google Chrome.app") {
		t.Fatalf("resolve-chrome.sh on Linux named the macOS bundle: %q", chrome)
	}
	if _, statErr := os.Stat(chrome); statErr != nil {
		t.Fatalf("resolve-chrome.sh named %q, which is not present: %v", chrome, statErr)
	}
}

func assertExplicitChromeIsRespected(t *testing.T, ctx context.Context, resolver string) {
	t.Helper()

	explicitChrome := exec.CommandContext(ctx, resolver)
	explicitChrome.Env = append(os.Environ(), "CHROME=/explicit/chrome/for-layout-check")
	got, err := explicitChrome.Output()
	if err != nil {
		t.Fatalf("resolve-chrome.sh with CHROME set: %v", err)
	}
	if !bytes.Equal(got, []byte("/explicit/chrome/for-layout-check\n")) {
		t.Fatalf("resolve-chrome.sh = %q, want explicit CHROME respected", strings.TrimSpace(string(got)))
	}
}
