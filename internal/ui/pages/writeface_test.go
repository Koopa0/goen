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
			// Same write, same handler. A form whose hx-post goes somewhere its
			// action does not is two server paths wearing one button: the
			// scripting-off visitor reaches one and everyone else reaches the
			// other, and nothing but a reader notices when they drift.
			if hx := attrValue(attrs, "hx-post"); hx != "" && hx != action {
				t.Errorf("%s\n  posts to %q with script and %q without — a write has "+
					"one handler, not one per visitor", where, hx, action)
			}
		}
	}

	if forms < 20 {
		t.Fatalf("found %d forms, want far more — the parser stopped matching", forms)
	}
}

// TestEveryFormActionResolvesToAPostRoute is the other half of naming a handler:
// the guard above asks that a form HAS an action, never that anything answers
// it. A form posting to a route nobody registered reaches the ServeMux's 404,
// which looks like a working page until somebody presses the button.
func TestEveryFormActionResolvesToAPostRoute(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	posts := routesFor(t, filepath.Join(root, "cmd", "goen", "server.go"), "POST")
	gets := routesFor(t, filepath.Join(root, "cmd", "goen", "server.go"), "GET")
	if len(posts) < 20 {
		t.Fatalf("only %d POST routes found; the parser is not reading server.go", len(posts))
	}

	checked := 0
	methods := methodFormActions(t)
	seen := map[string]bool{}
	for path, src := range templateSources(t) {
		for _, m := range formOpen.FindAllStringSubmatch(src, -1) {
			attrs := m[1]
			action := attrValue(attrs, "action")
			if action == "{expr}" {
				expr := strings.TrimSpace(attrExpr(attrs, "action"))
				key := path + ":" + expr
				if actions, ok := methods[key]; ok {
					seen[key] = true
					for _, candidate := range actions {
						checkFormRoute(t, path, attrs, candidate, posts, gets)
						checked++
					}
					continue
				}
				// The pickup map posts to the carrier, whose URL is supplied by
				// the gateway; it must not be compared with this server's mux.
				if key == "pages/cart.templ:templ.URL(v.Map.Action)" {
					continue
				}
				action = pathFromExpr(expr)
				if action == "" {
					t.Errorf("%s: unresolved form action %s; add production action fixtures", path, expr)
					continue
				}
			}
			if action == "" || !strings.HasPrefix(action, "/") {
				continue
			}
			checked++
			checkFormRoute(t, path, attrs, action, posts, gets)
		}
	}
	if checked < 30 {
		t.Fatalf("only %d form actions resolved; the parser stopped matching", checked)
	}
	for key := range methods {
		if !seen[key] {
			t.Errorf("action fixture %s no longer matches a form", key)
		}
	}
	t.Logf("%d local actions resolved", checked)
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
		// Keyed by the path under internal/ui and not by the base name: two
		// packages here both hold an admin.templ, and a map of base names drops
		// one of them in silence — the write-face of a whole file then goes
		// unchecked while both guards below still meet their floors.
		//
		// Comments are stripped first, or a doc comment quoting `<form
		// method="post">` is read as a form.
		out[filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))] = withoutComments(string(src))
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/ui: %v", err)
	}
	return out
}

func checkFormRoute(t *testing.T, path, attrs, action string, posts, gets []*regexp.Regexp) {
	t.Helper()
	// Fragments and chooser queries never reach ServeMux.
	if i := strings.IndexAny(action, "?#"); i >= 0 {
		action = action[:i]
	}
	want := posts
	if strings.EqualFold(attrValue(attrs, "method"), "get") {
		want = gets
	}
	if !resolves(action, want) {
		t.Errorf("%s: form action %q has no %s route", path, action, attrValue(attrs, "method"))
	}
}
