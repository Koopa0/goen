package pages_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// formOpen matches the start of a form element and everything up to its closing
// bracket, across newlines — templ formats attributes one per line.
var formOpen = regexp.MustCompile(`(?s)<form\b(.*?)>`)

// TestEveryFormWorksWithScriptingOff is the write-face rule, mechanised.
//
// CLAUDE.md states it as the project's central pin: every mutation is a plain
// <form method="post"> that works with scripting off, and hx-* may only change
// what is written BACK. It names the failure exactly — "hx-post on a form with
// no action" — and until now nothing but review enforced it.
//
// A form that loses its action is not a compile error, not a test failure, and
// not visible in a browser that has JavaScript. It is visible to the customer
// whose extension blocked it, and to nobody else.
func TestEveryFormWorksWithScriptingOff(t *testing.T) {
	t.Parallel()

	forms := 0
	for path, src := range templateSources(t) {
		for _, m := range formOpen.FindAllStringSubmatch(src, -1) {
			forms++
			attrs := m[1]
			where := path + ": " + firstLine(m[0])

			action := attrValue(attrs, "action")
			if action == "" {
				t.Errorf("%s\n  has no action — with scripting off it posts to the "+
					"current URL, which is almost never the handler it means", where)
			}
			// GET forms are search and filter, which are not mutations. Anything
			// that writes must say so, or a reload resubmits it.
			method := strings.ToLower(attrValue(attrs, "method"))
			if method != "get" && method != "post" {
				t.Errorf("%s\n  has method=%q, want get or post", where, method)
			}
			if strings.Contains(attrs, "hx-post") && method != "post" {
				t.Errorf("%s\n  carries hx-post but is not method=post — htmx would "+
					"be the only way this write happens", where)
			}
		}
	}

	if forms < 20 {
		t.Fatalf("found %d forms, want far more — the parser stopped matching", forms)
	}
}

// attrValue reads a quoted attribute, or "" when it is absent or dynamic.
//
// A templ expression — action={ templ.SafeURL(...) } — counts as present: the
// rule is that the form names its handler, not that the name is a literal.
func attrValue(attrs, name string) string {
	if m := regexp.MustCompile(name + `="([^"]*)"`).FindStringSubmatch(attrs); m != nil {
		return m[1]
	}
	if regexp.MustCompile(name + `=\{`).MatchString(attrs) {
		return "{expr}"
	}
	return ""
}

// withoutComments drops // lines, which in a .templ file are Go comments in
// the template body as well as above it.
func withoutComments(src string) string {
	lines := strings.Split(src, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "//") {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// templateSources is every .templ file under internal/ui.
func templateSources(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	const root = ".." // internal/ui
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".templ") {
			return err
		}
		src, readErr := os.ReadFile(path) //nolint:gosec // G304: paths come from walking this repository
		if readErr != nil {
			return readErr
		}
		// Comments stripped first: a doc comment explaining the rule quotes
		// `<form method="post">`, and the first run of this test reported two
		// of its own explanations as violations.
		out[filepath.Base(path)] = withoutComments(string(src))
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/ui: %v", err)
	}
	return out
}
