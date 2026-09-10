package outbox_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// mailPayloads is every payload type a mail message carries, and the packages
// that declare a copy of it. The copies are deliberate: a consumer never imports
// a producer's types, so it can be deployed a version behind.
var mailPayloads = map[string][]string{
	"OrderPlaced":   {"../email", "../cart"},
	"OrderPaid":     {"../email", "../payment"},
	"OrderShipped":  {"../email", "../admin"},
	"RestockNotice": {"../email", "../admin"},
	// Declared once: the producer marshals email's own type.
	"PasswordReset":     {"../email"},
	"NewsletterConfirm": {"../email"},
	"NewsletterWelcome": {"../email"},
	"NewsletterIssue":   {"../email"},
	"AddressVerify":     {"../email"},
}

// TestEveryMailPayloadMatchesItsProducer holds the duplicated structs to each
// other, field for field.
func TestEveryMailPayloadMatchesItsProducer(t *testing.T) {
	t.Parallel()

	for name, dirs := range mailPayloads {
		if len(dirs) < 2 {
			continue
		}
		want := jsonFields(t, dirs[0], name)
		if len(want) == 0 {
			t.Errorf("%s.%s has no json-tagged fields; this test would pass on nothing",
				dirs[0], name)
			continue
		}
		for _, dir := range dirs[1:] {
			got := jsonFields(t, dir, name)
			if !slices.Equal(want, got) {
				t.Errorf("%s.%s carries %v; %s.%s carries %v — the two copies of one "+
					"payload have drifted, and whichever side is missing a field "+
					"silently reads its zero value",
					dirs[0], name, want, dir, name, got)
			}
		}
	}
}

// TestEveryMailPayloadCarriesALocale: the worker has no locale of its own, so
// the language has to travel in the payload.
func TestEveryMailPayloadCarriesALocale(t *testing.T) {
	t.Parallel()

	for name, dirs := range mailPayloads {
		for _, dir := range dirs {
			fields := jsonFields(t, dir, name)
			if !slices.Contains(fields, "locale") {
				t.Errorf("%s.%s has no locale field, so a message on this topic is sent "+
					"in whichever language the fallback picks: %v", dir, name, fields)
			}
		}
	}
}

// jsonFields is the sorted json tag names of a struct declared in dir.
func jsonFields(t *testing.T, dir, typeName string) []string {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		if out, found := fieldsOf(t, file, typeName); found {
			return out
		}
	}
	t.Errorf("%s declares no type %s", dir, typeName)
	return nil
}

func fieldsOf(t *testing.T, file *ast.File, typeName string) (names []string, found bool) {
	t.Helper()

	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != typeName {
			return true
		}
		st, ok := spec.Type.(*ast.StructType)
		if !ok {
			return false
		}
		found = true
		for _, f := range st.Fields.List {
			if f.Tag == nil {
				continue
			}
			tag, err := strconv.Unquote(f.Tag.Value)
			if err != nil {
				continue
			}
			if name := jsonName(tag); name != "" {
				names = append(names, name)
			}
		}
		return false
	})
	slices.Sort(names)
	return names, found
}

// jsonName reads the json tag's name, or "" when there is none.
func jsonName(tag string) string {
	_, rest, ok := strings.Cut(tag, `json:"`)
	if !ok {
		return ""
	}
	value, _, ok := strings.Cut(rest, `"`)
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(value, ",")
	return name
}

// TestTheFixtureDirsExist stops a rename turning this file into a no-op:
// jsonFields walks a directory by path.
func TestTheFixtureDirsExist(t *testing.T) {
	t.Parallel()

	for name, dirs := range mailPayloads {
		for _, dir := range dirs {
			if _, err := os.Stat(dir); err != nil {
				t.Errorf("%s (for %s) is not a directory: %v", dir, name, err)
			}
		}
	}
}
