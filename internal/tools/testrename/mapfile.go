package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
)

// A map is a tab-separated file under docs/test-renames/, one row per test:
//
//	file	old	new	unit	source	action	newfile
//
// file is the slash path, relative to the repository root, of the file that
// declares old. The columns:
//
//	new      the test's name afterwards; for case-of, the case's name; for
//	         split, the comma-separated names; "-" for audit-delete
//	unit     the function, type or type_method the test is named after, or ""
//	source   prefix, callee or hand: how the unit was found
//	action   rename, todo, mv-file, split, audit-delete #N or case-of:NAME
//	newfile  the file the test moves to, or "" to stay
//
// rename and mv-file rows are applied mechanically. todo rows are propose's
// unfinished guesses and fail verify. split, case-of and audit-delete rows
// record hand work that apply does not do and verify only checks the result of.
const mapHeader = "file\told\tnew\tunit\tsource\taction\tnewfile"

type row struct {
	File, Old, New, Unit, Source, Action, NewFile string
	line                                          int
}

// dir is the directory whose tests the row concerns.
func (r row) dir() string { return path.Dir(r.File) }

// kind is the action without its argument.
func (r row) kind() string {
	switch {
	case r.Action == "rename", r.Action == "todo", r.Action == "mv-file", r.Action == "split":
		return r.Action
	case strings.HasPrefix(r.Action, "audit-delete"):
		return "audit-delete"
	case strings.HasPrefix(r.Action, "case-of:"):
		return "case-of"
	}
	return ""
}

// renames reports whether the row changes the test's name or file and is
// done by apply.
func (r row) renames() bool {
	return r.kind() == "rename" && (r.New != r.Old || r.dest() != r.File)
}

// dest is the file the test ends up in.
func (r row) dest() string {
	if r.NewFile != "" {
		return r.NewFile
	}
	return r.File
}

// after returns the names that stand for the row's test once the map is done.
func (r row) after() []string {
	switch r.kind() {
	case "rename":
		return []string{r.New}
	case "case-of":
		return []string{strings.TrimPrefix(r.Action, "case-of:")}
	case "split":
		return strings.Split(r.New, ",")
	}
	return nil
}

var testName = regexp.MustCompile(`^Test[A-Z0-9_][A-Za-z0-9_]*$`)

// readMap parses and checks one map file.
func readMap(name string) ([]row, error) {
	body, err := os.ReadFile(name) //nolint:gosec // G304: the map named on the command line.
	if err != nil {
		return nil, err
	}
	return parseMap(name, bytes.NewReader(body))
}

func parseMap(name string, r io.Reader) ([]row, error) {
	var rows []row
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for n := 1; sc.Scan(); n++ {
		text := sc.Text()
		if n == 1 {
			if text != mapHeader {
				return nil, fmt.Errorf("%s:1: header is %q, want %q", name, text, mapHeader)
			}
			continue
		}
		if strings.TrimSpace(text) == "" || strings.HasPrefix(text, "#") {
			continue
		}
		cols := strings.Split(text, "\t")
		if len(cols) != 7 {
			return nil, fmt.Errorf("%s:%d: %d columns, want 7", name, n, len(cols))
		}
		rw := row{File: cols[0], Old: cols[1], New: cols[2], Unit: cols[3], Source: cols[4], Action: cols[5], NewFile: cols[6], line: n}
		if err := rw.check(); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", name, n, err)
		}
		rows = append(rows, rw)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := checkUnique(rows); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return rows, nil
}

// check refuses a row that is malformed whatever the tree holds.
func (r row) check() error {
	k := r.kind()
	if k == "" {
		return fmt.Errorf("unknown action %q", r.Action)
	}
	if !strings.HasSuffix(r.File, "_test.go") || strings.Contains(r.File, `\`) {
		return fmt.Errorf("file %q is not a slash path to a _test.go file", r.File)
	}
	if r.NewFile != "" && (!strings.HasSuffix(r.NewFile, "_test.go") || path.Dir(r.NewFile) != r.dir()) {
		return fmt.Errorf("newfile %q must be a _test.go file in %s", r.NewFile, r.dir())
	}
	if k == "mv-file" {
		return r.checkFileMove()
	}
	return r.checkNames(k)
}

func (r row) checkFileMove() error {
	if r.Old != "-" || r.New != "-" || r.NewFile == "" || r.NewFile == r.File {
		return errors.New("mv-file takes old and new \"-\" and a newfile other than file")
	}
	return nil
}

func (r row) checkNames(k string) error {
	if !testName.MatchString(r.Old) {
		return fmt.Errorf("old %q is not a Test name", r.Old)
	}
	if k == "audit-delete" {
		return nil
	}
	for _, n := range r.after() {
		if !testName.MatchString(n) {
			return fmt.Errorf("%q is not a Test name", n)
		}
	}
	return nil
}

// checkUnique refuses two rows for one test, and two rows that make one name in
// one directory.
func checkUnique(rows []row) error {
	old := map[string]int{}
	became := map[string]int{}
	moved := map[string]int{}
	for _, r := range rows {
		if r.kind() == "mv-file" {
			if prev, ok := moved[r.File]; ok {
				return fmt.Errorf("line %d: %s is moved twice (line %d)", r.line, r.File, prev)
			}
			moved[r.File] = r.line
			continue
		}
		k := r.dir() + " " + r.Old
		if prev, ok := old[k]; ok {
			return fmt.Errorf("line %d: %s in %s has two rows (line %d)", r.line, r.Old, r.dir(), prev)
		}
		old[k] = r.line
		if r.kind() == "audit-delete" || r.kind() == "todo" {
			continue
		}
		for _, n := range r.after() {
			k := r.dir() + " " + n
			if prev, ok := became[k]; ok && r.kind() != "case-of" {
				return fmt.Errorf("line %d: two tests become %s in %s (line %d)", r.line, n, r.dir(), prev)
			}
			became[k] = r.line
		}
	}
	return nil
}

// inverse returns the rows that undo rows: every rename and move read backwards.
// Rows that record hand work have no inverse and are dropped.
func inverse(rows []row) []row {
	var out []row
	for _, r := range rows {
		switch r.kind() {
		case "rename":
			back := row{File: r.dest(), Old: r.New, New: r.Old, Unit: r.Unit, Source: r.Source, Action: "rename", line: r.line}
			if r.dest() != r.File {
				back.NewFile = r.File
			}
			out = append(out, back)
		case "mv-file":
			out = append(out, row{File: r.NewFile, Old: "-", New: "-", Action: "mv-file", NewFile: r.File, line: r.line})
		}
	}
	return out
}

func writeMap(w io.Writer, rows []row) error {
	if _, err := fmt.Fprintln(w, mapHeader); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.File, r.Old, r.New, r.Unit, r.Source, r.Action, r.NewFile); err != nil {
			return err
		}
	}
	return nil
}
