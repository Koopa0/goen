package repocheck_test

import (
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// layerNames are the package names CONTRIBUTING's package rule 4 refuses: a
// name for a layer or a grab-bag says nothing about what the package holds.
// "Packages named util, common, or misc provide clients with no sense of what
// the package contains" (go.dev/blog/package-names); "Avoid uninformative
// package names like util, utility, common, helper, model, testhelper"
// (Google Go style, decisions). pages and views are the layer the page bodies
// once lived in, until each moved into the feature that renders it (#2184).
var layerNames = []string{
	"api", "common", "helper", "helpers", "interfaces", "misc", "model", "models",
	"pages", "testhelper", "types", "ui", "util", "utility", "utils", "view", "views",
}

// minPackageDirs is the floor for the walk: main has 104 directories holding Go
// files under internal/ and cmd/, so a walk that found far fewer has not looked
// at the repository.
const minPackageDirs = 80

// TestNoPackageHasALayerName holds rule 4 for every directory that holds a Go
// file, test files included: a layer-named package with only tests in it is
// still somewhere a page body could be put back.
func TestNoPackageHasALayerName(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	dirs := map[string]bool{}
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") {
				rel, relErr := filepath.Rel(root, filepath.Dir(path))
				if relErr != nil {
					return relErr
				}
				dirs[filepath.ToSlash(rel)] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", top, err)
		}
	}
	if len(dirs) < minPackageDirs {
		t.Fatalf("the walk found %d directories with Go files, want at least %d", len(dirs), minPackageDirs)
	}
	for _, dir := range slices.Sorted(maps.Keys(dirs)) {
		if slices.Contains(layerNames, filepath.Base(dir)) {
			t.Errorf("%s is named for a layer, not for what it holds: name the package for its concept, "+
				"or put the code in the feature that uses it (CONTRIBUTING, Packages, rule 4)", dir)
		}
	}
}
