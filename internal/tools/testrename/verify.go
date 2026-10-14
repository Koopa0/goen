package main

import (
	"fmt"
	"hash/fnv"
	"slices"
	"strings"
)

// exemptMarker excuses a line that names an old test on purpose; it is the
// marker TestEveryNamedTestExists honours.
const exemptMarker = "named-test-exempt:"

// verification is the comparison of a tree before a map was applied with the
// tree after.
type verification struct {
	rows     []row
	base     *snapshot
	after    *snapshot
	bd, ad   *declared
	rn       *renamer
	byOld    map[key]row
	moved    map[string]string
	findings []finding
	seen     map[string]bool
	// counts, for the report
	before, now map[string]int
	runs        int
}

func (v *verification) add(file string, line int, format string, args ...any) {
	f := finding{file, line, fmt.Sprintf(format, args...)}
	if !v.seen[f.String()] {
		v.seen[f.String()] = true
		v.findings = append(v.findings, f)
	}
}

// verify checks that after is base with rows applied: no test was lost or
// added, every renamed test has the body it had, no old name survives, no
// path to a vanished test file is left, and every go test -run line selects
// what it selected.
func verify(base, after *snapshot, rows []row) (*verification, error) {
	bd, err := scanDecls(base)
	if err != nil {
		return nil, fmt.Errorf("base: %w", err)
	}
	ad, err := scanDecls(after)
	if err != nil {
		return nil, fmt.Errorf("after: %w", err)
	}
	v := &verification{
		rows: rows, base: base, after: after, bd: bd, ad: ad,
		rn: newRenamer(rows, base, bd), byOld: map[key]row{}, moved: map[string]string{},
		seen: map[string]bool{}, before: map[string]int{}, now: map[string]int{},
	}
	for _, rw := range rows {
		if rw.kind() == "mv-file" {
			v.moved[rw.File] = rw.NewFile
			continue
		}
		v.byOld[key{rw.dir(), rw.Old}] = rw
		if rw.kind() == "todo" {
			v.add(rw.File, rw.line, "%s has an unfinished row (action todo)", rw.Old)
		}
	}
	for _, set := range tagSets {
		v.compareTests(set)
	}
	v.checkSurvivors()
	v.checkPaths()
	v.checkRuns()
	return v, nil
}

func (v *verification) newFile(file string) string {
	if to, ok := v.moved[file]; ok {
		return to
	}
	return file
}

// compareTests holds the listing of one tag set up against the map.
func (v *verification) compareTests(set []string) {
	lb, la := v.bd.list(set), v.ad.list(set)
	name := tagSetName(set)
	v.before[name], v.now[name] = len(lb), len(la)
	expected := map[key]bool{}
	for _, k := range sortedKeysOf(lb) {
		for _, want := range v.expect(k, lb[k], la) {
			expected[want] = true
		}
	}
	for _, k := range sortedKeysOf(la) {
		if !expected[k] {
			v.add(la[k][0].File, 0, "%s is a test that neither the base nor the map accounts for (%s)", k.Name, name)
		}
	}
}

// expect compares one base test with the after listing and returns the keys
// the after listing may hold for it.
func (v *verification) expect(k key, before []decl, after map[key][]decl) []key {
	rw, mapped := v.byOld[k]
	if !mapped {
		v.sameTest(before, k.Name, after[k], k)
		return []key{k}
	}
	switch rw.kind() {
	case "rename":
		nk := key{k.Dir, rw.New}
		v.sameTest(before, rw.New, after[nk], nk)
		return []key{nk}
	case "case-of", "split":
		var out []key
		for _, n := range rw.after() {
			nk := key{k.Dir, n}
			out = append(out, nk)
			if len(after[nk]) == 0 {
				v.add(rw.File, rw.line, "%s became %s by hand, but %s is not declared in %s", rw.Old, n, n, k.Dir)
			}
		}
		return out
	}
	return nil // audit-delete, todo: nothing is expected, and an old name that survives is reported as a survivor
}

// sameTest requires a test to be declared once more, in the right file, with
// the body it had once the map's renames are applied to the body.
func (v *verification) sameTest(before []decl, newName string, after []decl, nk key) {
	if len(after) == 0 {
		v.add(before[0].File, 0, "%s was declared in %s and is gone: no test may be lost", newName, nk.Dir)
		return
	}
	if len(after) != len(before) {
		v.add(before[0].File, 0, "%s is declared %d times in %s afterwards, %d before", newName, len(after), nk.Dir, len(before))
		return
	}
	for i, b := range before {
		a := after[i]
		wantFile := v.newFile(b.File)
		if rw, ok := v.byOld[key{b.Dir, b.Name}]; ok && rw.kind() == "rename" {
			wantFile = v.newFile(rw.dest())
		}
		if a.File != wantFile {
			v.add(a.File, 0, "%s is in %s, want %s", newName, a.File, wantFile)
		}
		want := v.bodyTokens(b, v.base.data[b.File], true)
		got := v.bodyTokens(a, v.after.data[a.File], false)
		if !slices.Equal(want, got) {
			v.add(a.File, 0, "body of %s changed: hash %s at the base (renamed as the map says), %s now", newName, hashOf(want), hashOf(got))
		}
	}
}

func (v *verification) bodyTokens(d decl, src []byte, base bool) []string {
	fix := func(lit string) string { return lit }
	if base {
		fix = func(lit string) string {
			out, _ := v.rn.rewrite(d.File, []byte(lit))
			return string(out)
		}
	}
	return tokens(src, d.BodyStart, d.End, fix)
}

// hashOf names a token stream in a message: a hash for a person to compare, not
// a digest of anything secret.
func hashOf(toks []string) string {
	h := fnv.New64a()
	for _, tok := range toks {
		_, _ = h.Write([]byte(tok + "\x00"))
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

// checkSurvivors looks for an old name that is still mentioned. It does not use
// apply's rewriting, so a rewrite that skips a kind of mention is seen here. A
// name that is still declared elsewhere is looked for only in the files of the
// directory that renamed it.
func (v *verification) checkSurvivors() {
	stillDeclared := v.ad.dirsByName()
	olds := map[string]string{} // old name -> directory that renamed it
	for _, rw := range v.rows {
		if rw.kind() != "mv-file" && rw.kind() != "todo" && rw.Old != rw.New && !stillDeclared[rw.Old][rw.dir()] {
			olds[rw.Old] = rw.dir()
		}
	}
	for _, name := range v.after.paths {
		text := string(v.after.data[name])
		for _, at := range nameRE.FindAllStringIndex(text, -1) {
			word := text[at[0]:at[1]]
			dir, ok := olds[word]
			if !ok || (len(stillDeclared[word]) > 0 && dirOf(name) != dir) {
				continue
			}
			line := lineOfString(text, at[0])
			if !strings.Contains(lineText(text, at[0]), exemptMarker) {
				v.add(name, line, "the old name %s survives", word)
			}
		}
	}
}

func lineOfString(text string, off int) int { return 1 + strings.Count(text[:off], "\n") }

// lineText returns the line that holds offset off.
func lineText(text string, off int) string {
	start := strings.LastIndexByte(text[:off], '\n') + 1
	end := strings.IndexByte(text[off:], '\n')
	if end < 0 {
		return text[start:]
	}
	return text[start : off+end]
}

// checkPaths reports a mention of a test file that the map removed.
func (v *verification) checkPaths() {
	for _, name := range v.base.paths {
		if !strings.HasSuffix(name, "_test.go") || v.after.has(name) {
			continue
		}
		for _, other := range v.after.paths {
			if pathMentions(string(v.after.data[other]), name) {
				v.add(other, 0, "names %s, which no longer exists (it moved to %s)", name, v.newFile(name))
			}
		}
	}
}

// checkRuns holds each go test -run line to what it selected at the base.
func (v *verification) checkRuns() {
	before, after := groupRuns(runsIn(v.base)), groupRuns(runsIn(v.after))
	for _, file := range sortedKeys(before) {
		if len(before[file]) != len(after[file]) {
			v.add(file, 0, "has %d go test -run commands, %d at the base", len(after[file]), len(before[file]))
			continue
		}
		for i, b := range before[file] {
			v.checkRun(b, after[file][i])
		}
	}
}

func groupRuns(runs []goTestRun) map[string][]goTestRun {
	out := map[string][]goTestRun{}
	for _, r := range runs {
		out[r.file] = append(out[r.file], r)
	}
	return out
}

func (v *verification) checkRun(b, a goTestRun) {
	v.runs++
	var set []string
	if b.integration {
		set = []string{"integration"}
	}
	was, err := b.selected(v.bd.list(set))
	if err != nil {
		v.add(b.file, b.line, "-run %q does not compile: %v", b.pattern, err)
		return
	}
	now, err := a.selected(v.ad.list(set))
	if err != nil {
		v.add(a.file, a.line, "-run %q does not compile: %v", a.pattern, err)
		return
	}
	var want []string
	for _, w := range was {
		want = append(want, v.afterNames(w)...)
	}
	slices.Sort(want)
	slices.Sort(now)
	if !slices.Equal(slices.Compact(want), slices.Compact(now)) {
		v.add(a.file, a.line, "-run %q selected %s at the base and now selects %s; the map says it should select %s",
			a.pattern, listOf(was), listOf(now), listOf(want))
	}
}

// afterNames maps "dir name" through the map.
func (v *verification) afterNames(dirName string) []string {
	dir, name, _ := strings.Cut(dirName, " ")
	rw, ok := v.byOld[key{dir, name}]
	if !ok {
		return []string{dirName}
	}
	var out []string
	for _, n := range rw.after() {
		out = append(out, dir+" "+n)
	}
	return out
}

func listOf(xs []string) string {
	xs = slices.Clone(xs)
	slices.Sort(xs)
	return "[" + strings.Join(xs, ", ") + "]"
}

func sortedKeysOf[V any](m map[key]V) []key {
	keys := make([]key, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b key) int {
		return strings.Compare(a.Dir+" "+a.Name, b.Dir+" "+b.Name)
	})
	return keys
}

// checkGoList compares the untagged listing of every directory of the map with
// what go test -list prints, so the syntax-tree listing is itself checked.
func (v *verification) checkGoList(root string) {
	dirs := map[string]bool{}
	for _, rw := range v.rows {
		dirs[rw.dir()] = true
	}
	la := v.ad.list(nil)
	for _, dir := range sortedKeys(dirs) {
		out, err := goTestList(root, dir)
		if err != nil {
			v.add(dir, 0, "go test -list failed: %v", err)
			continue
		}
		var want []string
		for k := range la {
			if k.Dir == dir {
				want = append(want, k.Name)
			}
		}
		slices.Sort(want)
		if !slices.Equal(want, out) {
			v.add(dir, 0, "go test -list prints %s, the syntax tree lists %s", listOf(out), listOf(want))
		}
	}
}
