package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryLocaleParamIsAssigned refuses a localized query called with no locale.
func TestEveryLocaleParamIsAssigned(t *testing.T) {
	t.Parallel()

	needsLocale := localeParamTypes(t)
	if len(needsLocale) < 5 {
		t.Fatalf("found %d Params types carrying a locale, want far more — the parse "+
			"of the generated code is wrong", len(needsLocale))
	}

	found := 0
	for path, src := range goSources(t) {
		for _, lit := range paramLiterals(src) {
			if !needsLocale[lit.typeName] {
				continue
			}
			found++
			if strings.Contains(lit.body, "Locale:") {
				continue
			}
			t.Errorf("%s: db.%s is constructed without Locale.\n"+
				"  The zero value is \"\", which localized_name reads as \"not English\" "+
				"— so the page renders in Chinese and nothing fails. Pass "+
				"string(i18n.FromContext(ctx)).", path, lit.typeName)
		}
	}
	if found == 0 {
		t.Fatal("no localized Params literal was found at all — the scanner is broken")
	}
}

// localeParamTypes is every generated Params struct with a Locale field.
func localeParamTypes(t *testing.T) map[string]bool {
	t.Helper()

	src, err := os.ReadFile("query.sql.go")
	if err != nil {
		t.Fatalf("read the generated queries: %v", err)
	}
	out := map[string]bool{}
	decl := regexp.MustCompile(`(?s)type (\w+Params) struct \{(.*?)\n\}`)
	for _, m := range decl.FindAllStringSubmatch(string(src), -1) {
		if regexp.MustCompile(`(?m)^\s+Locale\s+string$`).MatchString(m[2]) {
			out[m[1]] = true
		}
	}
	return out
}

// paramLiteral is one `db.XParams{...}` composite literal.
type paramLiteral struct {
	typeName string
	body     string
}

// paramLiterals finds every db.*Params{...} in src, brace-balanced so a nested literal does not cut it short.
func paramLiterals(src string) []paramLiteral {
	head := regexp.MustCompile(`db\.(\w+Params)\{`)
	found := head.FindAllStringSubmatchIndex(src, -1)
	out := make([]paramLiteral, 0, len(found))
	for _, m := range found {
		depth, end := 1, m[1]
		for i := m[1]; i < len(src) && depth > 0; i++ {
			switch src[i] {
			case '{':
				depth++
			case '}':
				depth--
			}
			end = i
		}
		out = append(out, paramLiteral{
			typeName: src[m[2]:m[3]],
			body:     src[m[1]:end],
		})
	}
	return out
}

// goSources is every hand-written Go file in the repository.
func goSources(t *testing.T) map[string]string {
	t.Helper()

	out := map[string]string{}
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case !strings.HasSuffix(path, ".go"),
			strings.HasSuffix(path, "_templ.go"),
			strings.Contains(filepath.ToSlash(path), "internal/db/query.sql.go"):
			return nil
		}
		src, readErr := os.ReadFile(path) //nolint:gosec // G304: paths come from walking this repository
		if readErr != nil {
			return readErr
		}
		out[path] = string(src)
		return nil
	})
	if err != nil {
		t.Fatalf("walk for Go sources: %v", err)
	}
	return out
}
