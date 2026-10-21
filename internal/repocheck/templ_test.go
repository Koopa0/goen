package repocheck_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minTemplates is the floor under the template walk. Moving a template from one
// package to another keeps the total; a walk that stopped reaching the
// templates drops far below it. There is deliberately no per-directory floor:
// it would have to be edited by every co-location change and would protect
// nothing the walk of internal/ does not already protect.
const minTemplates = 80

// templSources is every hand-written template under internal/, keyed by its
// path from the repository root ("internal/auth/cart_recovery_page.templ"). The key is
// the whole path, never a directory's base name: two packages each hold an
// admin.templ, and a base-name map would silently drop one. The generated
// *_templ.go files end in .go, so they are not part of the walk.
func templSources(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	out := map[string]string{}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".templ") {
			return nil
		}
		body, readErr := os.ReadFile(path) //nolint:gosec // G304: the path comes from walking this repository
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out[filepath.ToSlash(rel)] = string(body)
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/ for templates: %v", err)
	}
	if len(out) < minTemplates {
		t.Fatalf("found %d templates under internal/, fewer than %d: the walk stopped reaching them", len(out), minTemplates)
	}
	return out
}
