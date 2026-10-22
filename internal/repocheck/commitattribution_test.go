package repocheck_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func commitAttributionScript(t *testing.T) string {
	t.Helper()
	script, pathErr := filepath.Abs(filepath.Join(repoRoot(t), "scripts", "check-commit-attribution.sh"))
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	return script
}

func newCommitAttributionRepo(t *testing.T) (dir string, git func(...string) string) {
	t.Helper()
	dir = t.TempDir()
	git = func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // Arguments are test-owned literals and commit messages passed as data, without a shell.
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-q")
	git("config", "user.name", "message-test")
	git("config", "user.email", "message-test@goen.invalid")
	git("config", "commit.gpgsign", "false")
	git("config", "core.hooksPath", os.DevNull) // a developer's global commit-msg hook must not stop the fixtures from being created
	return dir, git
}

func runCommitAttribution(t *testing.T, dir, script, base, head string, env ...string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "bash", script, base, head) //nolint:gosec // Only the fixed repository script and test-owned commit SHAs execute.
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), env...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// aiTrailer is the form the history tests use for a dirty commit.
const aiTrailer = "Co-authored-by: Claude <noreply@anthropic.com>"

// dependabotMessage is the commit message of Dependabot's own commit in pull
// request 1774, copied verbatim. The check accepted it before human co-authors
// were allowed, so it must still pass: the bot is named in a Signed-off-by line.
const dependabotMessage = `build(deps): bump golang.org/x/sync from 0.23.0 to 0.24.0

Bumps [golang.org/x/sync](https://github.com/golang/sync) from 0.23.0 to 0.24.0.
- [Commits](https://github.com/golang/sync/compare/v0.23.0...v0.24.0)

---
updated-dependencies:
- dependency-name: golang.org/x/sync
  dependency-version: 0.24.0
  dependency-type: direct:production
  update-type: version-update:semver-minor
...

Signed-off-by: dependabot[bot] <support@github.com>`

// TestCommitAttributionForbiddenMessages holds the whole policy: a human
// co-author is credited with a well-formed trailer, and every way of naming an
// AI tool stays refused. Each case is a separate commit judged on its own.
func TestCommitAttributionForbiddenMessages(t *testing.T) {
	script := commitAttributionScript(t)
	dir, git := newCommitAttributionRepo(t)
	git("commit", "--allow-empty", "-qm", "test: clean base")
	base := git("rev-parse", "HEAD")
	const fix = "fix: preserve the form\n\n"
	for _, tc := range []struct {
		name    string
		message string
		wantRed bool
	}{
		{"clean", "fix: preserve the native form", false},

		// A human is credited with a Co-authored-by trailer.
		{"human co-author", fix + "Co-authored-by: Ada Lovelace <ada@example.com>", false},
		{"human co-author owner noreply", fix + "Co-authored-by: Koopa <Koopa0@users.noreply.github.com>", false},
		{"human co-author numbered noreply", fix + "Co-authored-by: Grace Hopper <1234+grace@users.noreply.github.com>", false},
		{"human co-author key in lower case", fix + "co-authored-by: Ada Lovelace <ada@example.com>", false},
		{"two human co-authors", fix + "Co-authored-by: Ada Lovelace <ada@example.com>\nCo-authored-by: Grace Hopper <grace@example.org>", false},
		{"human whose name contains a tool word", fix + "Co-authored-by: Jean-Claude Van Damme <jcvd@example.com>", false},
		{"human co-author beside a signoff", fix + "Signed-off-by: Ada Lovelace <ada@example.com>\nCo-authored-by: Grace Hopper <grace@example.org>", false},
		{"human co-author after a long body", fix + strings.Repeat("body\n", 20000) + "\nCo-authored-by: Ada Lovelace <ada@example.com>", false},

		// Automation names itself in a Signed-off-by line and claims no credit.
		{"dependabot commit as written", dependabotMessage, false},
		{"github-actions signoff", fix + "Signed-off-by: github-actions[bot] <41898282+github-actions[bot]@users.noreply.github.com>", false},
		{"renovate signoff", fix + "Signed-off-by: renovate[bot] <29139614+renovate[bot]@users.noreply.github.com>", false},
		{"dependabot signoff beside a human co-author", dependabotMessage + "\nCo-authored-by: Ada Lovelace <ada@example.com>", false},

		// A name that begins with a tool word was rejected before and stays rejected.
		{"human first name Claude", fix + "Co-authored-by: Claude Monet <monet@example.com>", true},
		{"human first name Claudette", fix + "Co-authored-by: Claudette Colbert <claudette@example.com>", true},

		// A trailer that is not exactly one name and one address is not a credit.
		{"missing address", fix + "Co-authored-by: Someone", true},
		{"missing name", fix + "Co-authored-by: <person@example.com>", true},
		{"no space before address", fix + "Co-authored-by: Someone<person@example.com>", true},
		{"case and spacing", fix + "  CO-AUTHORED-BY : Someone <person@example.com>", true},
		{"indented trailer", fix + "  Co-authored-by: Someone <person@example.com>", true},
		{"inline attribution", "fix: preserve the form Co-authored-by: Someone <person@example.com>", true},
		{"two addresses", fix + "Co-authored-by: Someone <a@example.com> <b@example.com>", true},
		{"co-committed-by", fix + "Co-committed-by: Ada Lovelace <ada@example.com>", true},
		{"large message", fix + "Co-authored-by: Someone\n" + strings.Repeat("body\n", 20000), true},
		{"human beside an AI co-author", fix + "Co-authored-by: Ada Lovelace <ada@example.com>\n" + aiTrailer, true},

		// AI co-authors, by name and by address.
		{"claude trailer", fix + aiTrailer, true},
		{"claude trailer lower case", fix + "co-authored-by: claude <noreply@anthropic.com>", true},
		{"claude model name", fix + "Co-Authored-By: Claude Opus 4 <noreply@anthropic.com>", true},
		{"claude name, human address", fix + "Co-authored-by: Claude <person@example.com>", true},
		{"human name, anthropic address", fix + "Co-authored-by: Ada Lovelace <noreply@anthropic.com>", true},
		{"human name, openai address", fix + "Co-authored-by: Ada Lovelace <noreply@openai.com>", true},
		{"codex trailer", fix + "Co-authored-by: Codex <codex@openai.com>", true},
		{"chatgpt trailer", fix + "Co-authored-by: ChatGPT <noreply@openai.com>", true},
		{"cursor trailer", fix + "Co-authored-by: Cursor Agent <cursoragent@cursor.com>", true},
		{"cursor name, human address", fix + "Co-authored-by: Cursor <person@example.com>", true},
		{"copilot trailer", fix + "Co-authored-by: Copilot <175728472+Copilot@users.noreply.github.com>", true},
		{"copilot name, human address", fix + "Co-authored-by: GitHub Copilot <person@example.com>", true},
		{"gemini trailer", fix + "Co-authored-by: Gemini 2.5 Pro <gemini@example.com>", true},
		{"grok trailer", fix + "Co-authored-by: Grok <grok@x.ai>", true},
		{"aider trailer", fix + "Co-authored-by: aider (gpt-4o) <noreply@aider.chat>", true},
		{"devin bot", fix + "Co-authored-by: devin-ai-integration[bot] <158243242+devin-ai-integration[bot]@users.noreply.github.com>", true},
		{"dependabot", fix + "Co-authored-by: dependabot[bot] <49699333+dependabot[bot]@users.noreply.github.com>", true},
		{"claude bot co-author", fix + "Co-authored-by: claude[bot] <209825114+claude[bot]@users.noreply.github.com>", true},
		{"claude bot signoff", fix + "Signed-off-by: claude[bot] <209825114+claude[bot]@users.noreply.github.com>", true},
		{"copilot agent bot signoff", fix + "Signed-off-by: copilot-swe-agent[bot] <198982749+Copilot@users.noreply.github.com>", true},
		{"copilot noreply address on a signoff", fix + "Signed-off-by: Someone <198982749+copilot@users.noreply.github.com>", true},
		{"bot named in a development trailer", fix + "Co-developed-by: some-tool[bot] <1+some-tool[bot]@users.noreply.github.com>", true},
		{"bot named in an assisted-by trailer", fix + "Assisted-by: some-tool[bot]", true},
		{"signoff by an AI name", fix + "Signed-off-by: Gemini <person@example.com>", true},
		{"signoff at an AI address", fix + "Signed-off-by: Ada Lovelace <noreply@anthropic.com>", true},
		{"assisted-by trailer", fix + "Assisted-by: Claude:claude-opus-4", true},

		// Other attribution lines.
		{"self identification", "fix: written by Cursor", true},
		{"agent identity", fix + "Cursor Agent", true},
		{"generated attribution", "fix: generated by Cursor", true},
		{"other generated attribution", "fix: generated by Codex", true},
		{"generated with link", fix + "Generated with [Claude Code](https://claude.com/claude-code)", true},
		{"generated with tool name", fix + "Generated with Gemini", true},
		{"robot line", fix + "\U0001F916 Generated with something", true},
		{"robot emoji alone", fix + "\U0001F916", true},
		{"robot shortcode", fix + ":robot: wrote this", true},
		{"claude session trailer", fix + "Claude-Session: https://claude.ai/code/session_01ABC", true},
		{"session link", fix + "https://claude.ai/code/session_01ABC", true},
		{"chat link", fix + "https://chatgpt.com/share/abc", true},

		// Ordinary words that look close must not trip the check.
		{"generated by a build tool", fix + "Regenerate the queries.\n\nGenerated with sqlc.", false},
		{"names a tool in prose", fix + "Document how Claude Code settings are ignored by the build.", false},
		{"surname containing a tool word", fix + "Co-authored-by: Anne Cursoria <anne@example.com>", false},
	} {
		t.Run("available base/"+tc.name, func(t *testing.T) {
			git("commit", "--allow-empty", "-qm", tc.message)
			head := git("rev-parse", "HEAD")
			output, err := runCommitAttribution(t, dir, script, base, head)
			if (err != nil) != tc.wantRed {
				t.Fatalf("want red=%t, got %v: %s", tc.wantRed, err, output)
			}
			if tc.wantRed && !strings.Contains(output, "forbidden attribution") {
				t.Fatalf("not an attribution failure: %s", output)
			}
			if !tc.wantRed && !strings.Contains(output, "PASS") {
				t.Fatalf("clean message did not report PASS: %s", output)
			}
			base = head
		})
	}
}

func TestCommitAttributionScansReachableHistory(t *testing.T) {
	script := commitAttributionScript(t)
	missingBase := strings.Repeat("f", 40)
	zeroBase := strings.Repeat("0", 40)

	t.Run("available base range", func(t *testing.T) {
		dir, git := newCommitAttributionRepo(t)
		git("commit", "--allow-empty", "-qm", "test: clean root")
		base := git("rev-parse", "HEAD")
		git("commit", "--allow-empty", "-qm", "fix: preserve the form\n\n"+aiTrailer)
		git("commit", "--allow-empty", "-qm", "fix: clean tip")
		head := git("rev-parse", "HEAD")
		output, err := runCommitAttribution(t, dir, script, base, head)
		if err == nil || !strings.Contains(output, "forbidden attribution") {
			t.Fatalf("available-base range must reject dirty ancestor: %v: %s", err, output)
		}
	})

	t.Run("zero base initial push", func(t *testing.T) {
		dir, git := newCommitAttributionRepo(t)
		git("commit", "--allow-empty", "-qm", "test: clean root")
		git("commit", "--allow-empty", "-qm", "fix: preserve the form\n\n"+aiTrailer)
		head := git("rev-parse", "HEAD")
		output, err := runCommitAttribution(t, dir, script, zeroBase, head)
		if err == nil || !strings.Contains(output, "forbidden attribution") {
			t.Fatalf("zero-base scan must reject dirty ancestor: %v: %s", err, output)
		}
	})

	t.Run("missing base clean history", func(t *testing.T) {
		dir, git := newCommitAttributionRepo(t)
		git("commit", "--allow-empty", "-qm", "test: clean root")
		git("commit", "--allow-empty", "-qm", "fix: clean tip")
		head := git("rev-parse", "HEAD")
		output, err := runCommitAttribution(t, dir, script, missingBase, head)
		if err != nil {
			t.Fatalf("missing base must pass on clean history: %v: %s", err, output)
		}
		if !strings.Contains(output, "PASS") {
			t.Fatalf("missing base must pass on clean history: %s", output)
		}
	})

	t.Run("missing base dirty ancestor clean tip", func(t *testing.T) {
		dir, git := newCommitAttributionRepo(t)
		git("commit", "--allow-empty", "-qm", "test: clean root")
		git("commit", "--allow-empty", "-qm", "fix: preserve the form\n\n"+aiTrailer)
		git("commit", "--allow-empty", "-qm", "fix: clean tip")
		head := git("rev-parse", "HEAD")
		output, err := runCommitAttribution(t, dir, script, missingBase, head)
		if err == nil || !strings.Contains(output, "forbidden attribution") {
			t.Fatalf("missing base must reject dirty ancestor: %v: %s", err, output)
		}
	})

	t.Run("missing base dirty tip", func(t *testing.T) {
		dir, git := newCommitAttributionRepo(t)
		git("commit", "--allow-empty", "-qm", "test: clean root")
		git("commit", "--allow-empty", "-qm", "fix: generated by Cursor")
		head := git("rev-parse", "HEAD")
		output, err := runCommitAttribution(t, dir, script, missingBase, head)
		if err == nil || !strings.Contains(output, "forbidden attribution") {
			t.Fatalf("missing base must reject dirty tip: %v: %s", err, output)
		}
	})
}

// TestCommitAttributionFailsClosed refuses to pass a range the policy script
// cannot judge: a malformed SHA, a head commit that does not exist, and a grep
// that fails while scanning each fail the script rather than read as clean.
func TestCommitAttributionFailsClosed(t *testing.T) {
	script := commitAttributionScript(t)
	dir, git := newCommitAttributionRepo(t)
	git("commit", "--allow-empty", "-qm", "test: clean base")
	base := git("rev-parse", "HEAD")
	head := base

	output, err := runCommitAttribution(t, dir, script, "not-a-sha", head)
	if err == nil || !strings.Contains(output, "two full commit SHAs are required") {
		t.Fatalf("malformed SHA must fail closed: %v: %s", err, output)
	}

	output, err = runCommitAttribution(t, dir, script, base, strings.Repeat("a", 40))
	if err == nil {
		t.Fatalf("missing head must fail closed: %v: %s", err, output)
	}

	fixture := filepath.Join(dir, "failing-grep.sh")
	if writeErr := os.WriteFile(fixture, []byte("grep() { return 2; }\n"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	git("commit", "--allow-empty", "-qm", "test: clean detector input")
	output, err = runCommitAttribution(t, dir, script, base, git("rev-parse", "HEAD"), "BASH_ENV="+fixture)
	if err == nil || !strings.Contains(output, "detector failed") {
		t.Fatalf("detector error must fail closed, got %v: %s", err, output)
	}
}
