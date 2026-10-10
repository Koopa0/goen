package layoutcheck_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	explicitLayoutChrome = "/explicit/chrome/for-layout-check"
	macosChromeBundle    = `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`
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

	assertCheckLayoutDryRunPropagatesChrome(t, ctx, root)

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

	for _, scenario := range []string{"normal-return", "canceled-run"} {
		t.Run(scenario, func(t *testing.T) {
			binDir := t.TempDir()
			t.Run("probe", func(t *testing.T) {
				probeResolvedChrome(t, binDir, scenario)
			})
			assertLayoutChromeExited(t, binDir)
		})
	}
}

func probeResolvedChrome(t *testing.T, binDir, scenario string) {
	t.Helper()

	ctx := t.Context()
	root := repoRoot(t)

	launchLog := filepath.Join(binDir, "launch.log")
	fakeChrome := filepath.Join(binDir, "google-chrome-stable")
	//nolint:gosec // G306: test fixture must be executable
	if err := os.WriteFile(fakeChrome, []byte(fmt.Sprintf(`#!/bin/sh
printf %%s "$$" > %q
printf %%s "$0" > %q
exec sleep 3600
`, filepath.Join(binDir, "fixture.pid"), launchLog)), 0o755); err != nil {
		t.Fatalf("write fake chrome: %v", err)
	}

	refusedFetch := filepath.Join(binDir, "refused-fetch")
	//nolint:gosec // G306: the executable fixture prevents network access after browser launch
	if err := os.WriteFile(filepath.Join(binDir, "curl"), []byte(fmt.Sprintf(`#!/bin/sh
for arg do
    case "$arg" in
        *axe.min.js) : > %q; exit 1 ;;
    esac
done
exec /usr/bin/curl "$@"
`, refusedFetch)), 0o755); err != nil {
		t.Fatalf("write controlled curl refusal: %v", err)
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
	// Canceling only make skips the wrapper's trap; the controlled fetch refusal
	// finishes the recipe while its owner can still read the browser PID.
	cmd.Cancel = nil
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(envWithoutChrome(os.Environ()),
		"PATH="+binDir+":/usr/bin",
		"GOEN_DATABASE_URL=postgres://layout-check-test.invalid/db",
		"LAYOUT_DIR="+t.TempDir(),
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start make check-layout: %v", err)
	}
	t.Cleanup(func() {
		waitErr := cmd.Wait()
		if err, ok := errors.AsType[*exec.ExitError](waitErr); !ok || err.ExitCode() != 2 {
			t.Errorf("make check-layout exit = %v, want the controlled recipe failure", waitErr)
		}
		if _, err := os.Stat(refusedFetch); err != nil {
			t.Errorf("controlled axe fetch refusal was not reached: %v", err)
		}
	})

	launchCtx, stopLaunch := context.WithTimeout(ctx, 10*time.Second)
	defer stopLaunch()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, statErr := os.Stat(launchLog); statErr == nil {
			break
		}
		select {
		case <-launchCtx.Done():
			t.Fatalf("check-layout never launched the resolved browser: %v", launchCtx.Err())
		case <-ticker.C:
		}
	}

	if scenario == "canceled-run" {
		cancel()
		return
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

func assertLayoutChromeExited(t *testing.T, binDir string) {
	t.Helper()
	//nolint:gosec // G304: the PID is recorded by this test's fixture under t.TempDir
	body, err := os.ReadFile(filepath.Join(binDir, "fixture.pid"))
	if err != nil {
		t.Fatalf("read fixture PID: %v", err)
	}
	pid, err := strconv.Atoi(string(body))
	if err != nil || pid <= 0 {
		t.Fatalf("fixture PID = %q, want a positive integer: %v", body, err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("find fixture PID %d: %v", pid, err)
	}
	t.Cleanup(func() {
		// A failing reproducer must not leave its own one-hour sleeper behind.
		if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
			t.Errorf("kill recorded fixture PID %d: %v", pid, err)
		}
		if err := process.Release(); err != nil {
			t.Errorf("release recorded fixture PID %d: %v", pid, err)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := process.Signal(syscall.Signal(0))
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("probe recorded fixture PID %d: %v", pid, err)
		}
		if runtime.GOOS == "linux" && linuxLayoutChromeExited(t, pid) {
			return
		}
		select {
		case <-ctx.Done():
			t.Errorf("fixture PID %d is still running after probe cleanup, want the recorded process gone", pid)
			return
		case <-ticker.C:
		}
	}
}

func linuxLayoutChromeExited(t *testing.T, pid int) bool {
	t.Helper()
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		t.Fatalf("read recorded fixture PID %d state: %v", pid, err)
	}
	// The parenthesized process name can itself contain spaces and parentheses.
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		t.Fatalf("recorded fixture PID %d stat has no process name terminator: %q", pid, stat)
	}
	fields := bytes.Fields(stat[end+1:])
	if len(fields) == 0 || len(fields[0]) != 1 {
		t.Fatalf("recorded fixture PID %d stat has no process state: %q", pid, stat)
	}
	// Signal 0 still succeeds for an exited zombie when PID 1 does not reap it.
	return fields[0][0] == 'Z'
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

func assertCheckLayoutDryRunPropagatesChrome(t *testing.T, ctx context.Context, root string) {
	t.Helper()

	layoutDir := t.TempDir()
	// Pin CHROME so a Darwin host whose resolver correctly names the app
	// bundle is not treated as a macOS-only Makefile.
	//nolint:gosec // G204: the arguments are test constants and t.TempDir()
	cmd := exec.CommandContext(ctx, "make", "-n", "check-layout", "CHROME="+explicitLayoutChrome, "LAYOUT_DIR="+layoutDir)
	cmd.Dir = root
	cmd.Env = envWithoutChrome(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n check-layout: %v\n%s", err, out)
	}
	dryRun := string(out)
	if !strings.Contains(dryRun, "--user-data-dir="+layoutDir) {
		t.Fatalf("make -n check-layout did not put the browser profile under LAYOUT_DIR %q:\n%s", layoutDir, dryRun)
	}
	if strings.Contains(dryRun, "$$CHROME") {
		t.Fatalf("make -n check-layout still launches through a per-line shell variable:\n%s", dryRun)
	}
	if !strings.Contains(dryRun, `test -x "`+explicitLayoutChrome+`"`) {
		t.Fatalf("make -n check-layout did not carry the explicit CHROME path:\n%s", dryRun)
	}
	if strings.Contains(dryRun, `test -x "`+macosChromeBundle+`"`) {
		t.Fatalf("make -n check-layout still probes only the macOS app bundle:\n%s", dryRun)
	}
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
	explicitChrome.Env = append(os.Environ(), "CHROME="+explicitLayoutChrome)
	got, err := explicitChrome.Output()
	if err != nil {
		t.Fatalf("resolve-chrome.sh with CHROME set: %v", err)
	}
	if !bytes.Equal(got, []byte(explicitLayoutChrome+"\n")) {
		t.Fatalf("resolve-chrome.sh = %q, want explicit CHROME respected", strings.TrimSpace(string(got)))
	}
}
