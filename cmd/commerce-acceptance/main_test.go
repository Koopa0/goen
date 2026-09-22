package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

var captureMu sync.Mutex

func captureRun(t *testing.T, args []string) (code int, stdout, stderr string) {
	t.Helper()
	captureMu.Lock()
	defer captureMu.Unlock()
	prevOut, prevErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = outW
	os.Stderr = outW
	code = run(args)
	_ = outW.Close()
	os.Stdout = prevOut
	os.Stderr = prevErr
	combined, readErr := io.ReadAll(outR)
	if readErr != nil {
		t.Fatal(readErr)
	}
	text := string(combined)
	return code, text, text
}

func TestRunBlockedScenarioFailsClosed(t *testing.T) {
	t.Parallel()
	code, stdout, _ := captureRun(t, []string{"run", "C02"})
	if code == 0 {
		t.Fatalf("run C02 exited %d, want failure; output=%q", code, stdout)
	}
	if !strings.Contains(stdout, "C02 BLOCKED") {
		t.Fatalf("run C02 output = %q, want blocked status line", stdout)
	}
	if strings.Contains(stdout, "PASS") {
		t.Fatalf("run C02 reported PASS: %q", stdout)
	}
}

func TestRunBlockedScenarioWithAllAndReadyOnlyIsRejected(t *testing.T) {
	t.Parallel()
	code, _, stderr := captureRun(t, []string{"run", "--all", "--ready-only", "C02"})
	if code != 2 {
		t.Fatalf("contradictory flags exited %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "contradict") {
		t.Fatalf("stderr = %q, want contradiction message", stderr)
	}
}

func TestRunBlockedScenarioInAllModeFailsClosed(t *testing.T) {
	t.Parallel()
	code, stdout, _ := captureRun(t, []string{"run", "--all", "C02"})
	if code == 0 {
		t.Fatalf("run --all C02 exited %d, want failure; output=%q", code, stdout)
	}
	if !strings.Contains(stdout, "C02 BLOCKED") {
		t.Fatalf("run --all C02 output = %q, want blocked status line", stdout)
	}
}

func TestRunBlockedScenarioDoesNotReportSuccessWithoutResults(t *testing.T) {
	captureMu.Lock()
	defer captureMu.Unlock()
	prevOut := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := run([]string{"run", "C02"})
	_ = w.Close()
	os.Stdout = prevOut
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	out := buf.String()
	if code == 0 {
		t.Fatalf("run C02 exited success with output %q", out)
	}
	if strings.TrimSpace(strings.ReplaceAll(out, "elapsed 0s", "")) == "" {
		t.Fatalf("run C02 printed only elapsed time: %q", out)
	}
}

func TestRunFlagsFollowTheSelectedScenario(t *testing.T) {
	for _, args := range [][]string{
		{"run", "C02", "--all", "--ready-only"},
		{"run", "--all", "C02", "--ready-only"},
	} {
		code, _, stderr := captureRun(t, args)
		if code != 2 || !strings.Contains(stderr, "contradict") {
			t.Fatalf("%v = %d, %q; want usage error for contradictory flags", args, code, stderr)
		}
	}
}

func TestRunRejectsIgnoredTrailingArguments(t *testing.T) {
	for _, args := range [][]string{
		{"run", "C02", "C03"},
		{"run", "C02", "--unknown"},
		{"run", "C02", "--", "--with-browser"},
	} {
		code, _, stderr := captureRun(t, args)
		if code != 2 {
			t.Fatalf("%v = %d, %q; want usage error", args, code, stderr)
		}
	}
}

func TestRunSelectionPreservesBrowserFlagsAndCanonicalAll(t *testing.T) {
	for _, test := range []struct {
		args                          []string
		target                        string
		browser, explicit, ready, all bool
	}{
		{args: []string{"C01", "--with-browser"}, target: "C01", browser: true, explicit: true, ready: true},
		{args: []string{"--with-browser", "c01"}, target: "C01", browser: true, explicit: true, ready: true},
		{args: []string{"C01", "--with-browser=false"}, target: "C01", explicit: true, ready: true},
		{args: []string{"--ready-only", "all"}, target: "all", ready: true},
		{args: []string{"ALL", "--ready-only"}, target: "all", ready: true},
		{args: []string{"All", "--all"}, target: "all", all: true},
		{args: []string{"--all"}, target: "all", all: true},
		{args: []string{"--", "C02"}, target: "C02", explicit: true, ready: true},
	} {
		opts, all, err := parseRunArgs(test.args)
		if err != nil {
			t.Fatalf("%v: %v", test.args, err)
		}
		if opts.ScenarioID != test.target || opts.WithBrowser != test.browser || opts.ExplicitSelection != test.explicit || opts.ReadyOnly != test.ready || all != test.all {
			t.Errorf("%v = %+v all=%t; want target=%s browser=%t explicit=%t ready=%t all=%t", test.args, opts, all, test.target, test.browser, test.explicit, test.ready, test.all)
		}
	}
}
