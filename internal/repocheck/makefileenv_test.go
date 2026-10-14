package repocheck_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestMakefileDotEnvNeverOverridesTheCaller: a value the caller names, on the
// make command line or in the environment, beats .env. `include .env` made it
// the other way round, so `GOEN_DATABASE_URL=<other> make db-reset` rebuilt the
// database in .env while the command line named another.
//
// It runs the real Makefile, from a directory holding a fixture .env, beside a
// probe target that prints what a recipe's shell would see. The four cases are
// the ones that matter: only .env, the environment, the command line, both.
func TestMakefileDotEnvNeverOverridesTheCaller(t *testing.T) {
	t.Parallel()

	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skipf("make is not installed: %v", err)
	}
	makefile := filepath.Join(repoRoot(t), "Makefile")

	const (
		fromDotenv = "postgres://dotenv.example/goen?sslmode=disable&pw=$1"
		fromShell  = "postgres://shell.example/goen"
		fromCmd    = "postgres://cmd.example/goen"
	)
	const dotenv = "# a comment\n" +
		"GOEN_DATABASE_URL=" + fromDotenv + "\n" +
		"GOEN_ADDR = 127.0.0.1:9999\n" +
		"GOEN_LOG_LEVEL=debug\n"
	const probe = "probe:\n\t@printf '%s|%s|%s' \"$$GOEN_DATABASE_URL\" \"$$GOEN_ADDR\" \"$$GOEN_LOG_LEVEL\"\n"

	tests := []struct {
		name   string
		dotenv bool
		env    []string
		args   []string
		want   string
	}{
		{name: "only .env", dotenv: true, want: fromDotenv + "|127.0.0.1:9999|debug"},
		{
			name: "the environment", dotenv: true, env: []string{"GOEN_DATABASE_URL=" + fromShell},
			want: fromShell + "|127.0.0.1:9999|debug",
		},
		{
			name: "the command line", dotenv: true, args: []string{"GOEN_DATABASE_URL=" + fromCmd},
			want: fromCmd + "|127.0.0.1:9999|debug",
		},
		{
			name: "both", dotenv: true, env: []string{"GOEN_DATABASE_URL=" + fromShell},
			args: []string{"GOEN_DATABASE_URL=" + fromCmd}, want: fromCmd + "|127.0.0.1:9999|debug",
		},
		{
			name:   "an empty value the caller set is kept, so a target that requires one stops",
			dotenv: true, env: []string{"GOEN_DATABASE_URL="}, want: "|127.0.0.1:9999|debug",
		},
		{
			name:   "another name in the environment does not hide the rest of .env",
			dotenv: true, env: []string{"GOEN_ADDR=127.0.0.1:1"}, want: fromDotenv + "|127.0.0.1:1|debug",
		},
		{name: "no .env", env: []string{"GOEN_DATABASE_URL=" + fromShell}, want: fromShell + "||"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if tt.dotenv {
				writeFile(t, filepath.Join(dir, ".env"), dotenv)
			}
			writeFile(t, filepath.Join(dir, "probe.mk"), probe)

			args := append([]string{"-s", "-f", makefile, "-f", "probe.mk", "probe"}, tt.args...)
			//nolint:gosec // G204: make is resolved on PATH; the arguments are the Makefile path and test-owned literals.
			cmd := exec.CommandContext(t.Context(), makeBin, args...)
			cmd.Dir = dir
			cmd.Env = append(cleanMakeEnv(os.Environ()), tt.env...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("make: %v\n%s", err, out)
			}
			if got := string(out); got != tt.want {
				t.Errorf("a recipe saw GOEN_DATABASE_URL|GOEN_ADDR|GOEN_LOG_LEVEL = %q, want %q", got, tt.want)
			}
		})
	}
}

// cleanMakeEnv drops what would leak from the make that runs this test, or from
// the developer's shell, into the one under test.
func cleanMakeEnv(env []string) []string {
	return slices.DeleteFunc(slices.Clone(env), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return strings.HasPrefix(name, "GOEN_") || name == "MAKEFLAGS" || name == "MFLAGS" ||
			name == "MAKELEVEL" || name == "MAKEOVERRIDES"
	})
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
