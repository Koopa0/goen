package layoutcheck_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
	root := repoRoot(t)
	body := readMakefile(t, root)
	if strings.Contains(body, "CHROME ?= /Applications/Google Chrome.app") {
		t.Fatal("the Makefile hard-codes a macOS-only CHROME default; use scripts/resolve-chrome.sh")
	}
	if !strings.Contains(body, "scripts/resolve-chrome.sh") {
		t.Fatal("check-layout must resolve Chrome through scripts/resolve-chrome.sh")
	}
	if !strings.Contains(body, "LAYOUT_CHROME") {
		t.Fatal("check-layout must carry the resolved browser path in a target variable")
	}
	if strings.Contains(body, `@export CHROME=$$(scripts/resolve-chrome.sh)`) {
		t.Fatal("check-layout must not resolve Chrome in a recipe line that a later line cannot see")
	}

	cmd := exec.CommandContext(ctx, "make", "-n", "check-layout")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n check-layout: %v\n%s", err, out)
	}
	dryRun := string(out)
	if strings.Contains(dryRun, "$$CHROME") {
		t.Fatalf("make -n check-layout still launches through a per-line shell variable:\n%s", dryRun)
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

// TestCheckLayoutLaunchesTheResolvedChrome proves the browser make actually
// execs is the path the resolver chose, not merely named in a dry-run.
func TestCheckLayoutLaunchesTheResolvedChrome(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the google-chrome-stable probe ordering is exercised on Linux")
	}

	ctx := t.Context()
	root := repoRoot(t)
	t.Cleanup(func() { os.RemoveAll(filepath.Join(root, ".layout-chrome")) })

	binDir := t.TempDir()
	launchLog := filepath.Join(binDir, "launch.log")
	fakeChrome := filepath.Join(binDir, "google-chrome-stable")
	//nolint:gosec // G306: test fixture must be executable
	if err := os.WriteFile(fakeChrome, []byte(fmt.Sprintf(`#!/bin/sh
printf %%s "$0" > %q
exec sleep 3600
`, launchLog)), 0o755); err != nil {
		t.Fatalf("write fake chrome: %v", err)
	}

	var lc net.ListenConfig
	listener, listenErr := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatalf("listen for layout server stub: %v", listenErr)
	}
	t.Cleanup(func() { listener.Close() })

	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })

	goenURL := "http://" + listener.Addr().String()

	makePath, lookErr := exec.LookPath("make")
	if lookErr != nil {
		t.Fatalf("find make: %v", lookErr)
	}

	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	t.Cleanup(cancel)

	//nolint:gosec // G204: makePath comes from exec.LookPath("make")
	cmd := exec.CommandContext(runCtx, makePath, "check-layout", "GOEN_URL="+goenURL)
	cmd.Dir = root
	// binDir must precede /usr/bin so the resolver picks the fake chrome, while
	// /usr/bin still supplies curl for the server probe recipe line.
	cmd.Env = append(envWithoutChrome(os.Environ()),
		"PATH="+binDir+":/usr/bin",
		"GOEN_DATABASE_URL=postgres://layout-check-test.invalid/db",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start make check-layout: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(launchLog); statErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	//nolint:gosec // G304: launchLog is under t.TempDir()
	got, readErr := os.ReadFile(launchLog)
	if readErr != nil {
		t.Fatalf("check-layout never launched the resolved browser: %v", readErr)
	}
	want, wantErr := filepath.EvalSymlinks(fakeChrome)
	if wantErr != nil {
		t.Fatalf("resolve fake chrome path: %v", wantErr)
	}
	if strings.TrimSpace(string(got)) != want {
		t.Fatalf("launch argv[0] = %q, want %q", strings.TrimSpace(string(got)), want)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..")
}

func readMakefile(t *testing.T, root string) string {
	t.Helper()
	makefilePath := filepath.Join(root, "Makefile")
	//nolint:gosec // G304: the repository root joined to a fixed name
	makefile, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read the Makefile: %v", err)
	}
	return string(makefile)
}

func envWithoutChrome(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, "CHROME=") {
			continue
		}
		out = append(out, entry)
	}
	return out
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
