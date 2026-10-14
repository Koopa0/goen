package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// result is what applying rows changes: the new content of every changed or
// created file, and the files that vanish.
type result struct {
	changed  map[string][]byte
	removed  []string
	findings []finding
	pending  int // rows applied now
	done     int // rows found already applied
}

// rowState is where a rename or mv-file row stands in a tree.
type rowState int

const (
	statePending rowState = iota
	stateDone
)

// naming is what a map does to the names of one directory.
type naming struct {
	away    map[key]bool // names a rename row takes away
	becomes map[key]bool // names a rename row gives
	moved   map[string]string
	s       *snapshot
	d       *declared
}

func newNaming(rows []row, s *snapshot, d *declared) naming {
	n := naming{away: map[key]bool{}, becomes: map[key]bool{}, moved: map[string]string{}, s: s, d: d}
	for _, rw := range rows {
		switch rw.kind() {
		case "rename":
			if rw.New != rw.Old {
				n.away[key{rw.dir(), rw.Old}] = true
				n.becomes[key{rw.dir(), rw.New}] = true
			}
		case "mv-file":
			n.moved[rw.File] = rw.NewFile
		}
	}
	return n
}

// file follows a file the map moves: before the move it is itself, afterwards
// its new name.
func (n naming) file(name string) string {
	if to, ok := n.moved[name]; ok && !n.s.has(name) {
		return to
	}
	return name
}

func (n naming) declaredIn(name, file string) bool {
	return slices.ContainsFunc(n.d.all, func(x decl) bool { return x.File == file && x.Name == name })
}

func (n naming) inDir(dir, name string) bool {
	return slices.ContainsFunc(n.d.all, func(x decl) bool { return x.Dir == dir && x.Name == name })
}

// state decides whether a rename row is still to do or already done, and
// refuses a row the tree contradicts.
func (n naming) state(rw row) (rowState, error) {
	from, to := n.file(rw.File), n.file(rw.dest())
	dir := rw.dir()
	if rw.Old == rw.New {
		switch {
		case n.declaredIn(rw.Old, from) && from != to:
			return statePending, nil
		case n.declaredIn(rw.Old, to) && !n.declaredIn(rw.Old, from):
			return stateDone, nil
		}
		return 0, fmt.Errorf("%s is declared in neither %s nor %s", rw.Old, rw.File, rw.dest())
	}
	freeNew := !n.inDir(dir, rw.New) || n.away[key{dir, rw.New}]
	freeOld := !n.inDir(dir, rw.Old) || n.becomes[key{dir, rw.Old}]
	switch {
	case n.declaredIn(rw.Old, from) && freeNew:
		return statePending, nil
	case n.declaredIn(rw.New, to) && freeOld:
		return stateDone, nil
	case n.declaredIn(rw.Old, from):
		return 0, fmt.Errorf("%s is already declared in %s", rw.New, dir)
	}
	return 0, fmt.Errorf("%s is not declared in %s, and %s is not declared in %s", rw.Old, rw.File, rw.New, rw.dest())
}

// applyRows computes the tree after the rows are applied. Applying rows that
// are already applied changes nothing, but still rewrites mentions that use an
// old name: that is how a branch that rebased onto a renamed main is repaired.
func applyRows(s *snapshot, rows []row) (*result, error) {
	d, err := scanDecls(s)
	if err != nil {
		return nil, err
	}
	rn := newRenamer(rows, s, d)
	res := &result{changed: map[string][]byte{}}
	moves, err := plan(rows, s, newNaming(rows, s, d), res)
	if err != nil {
		return nil, err
	}
	for _, name := range s.paths {
		out, found := rn.rewrite(name, s.data[name])
		res.findings = append(res.findings, found...)
		if !bytes.Equal(out, s.data[name]) {
			res.changed[name] = out
		}
	}
	if err := finish(s, res, moves, rn.fileMoves); err != nil {
		return nil, err
	}
	return res, nil
}

// plan decides, for each rename and mv-file row, whether it is still to do, and
// returns the function moves among them.
func plan(rows []row, s *snapshot, nm naming, res *result) ([]move, error) {
	var moves []move
	for _, rw := range rows {
		pending, err := isPending(rw, s, nm)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", rw.line, err)
		}
		if rw.kind() != "rename" && rw.kind() != "mv-file" {
			continue
		}
		if !pending {
			res.done++
			continue
		}
		res.pending++
		if rw.kind() == "rename" && rw.dest() != rw.File {
			moves = append(moves, move{from: rw.File, to: rw.dest(), name: rw.New})
		}
	}
	return moves, nil
}

func isPending(rw row, s *snapshot, nm naming) (bool, error) {
	switch rw.kind() {
	case "rename":
		st, err := nm.state(rw)
		return st == statePending, err
	case "mv-file":
		return s.has(rw.File), checkFileMove(rw, s)
	}
	return false, nil
}

func checkFileMove(rw row, s *snapshot) error {
	switch {
	case s.has(rw.File) && s.has(rw.NewFile):
		return fmt.Errorf("%s exists, so %s cannot be moved to it", rw.NewFile, rw.File)
	case !s.has(rw.File) && !s.has(rw.NewFile):
		return fmt.Errorf("neither %s nor %s exists", rw.File, rw.NewFile)
	}
	return nil
}

// finish performs the function moves and file moves on the rewritten content,
// then formats what changed.
func finish(s *snapshot, res *result, moves []move, fileMoves map[string]string) error {
	files := map[string][]byte{}
	for _, m := range moves {
		for _, name := range []string{m.from, m.to} {
			if body, ok := res.content(s, name); ok {
				files[name] = body
			}
		}
	}
	if err := moveDecls(files, moves, modulePathOf(s)); err != nil {
		return err
	}
	for name, body := range files {
		if s.has(name) && emptyShell(body) {
			delete(res.changed, name)
			res.removed = append(res.removed, name)
			continue
		}
		res.changed[name] = body
	}
	for name, body := range res.changed {
		if strings.HasSuffix(name, ".go") && s.has(name) && !movedInto(moves, name) {
			res.changed[name] = formatStable(s.data[name], body)
		}
	}
	res.moveFiles(s, fileMoves)
	slices.Sort(res.removed)
	return nil
}

// content returns the rewritten content of a file, or its content in s.
func (r *result) content(s *snapshot, name string) ([]byte, bool) {
	if body, ok := r.changed[name]; ok {
		return body, true
	}
	body, ok := s.data[name]
	return body, ok
}

// moveFiles renames the files the mv-file rows name.
func (r *result) moveFiles(s *snapshot, fileMoves map[string]string) {
	for from, to := range fileMoves {
		if !s.has(from) {
			continue
		}
		body, _ := r.content(s, from)
		r.changed[to] = body
		delete(r.changed, from)
		r.removed = append(r.removed, from)
	}
}

// declCount is the number of top-level declarations of a Go file besides imports.
func declCount(src []byte) int {
	file, err := parser.ParseFile(token.NewFileSet(), "", src, parser.SkipObjectResolution)
	if err != nil {
		return -1
	}
	n := 0
	for _, d := range file.Decls {
		if g, ok := d.(*ast.GenDecl); !ok || g.Tok != token.IMPORT {
			n++
		}
	}
	return n
}

// emptyShell reports whether a Go file holds nothing but its package clause and
// imports: the test file a move leaves behind when its last function goes.
func emptyShell(src []byte) bool { return declCount(src) == 0 }

func movedInto(moves []move, name string) bool {
	return slices.ContainsFunc(moves, func(m move) bool { return m.from == name || m.to == name })
}

// modulePathOf reads the module path from go.mod.
func modulePathOf(s *snapshot) string {
	for line := range strings.SplitSeq(string(s.data["go.mod"]), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

// writeResult stores res in the directory root.
func writeResult(root string, res *result) error {
	for _, name := range res.removed {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			return err
		}
	}
	for _, name := range sortedKeys(res.changed) {
		p := filepath.Join(root, filepath.FromSlash(name))
		mode := os.FileMode(0o644)
		if info, err := os.Stat(p); err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(p, res.changed[name], mode); err != nil {
			return err
		}
	}
	return nil
}
