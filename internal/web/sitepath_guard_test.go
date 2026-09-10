package web

import (
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestOnlySitePathDecidesASameSitePath keeps WHATWG rootedness in one place.
// A second prefix test is not a simpler spelling of this rule; it is a second
// definition waiting to drift from the browser grammar again.
func TestOnlySitePathDecidesASameSitePath(t *testing.T) {
	t.Parallel()

	root := filepath.Clean(filepath.Join("..", ".."))
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch rel {
			case ".git", "internal/db", "internal/web":
				return filepath.SkipDir
			}
			return nil
		}
		if !sitePathGuardSource(path) {
			return nil
		}
		src, err := os.ReadFile(path) //nolint:gosec // G304: path comes from walking this repository
		if err != nil {
			return err
		}
		for _, at := range sameSitePrefixTests(path, src) {
			t.Errorf("%s:%d reimplements same-site rootedness with strings.HasPrefix; use web.SitePath",
				rel, at)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}
}

func sitePathGuardSource(path string) bool {
	switch {
	case strings.HasSuffix(path, "_templ.go"), strings.HasSuffix(path, "_test.go"):
		return false
	case strings.HasSuffix(path, ".go"), strings.HasSuffix(path, ".templ"):
		return true
	default:
		return false
	}
}

// sameSitePrefixTests tokenises instead of grepping so comments do not satisfy
// the guard and a call split over lines is still found. Go's scanner continues
// through templ markup, while the call expressions inside it retain Go tokens.
func sameSitePrefixTests(path string, src []byte) []int {
	type item struct {
		pos token.Pos
		tok token.Token
		lit string
	}
	fset := token.NewFileSet()
	file := fset.AddFile(path, -1, len(src))
	var scan scanner.Scanner
	scan.Init(file, src, func(token.Position, string) {}, 0)
	var items []item
	for {
		pos, tok, lit := scan.Scan()
		items = append(items, item{pos: pos, tok: tok, lit: lit})
		if tok == token.EOF {
			break
		}
	}

	var lines []int
	for i := 0; i+4 < len(items); i++ {
		if items[i].tok != token.IDENT || items[i].lit != "strings" ||
			items[i+1].tok != token.PERIOD ||
			items[i+2].tok != token.IDENT || items[i+2].lit != "HasPrefix" ||
			items[i+3].tok != token.LPAREN {
			continue
		}
		depth, argument := 1, 0
		for j := i + 4; j < len(items) && depth > 0; j++ {
			switch items[j].tok {
			case token.LPAREN, token.LBRACK, token.LBRACE:
				depth++
			case token.RPAREN, token.RBRACK, token.RBRACE:
				depth--
			case token.COMMA:
				if depth == 1 {
					argument++
					if argument != 1 || items[j+1].tok != token.STRING ||
						items[j+2].tok != token.RPAREN {
						continue
					}
					literal, err := strconv.Unquote(items[j+1].lit)
					if err == nil && (literal == "/" || literal == "//" || literal == `/\`) {
						lines = append(lines, fset.Position(items[i].pos).Line)
					}
				}
			default:
			}
		}
	}
	return lines
}
