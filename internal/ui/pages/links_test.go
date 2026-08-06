package pages_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryHardCodedLinkResolvesToARoute proves the site never points at its
// own 404.
//
// A link to a route that does not exist is a 404 the site's own chrome points
// at, and nothing else catches it: the templates compile, the handlers work,
// and the page renders. Only somebody clicking finds out.
//
// It happened twice while the account navigation was being built —
// /account/orders is /account/orders/{number} and nothing else — and the first
// sweep missed it because that sweep asked whether anything LINKS TO each
// route. This asks the other direction, which is the one a customer takes.
//
// Only literal hrefs. A templated one carries a value the template cannot know,
// and its handler's own tests are what cover it.
func TestEveryHardCodedLinkResolvesToARoute(t *testing.T) {
	root := repoRoot(t)

	routes := readRoutes(t, filepath.Join(root, "cmd", "goen", "server.go"))
	if len(routes) < 20 {
		t.Fatalf("only %d GET routes found; the parser is not reading server.go", len(routes))
	}

	links := readLinks(t, filepath.Join(root, "internal", "ui"))
	if len(links) < 10 {
		t.Fatalf("only %d links found; the parser is not reading the templates", len(links))
	}

	for _, link := range links {
		if strings.HasPrefix(link, "//") || strings.Contains(link, "://") {
			continue // off-site, not this test's business
		}
		// A fragment or a query belongs to the page, not the route.
		path := link
		if i := strings.IndexAny(path, "?#"); i >= 0 {
			path = path[:i]
		}
		if path == "" {
			continue
		}
		if !resolves(path, routes) {
			t.Errorf("%s is linked from a template and matches no GET route", link)
		}
	}
}

// readRoutes is every GET path the server registers, as a matcher.
func readRoutes(t *testing.T, serverGo string) []*regexp.Regexp {
	t.Helper()
	//nolint:gosec // G304: the path is this test's own constant, joined to the
	// repository root it just located
	src, err := os.ReadFile(serverGo)
	if err != nil {
		t.Fatalf("read routes: %v", err)
	}
	declared := regexp.MustCompile(`mux\.HandleFunc\("GET (/[^"]*)"`).FindAllStringSubmatch(string(src), -1)

	out := make([]*regexp.Regexp, 0, len(declared))
	for _, m := range declared {
		// A {wildcard} matches one segment; {rest...} matches the tail.
		pattern := regexp.QuoteMeta(m[1])
		pattern = strings.ReplaceAll(pattern, `\{\$\}`, "")
		pattern = regexp.MustCompile(`\\\{[^}]*\.{3}\\\}`).ReplaceAllString(pattern, ".*")
		pattern = regexp.MustCompile(`\\\{[^}]*\\\}`).ReplaceAllString(pattern, "[^/]+")
		out = append(out, regexp.MustCompile("^"+pattern+"$"))
	}
	return out
}

// readLinks is every literal href in the templates.
func readLinks(t *testing.T, uiDir string) []string {
	t.Helper()
	href := regexp.MustCompile(`href="(/[^"]*)"`)

	seen := map[string]bool{}
	err := filepath.WalkDir(uiDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".templ") {
			return err
		}
		//nolint:gosec // G304: path comes from WalkDir over this repository
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, m := range href.FindAllStringSubmatch(string(src), -1) {
			seen[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
	out := make([]string, 0, len(seen))
	for link := range seen {
		out = append(out, link)
	}
	return out
}

// resolves reports whether any route matches.
func resolves(path string, routes []*regexp.Regexp) bool {
	for _, r := range routes {
		if r.MatchString(path) {
			return true
		}
	}
	return false
}

// repoRoot walks up until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for range 6 {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("no go.mod above the test's directory")
	return ""
}
