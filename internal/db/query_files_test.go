package db_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestQueryFilesIgnoreAChromeProfileRemovedDuringTheWalk(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	before := filepath.Join(root, ".before-profile")
	profile := filepath.Join(root, ".layout-chrome")
	writeQueryFixture(t, filepath.Join(before, "source.sql"), "SELECT 1;")
	writeQueryFixture(t, filepath.Join(profile, "scratch.sql"), "SELECT 2;")
	writeQueryFixture(t, filepath.Join(root, "internal", "feature", "query.sql"), "SELECT 3;")

	got := map[string]string{}
	visit := queryFileVisitor(root, got)
	removed := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		// Root entries are already cached when this earlier sibling is visited.
		if path == before {
			if removeErr := os.RemoveAll(profile); removeErr != nil {
				return removeErr
			}
			removed = true
		}
		return visit(path, d, err)
	})
	if err != nil {
		t.Fatalf("walk after removing browser profile: %v", err)
	}
	if !removed {
		t.Fatal("the browser profile was not removed during the walk")
	}
	want := map[string]string{
		filepath.Join(before, "source.sql"):                     "SELECT 1;",
		filepath.Join(root, "internal", "feature", "query.sql"): "SELECT 3;",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("query inventory mismatch (-want +got):\n%s", diff)
	}
}

func TestQueryFilesKeepSourcesOutsideTheBrowserProfile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	want := map[string]string{
		filepath.Join(root, "internal", "feature", "query.sql"): "SELECT 1;",
		filepath.Join(root, "cmd", "query.sql"):                 "SELECT 2;",
		filepath.Join(root, "seed", "catalog.sql"):              "SELECT 3;",
		filepath.Join(root, "scripts", "fixture.sql"):           "SELECT 4;",
		filepath.Join(root, ".fixtures", "source.sql"):          "SELECT 5;",
		filepath.Join(root, "other", "query.sql"):               "SELECT 6;",
	}
	for path, src := range want {
		writeQueryFixture(t, path, src)
	}
	for _, dir := range []string{".git", ".layout-chrome", "migrations"} {
		writeQueryFixture(t, filepath.Join(root, dir, "ignored.sql"), "SELECT 7;")
	}
	got, err := readQueryFiles(root)
	if err != nil {
		t.Fatalf("read query sources: %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("query inventory mismatch (-want +got):\n%s", diff)
	}
}

func TestQueryFilesReportRealSourceErrors(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name        string
		missingRoot bool
	}{
		{name: "missing-root", missingRoot: true},
		{name: "missing-query-target", missingRoot: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			missing := filepath.Join(root, "missing")
			if tt.missingRoot {
				root = missing
			} else {
				path := filepath.Join(root, "query.sql")
				if err := os.Symlink(missing, path); err != nil {
					t.Fatalf("create missing query target: %v", err)
				}
				missing = path
			}
			_, err := readQueryFiles(root)
			if !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("readQueryFiles(%q) error = %v, want missing source", root, err)
			}
			pathErr, ok := errors.AsType[*fs.PathError](err)
			if !ok || pathErr.Path != missing {
				t.Errorf("readQueryFiles(%q) error = %v, want path %q", root, err, missing)
			}
		})
	}
}

func writeQueryFixture(t *testing.T, path, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create query directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write query fixture: %v", err)
	}
}
