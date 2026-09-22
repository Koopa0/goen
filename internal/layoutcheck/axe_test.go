package layoutcheck_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTheLayoutGateFetchesAxeCoreAtAPinnedDigest refuses an audit whose rules
// arrive from a CDN at page load.
//
// axe-core decides what merges, so where it comes from is a supply-chain
// question rather than a convenience one: a <script src> at a third party lets
// that party choose the code running inside the gate, and it would need the
// site's own `script-src 'self'` relaxed to get in — which would mean auditing
// a page no visitor is served. The Makefile fetches the file at a pinned
// version, checks it against a digest pinned beside it, and the script
// evaluates it over CDP.
func TestTheLayoutGateFetchesAxeCoreAtAPinnedDigest(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	makefile := readMakefile(t, root)

	version := regexp.MustCompile(`(?m)^AXE_CORE_VERSION\s*:=\s*(\S+)$`).FindStringSubmatch(makefile)
	if version == nil {
		t.Fatal("the Makefile does not pin AXE_CORE_VERSION; the pins test cannot see an unpinned tool")
	}
	if !regexp.MustCompile(`(?m)^AXE_CORE_SHA256\s*:=\s*[0-9a-f]{64}$`).MatchString(makefile) {
		t.Fatal("AXE_CORE_SHA256 must pin the digest of the file the version names")
	}
	if !strings.Contains(makefile, "axe-core@$(AXE_CORE_VERSION)/axe.min.js") {
		t.Fatal("check-layout must fetch axe-core at AXE_CORE_VERSION, not at a floating tag")
	}
	if !strings.Contains(makefile, "openssl dgst -sha256 .layout-chrome/axe.min.js") ||
		!strings.Contains(makefile, `test "$$digest" = '$(AXE_CORE_SHA256)'`) {
		t.Fatal("check-layout must hash the fetched axe-core and compare it with AXE_CORE_SHA256")
	}

	script := readLayoutScript(t, root)
	if !strings.Contains(script, "const PER_RUN = [") {
		t.Fatal("check-layout.mjs must fold the per-run fixtures out of a route key")
	}
	for _, host := range []string{"unpkg.com", "cdn.jsdelivr.net", "cdnjs.cloudflare.com"} {
		if strings.Contains(script, host) {
			t.Fatalf("check-layout.mjs names %s: the rules must be fetched and verified by the "+
				"Makefile, never loaded into the page at run time", host)
		}
	}
}

// TestTheAxeAuditFailsClosedWithoutItsSource proves a missing rule set stops the
// run rather than quietly reducing it to the geometry checks.
//
// The failure also has to arrive before Chrome is contacted: a gate that spends
// ten minutes measuring and then reports that it never audited anything is a
// gate nobody reads to the end.
func TestTheAxeAuditFailsClosedWithoutItsSource(t *testing.T) {
	t.Parallel()

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}

	root := repoRoot(t)
	//nolint:gosec // G204: node comes from exec.LookPath and the script path is fixed
	cmd := exec.CommandContext(t.Context(), node, filepath.Join("scripts", "check-layout.mjs"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"AXE_SOURCE="+filepath.Join(t.TempDir(), "absent-axe.min.js"),
		// A port nothing is listening on, so a script that got as far as the
		// browser would hang here instead of exiting.
		"CDP_PORT=1",
	)
	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("check-layout.mjs passed with no axe-core to run:\n%s", out)
	}
	var exit *exec.ExitError
	if !asExitError(runErr, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("check-layout.mjs exited %v, want status 2:\n%s", runErr, out)
	}
	if !strings.Contains(string(out), "axe-core is not readable") {
		t.Fatalf("check-layout.mjs did not name the missing rule set:\n%s", out)
	}
}

// TestTheAxeBaselineIsRouteToRuleIDs holds the shape the run reads back.
//
// The file is edited by hand when a finding is accepted, and a baseline that
// does not parse is a gate that will not start — which is discovered on the
// pull request that had nothing to do with it.
func TestTheAxeBaselineIsRouteToRuleIDs(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	baselinePath := filepath.Join(root, "scripts", "axe-baseline.json")
	//nolint:gosec // G304: the repository root joined to a fixed name
	body, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read the axe baseline: %v", err)
	}

	var baseline struct {
		Routes map[string][]string `json:"routes"`
	}
	if err := json.Unmarshal(body, &baseline); err != nil {
		t.Fatalf("the axe baseline does not parse: %v", err)
	}
	// The fixtures mint these on every run, so a baseline naming one is stale by
	// the next run and reports its own staleness as an accessibility failure.
	perRun := regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}` +
		`|GO-\d{6}-\d{6}|LAYOUTSN\d+`)
	ruleID := regexp.MustCompile(`^[a-z0-9-]+$`)
	for route, rules := range baseline.Routes {
		if !strings.HasPrefix(route, "/") {
			t.Errorf("baseline route %q is not a path", route)
		}
		if perRun.MatchString(route) {
			t.Errorf("baseline route %q names a per-run fixture; check-layout.mjs replaces "+
				"those with {id}, {order} and {serial}", route)
		}
		if len(rules) == 0 {
			t.Errorf("baseline route %q lists no rule; an empty entry is debt nobody owns", route)
		}
		for _, rule := range rules {
			if !ruleID.MatchString(rule) {
				t.Errorf("baseline route %q lists %q, which is not an axe rule id", route, rule)
			}
		}
	}
}

// TestCIRunsTheLayoutGate refuses the gate going back to a developer's machine.
//
// It went red on main unnoticed (#390) because nothing but a person running it
// locally ever asked.
func TestCIRunsTheLayoutGate(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	//nolint:gosec // G304: the repository root joined to a fixed name
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "verify.yml"))
	if err != nil {
		t.Fatalf("read the verify workflow: %v", err)
	}
	body := string(workflow)
	if !strings.Contains(body, "\n  layout:\n") {
		t.Fatal("verify.yml has no layout job")
	}
	if !strings.Contains(body, "make check-layout") {
		t.Fatal("the layout job must invoke make check-layout")
	}

	//nolint:gosec // G304: the repository root joined to a fixed name
	ruleset, err := os.ReadFile(filepath.Join(root, ".github", "branch-protection.json"))
	if err != nil {
		t.Fatalf("read the ruleset artifact: %v", err)
	}
	if !strings.Contains(string(ruleset), `"context": "layout"`) {
		t.Fatal("the ruleset artifact does not record layout as a required check")
	}
}

func asExitError(err error, target **exec.ExitError) bool {
	exit, ok := err.(*exec.ExitError) //nolint:errorlint // the concrete type is the subject
	if ok {
		*target = exit
	}
	return ok
}
