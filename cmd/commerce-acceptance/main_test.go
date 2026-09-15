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
