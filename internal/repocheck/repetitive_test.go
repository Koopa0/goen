package repocheck_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// minExportedNames is the floor under the exported package-level names the
// walk reads: main has 5336 in its non-test files, so a walk that reads far
// fewer has stopped looking at the repository.
const minExportedNames = 4500

// repetitiveNameExceptions lists the exported names that repeat their package
// and stay, key "dir.Name", value the reason. An entry may only disappear: one
// whose name no longer exists or no longer repeats its package fails, and so
// does one without a reason. A name goen chooses is renamed, not listed, but
// for a desk's view of one record beside its list's View (orders.OrderView
// beside orders.View), where the singular is what tells the two apart.
var repetitiveNameExceptions = map[string]string{
	"internal/db.DBTX": "sqlc chooses it: it writes type DBTX interface into the db.go of every package it " +
		"generates, and the sqlc queries take it as their connection",
	"internal/admin/orders.OrderView": "one order's page beside the orders list, View: the singular is " +
		"what tells the two apart, as the templates Order and Orders do",
	"internal/admin/customers.CustomerView": "one customer's page beside the search list, View: the singular " +
		"is what tells the two apart, as the templates Customer and Customers do",
	"internal/admin/campaigns.CampaignView": "one campaign's page beside the campaign list, View: the singular " +
		"is what tells the two apart, as the templates Campaign and Campaigns do",
	"internal/admin/products.ProductView": "one product's page beside the product list, View: the singular " +
		"is what tells the two apart, as the templates Product and Products do",
}

// TestNoExportedNameRepeatsItsPackage holds CONTRIBUTING's naming rule 6 for
// every exported package-level name in every package's non-test files,
// templ's generated components included: a caller writes the name after the
// package, so chubby.ChubbyFile says chubby twice and is chubby.File
// (go.dev/wiki/CodeReviewComments#package-names, and Google's "repetitive with
// package"). A name that is the package name itself is the type or page the
// package is named for (user.User, banners.Banners) and passes, and so does the
// singular a plural package is named for (orders.Order); a compound
// package's name repeats only part of itself (cartitem.Item) and never matches.
func TestNoExportedNameRepeatsItsPackage(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	dirs, err := goDirs(root, func(name string) bool {
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	})
	if err != nil {
		t.Fatalf("walk the repository for Go packages: %v", err)
	}
	if len(dirs) < minSourcePackages {
		t.Fatalf("the walk found %d directories holding Go source, want at least %d: it has stopped looking at the repository", len(dirs), minSourcePackages)
	}
	problems, examined := checkRepetitiveNames(root, dirs, repetitiveNameExceptions)
	if examined < minExportedNames {
		t.Fatalf("the walk read %d exported names, want at least %d: it has stopped reading the packages", examined, minExportedNames)
	}
	for _, problem := range problems {
		t.Error(problem)
	}
}

// checkRepetitiveNames returns each exported package-level name in the non-test
// files of dirs that repeats its package and is not excepted with a reason,
// each exception that does not hold, and how many exported names it read.
func checkRepetitiveNames(root string, dirs []string, exceptions map[string]string) (problems []string, examined int) {
	used := map[string]bool{}
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(dir), "*.go"))
		if err != nil {
			problems = append(problems, dir+": "+err.Error())
			continue
		}
		slices.Sort(files)
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			f, parseErr := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
			if parseErr != nil {
				problems = append(problems, dir+": "+parseErr.Error())
				continue
			}
			pkg := f.Name.Name
			for _, name := range exportedNames(f) {
				examined++
				n := repeatedPrefix(pkg, name)
				if n == 0 {
					continue
				}
				key := dir + "." + name
				reason, excepted := exceptions[key]
				switch {
				case excepted && strings.TrimSpace(reason) != "":
					used[key] = true
				case excepted:
					used[key] = true
					problems = append(problems, fmt.Sprintf("repetitiveNameExceptions[%q] gives no reason: say why the name stays, or rename it", key))
				default:
					problems = append(problems, fmt.Sprintf("%s/%s: %s.%s repeats its package; name it %s (CONTRIBUTING, Naming, rule 6)",
						dir, filepath.Base(file), pkg, name, name[n:]))
				}
			}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(exceptions)) {
		if !used[key] {
			problems = append(problems, fmt.Sprintf("repetitiveNameExceptions[%q] matches no exported name that repeats its package: delete the entry", key))
		}
	}
	return problems, examined
}

// exportedNames returns the exported package-level functions, types,
// variables and constants of f; methods are reached through a value, not the
// package, and are not among them.
func exportedNames(f *ast.File) []string {
	var names []string
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.IsExported() {
				names = append(names, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						names = append(names, s.Name.Name)
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.IsExported() {
							names = append(names, n.Name)
						}
					}
				}
			}
		}
	}
	return names
}

// repeatedPrefix returns how many bytes at the start of name repeat its
// package, 0 for none: the package name or, for a package named for a plural,
// its singular (questions, Question; warranties, Warranty), in any case and
// followed by a word of its own. QuestionShowAction in questions, WarrantyRow
// in warranties, FAQCategory in faq and StaffRow in staff repeat it; Order and
// Orders in orders, Questionnaire in questions and Staffing in staff do not.
func repeatedPrefix(pkg, name string) int {
	n := 0
	for _, stem := range []string{pkg, singular(pkg)} {
		if stem == "" || len(name) <= len(stem) || !strings.EqualFold(name[:len(stem)], stem) {
			continue
		}
		next, _ := utf8.DecodeRuneInString(name[len(stem):])
		if (unicode.IsUpper(next) || unicode.IsDigit(next)) && len(stem) > n {
			n = len(stem)
		}
	}
	return n
}

// singular is the record a plural package name counts, by English's regular
// plural: warranties gives warranty and questions gives question. A name that
// does not end in s has none. A stem that is not a word (access gives acces)
// matches nothing a Go name would spell.
func singular(pkg string) string {
	switch {
	case strings.HasSuffix(pkg, "ies"):
		return strings.TrimSuffix(pkg, "ies") + "y"
	case strings.HasSuffix(pkg, "s"):
		return strings.TrimSuffix(pkg, "s")
	}
	return ""
}

// TestCheckRepetitiveNames runs the check over fixture packages: each fault
// has a case that must report it, and the names the rule allows must pass.
func TestCheckRepetitiveNames(t *testing.T) {
	t.Parallel()
	const reason = "the generator chooses it"
	for _, tc := range []struct {
		name       string
		files      map[string]string
		exceptions map[string]string
		want       string // a substring of the one problem; "" means none
	}{
		{"named for the package", map[string]string{"foo/foo.go": "package foo\ntype Foo struct{}\nfunc New() Foo { return Foo{} }\n"}, nil, ""},
		{"not exported, a method, a test file", map[string]string{
			"foo/foo.go":      "package foo\ntype fooView struct{}\ntype T struct{}\nfunc (T) FooView() {}\n",
			"foo/foo_test.go": "package foo\ntype FooView struct{}\n",
		}, nil, ""},
		{"one word, not two", map[string]string{"questions/q.go": "package questions\ntype Questionnaire struct{}\ntype Question struct{}\nfunc Questions() {}\n"}, nil, ""},
		{"singular of the package", map[string]string{"questions/q.go": "package questions\nconst QuestionShowAction = 1\n"}, nil, "questions.QuestionShowAction repeats its package; name it ShowAction"},
		{"singular of an -ies package", map[string]string{"warranties/w.go": "package warranties\ntype WarrantyRow struct{}\n"}, nil, "warranties.WarrantyRow repeats its package; name it Row"},
		{"singular excepted with a reason", map[string]string{"orders/o.go": "package orders\ntype View struct{}\ntype OrderView struct{}\n"}, map[string]string{"orders.OrderView": reason}, ""},
		{"a compound's last part", map[string]string{"cart/cartitem/item.go": "package cartitem\ntype Item struct{}\n"}, nil, ""},
		{"type", map[string]string{"foo/foo.go": "package foo\ntype FooView struct{}\n"}, nil, "foo.FooView repeats its package; name it View"},
		{"generated component", map[string]string{"foo/page_templ.go": "// Code generated by templ - DO NOT EDIT.\n\npackage foo\nfunc FooList() {}\n"}, nil, "foo.FooList repeats"},
		{"value in a group", map[string]string{"foo/foo.go": "package foo\nvar (\n\tA, FooLimit = 1, 2\n)\n"}, nil, "foo.FooLimit repeats"},
		{"acronym package", map[string]string{"admin/faq/faq.go": "package faq\ntype FAQCategory struct{}\n"}, nil, "faq.FAQCategory repeats its package; name it Category"},
		{"package name differs from the directory", map[string]string{"order/warranty/page.go": "package warranty\nfunc WarrantyOrder() {}\n"}, nil, "warranty.WarrantyOrder repeats"},
		{"excepted with a reason", map[string]string{"db/db.go": "package db\ntype DBTX interface{}\n"}, map[string]string{"db.DBTX": reason}, ""},
		{"excepted without a reason", map[string]string{"db/db.go": "package db\ntype DBTX interface{}\n"}, map[string]string{"db.DBTX": " "}, "gives no reason"},
		{"exception for a name that is gone", map[string]string{"db/db.go": "package db\ntype Conn interface{}\n"}, map[string]string{"db.DBTX": reason}, "matches no exported name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dirs := map[string]bool{}
			for file, body := range tc.files {
				full := filepath.Join(root, filepath.FromSlash(file))
				if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				dirs[path.Dir(file)] = true
			}
			got, _ := checkRepetitiveNames(root, slices.Sorted(maps.Keys(dirs)), tc.exceptions)
			switch {
			case tc.want == "" && len(got) != 0:
				t.Errorf("reported %q, want no problem", got)
			case tc.want != "" && (len(got) != 1 || !strings.Contains(got[0], tc.want)):
				t.Errorf("reported %q, want one problem containing %q", got, tc.want)
			}
		})
	}
}
