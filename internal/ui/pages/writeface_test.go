package pages_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// formOpen matches a form element's opening tag across newlines, since templ
// formats attributes one per line.
var formOpen = regexp.MustCompile(`(?s)<form\b(.*?)>`)

// TestEveryFormWorksWithScriptingOff is the write-face rule, mechanised: a form
// that loses its action is not a compile error, not a test failure and not
// visible in a browser that has JavaScript.
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
			// GET forms are search and filter; a write must say post, or a
			// reload resubmits it.
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

// TestEveryFormActionResolvesToAPostRoute is the other half of naming a handler:
// the guard above asks that a form HAS an action, never that anything answers
// it. A form posting to a route nobody registered reaches the ServeMux's 404 —
// which is not a compile error, not a test failure, and looks like a working
// page until somebody presses the button. It is mistake #35 on the write face:
// "is it wired?" and "does a handler exist?" are different questions.
func TestEveryFormActionResolvesToAPostRoute(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	posts := routesFor(t, filepath.Join(root, "cmd", "goen", "server.go"), "POST")
	gets := routesFor(t, filepath.Join(root, "cmd", "goen", "server.go"), "GET")
	if len(posts) < 20 {
		t.Fatalf("only %d POST routes found; the parser is not reading server.go", len(posts))
	}

	checked, unresolvable := 0, 0
	for path, src := range templateSources(t) {
		for _, m := range formOpen.FindAllStringSubmatch(src, -1) {
			attrs := m[1]
			action := attrValue(attrs, "action")
			if action == "{expr}" {
				// Most of the back office builds its action from literals around
				// one value — templ.SafeURL("/admin/orders/" + v.Number + "/ship")
				// — and skipping every expression left the majority of goen's
				// POST routes uncovered, this PR's own allowance form among them.
				action = pathFromExpr(attrExpr(attrs, "action"))
				if action == "" {
					unresolvable++
					continue
				}
			}
			if action == "" || !strings.HasPrefix(action, "/") {
				continue
			}
			// A query string is the chooser's, not the route's.
			if i := strings.IndexByte(action, '?'); i >= 0 {
				action = action[:i]
			}
			checked++
			want := posts
			if strings.ToLower(attrValue(attrs, "method")) == "get" {
				want = gets
			}
			if !resolves(action, want) {
				t.Errorf("%s: %s\n  posts to a path the server registers no handler for",
					path, firstLine(m[0]))
			}
		}
	}
	if checked < 30 {
		t.Fatalf("only %d form actions resolved; the parser stopped matching", checked)
	}
	// Named rather than silent: an action built entirely from a method call
	// carries no literal to resolve, and a check that quietly drops those reads
	// as "everything is covered" when it is not.
	t.Logf("%d actions resolved; %d built from a call with no literal path", checked, unresolvable)
}

// attrExpr is the raw templ expression of attr={ ... }, or "" when it is a
// quoted literal or absent.
func attrExpr(attrs, name string) string {
	m := regexp.MustCompile(name + `=\{([^}]*)\}`).FindStringSubmatch(attrs)
	if m == nil {
		return ""
	}
	return m[1]
}

// pathFromExpr turns "/admin/orders/" + v.Number + "/ship" into a path a route
// matcher can answer, by standing one segment in for each interpolated value.
// It returns "" when the expression has no literal path to work from.
func pathFromExpr(expr string) string {
	lits := regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(expr, -1)
	if len(lits) == 0 || !strings.HasPrefix(lits[0][1], "/") {
		return ""
	}
	parts := make([]string, 0, len(lits))
	for _, l := range lits {
		parts = append(parts, l[1])
	}
	// "x" is a stand-in for the value between two literals; a route's own
	// {wildcard} matches one segment, so any non-empty segment will do.
	path := strings.Join(parts, "x")
	// A value can also come LAST — "/admin/taxonomy/" + kind — and joining
	// literals alone loses it, leaving a path that ends at the slash and matches
	// nothing. Anything after the final literal but inside the call is one more
	// segment.
	last := strings.LastIndex(expr, `"`)
	tail := strings.TrimSpace(expr[last+1:])
	tail = strings.TrimSuffix(tail, ")")
	if strings.TrimSpace(tail) != "" {
		path += "x"
	}
	return path
}

// attrValue reads a quoted attribute, or "" when it is absent. A templ
// expression counts as present: the rule is that the form names its handler,
// not that the name is a literal.
func attrValue(attrs, name string) string {
	if m := regexp.MustCompile(name + `="([^"]*)"`).FindStringSubmatch(attrs); m != nil {
		return m[1]
	}
	if regexp.MustCompile(name + `=\{`).MatchString(attrs) {
		return "{expr}"
	}
	return ""
}

// withoutComments drops // lines, which in a .templ file appear in the template
// body as well as above it.
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
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return strings.TrimSpace(before)
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
		// Comments are stripped first, or a doc comment quoting `<form
		// method="post">` is read as a form.
		out[filepath.Base(path)] = withoutComments(string(src))
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/ui: %v", err)
	}
	return out
}
