package main

import (
	"fmt"
	"regexp"
	"strings"
)

// finding is a place the tool cannot decide, or a check that failed.
type finding struct {
	File string
	Line int
	Msg  string
}

func (f finding) String() string {
	if f.Line == 0 {
		return f.File + ": " + f.Msg
	}
	return fmt.Sprintf("%s:%d: %s", f.File, f.Line, f.Msg)
}

// rewriteRE finds a Test name, with the test file that declares it when the
// text is a "file#Test" key.
var rewriteRE = regexp.MustCompile(`(?:([A-Za-z0-9_./-]+_test\.go)#)?\b(Test[A-Z0-9_][A-Za-z0-9_]*)`)

// renamer holds the rows of a map and decides, for each mention of an old
// name, which row it means.
//
// A name declared in one directory is the same test wherever it is mentioned.
// A name declared in several directories is the row of the directory whose
// file mentions it, of the directory a file#Test key or a go test command line
// names, and nothing else: any other mention is reported, not guessed.
type renamer struct {
	byName    map[string][]row           // old name -> rename rows
	dirs      map[string]map[string]bool // old name -> directories that declare it (or did)
	runs      map[string][]goTestRun     // command files -> their go test commands
	fileMoves map[string]string          // mv-file rows: old path -> new path
	vanishes  map[string]bool            // files that hold nothing once their tests have moved out
}

func newRenamer(rows []row, s *snapshot, d *declared) *renamer {
	r := &renamer{
		byName:    map[string][]row{},
		dirs:      d.dirsByName(),
		runs:      map[string][]goTestRun{},
		fileMoves: map[string]string{},
		vanishes:  map[string]bool{},
	}
	for _, rw := range rows {
		switch rw.kind() {
		case "rename":
			if !rw.renames() {
				continue
			}
			r.byName[rw.Old] = append(r.byName[rw.Old], rw)
			if r.dirs[rw.Old] == nil {
				r.dirs[rw.Old] = map[string]bool{}
			}
			r.dirs[rw.Old][rw.dir()] = true
		case "mv-file":
			r.fileMoves[rw.File] = rw.NewFile
		}
	}
	r.noteVanishing(rows, s)
	for _, run := range runsIn(s) {
		r.runs[run.file] = append(r.runs[run.file], run)
	}
	return r
}

// noteVanishing finds the files whose every declaration moves out.
func (r *renamer) noteVanishing(rows []row, s *snapshot) {
	out := map[string]int{}
	for _, rw := range rows {
		if rw.kind() == "rename" && rw.dest() != rw.File {
			out[rw.File]++
		}
	}
	for file, n := range out {
		if !s.has(file) || declCount(s.data[file]) == n {
			r.vanishes[file] = true
		}
	}
}

// rowIn returns the row of directory dir.
func rowIn(rows []row, dir string) (row, bool) {
	for _, rw := range rows {
		if rw.dir() == dir {
			return rw, true
		}
	}
	return row{}, false
}

// resolve finds the row a mention of word means. found is false when no row
// applies; a non-nil finding says the mention is ambiguous.
func (r *renamer) resolve(file string, line int, pathKey, word string) (rw row, found bool, f *finding) {
	rows := r.byName[word]
	if len(rows) == 0 {
		return row{}, false, nil
	}
	declDirs := r.dirs[word]
	if len(declDirs) == 1 {
		return rows[0], true, nil
	}
	dir := ""
	switch {
	case pathKey != "" && strings.Contains(pathKey, "/"):
		dir = dirOf(pathKey)
	case pathKey != "" || declDirs[dirOf(file)]:
		dir = dirOf(file)
	case isCommandFile(file):
		return r.inCommand(file, line, word, rows)
	}
	if dir == "" {
		return row{}, false, &finding{file, line, ambiguous(word, declDirs)}
	}
	rw, found = rowIn(rows, dir)
	return rw, found, nil
}

func ambiguous(word string, dirs map[string]bool) string {
	return fmt.Sprintf("%s is declared in %s and this mention does not say which one it means; resolve it by hand",
		word, strings.Join(sortedKeys(dirs), ", "))
}

// inCommand resolves a mention on a go test command line by the packages the
// command runs.
func (r *renamer) inCommand(file string, line int, word string, rows []row) (row, bool, *finding) {
	for _, run := range r.runs[file] {
		if line < run.line || line > run.last {
			continue
		}
		var hits []row
		for _, rw := range rows {
			if run.coversDir(rw.dir()) {
				hits = append(hits, rw)
			}
		}
		if len(hits) > 1 {
			return row{}, false, &finding{file, line, ambiguous(word, r.dirs[word])}
		}
		if len(hits) == 1 {
			return hits[0], true, nil
		}
		return row{}, false, nil
	}
	return row{}, false, &finding{file, line, ambiguous(word, r.dirs[word])}
}

// rewrite returns body with every mention of a renamed test replaced, a moved
// test's file#Test key pointed at its new file, and a moved file's path
// replaced. It also reports the mentions it could not decide.
func (r *renamer) rewrite(file string, body []byte) ([]byte, []finding) {
	text := string(body)
	var (
		out      strings.Builder
		findings []finding
		last     int
		line     = 1
		prev     int
		moved    = map[string]map[string]bool{} // old file -> new files of tests mentioned here
	)
	for _, m := range rewriteRE.FindAllStringSubmatchIndex(text, -1) {
		line += strings.Count(text[prev:m[0]], "\n")
		prev = m[0]
		pathKey := ""
		if m[2] >= 0 {
			pathKey = text[m[2]:m[3]]
		}
		rw, found, f := r.resolve(file, line, pathKey, text[m[4]:m[5]])
		if f != nil {
			findings = append(findings, *f)
		}
		if !found {
			continue
		}
		out.WriteString(text[last:m[0]])
		out.WriteString(replacement(rw, pathKey, moved))
		last = m[1]
	}
	out.WriteString(text[last:])
	result, more := r.followMoves(file, out.String(), moved)
	for from, to := range r.fileMoves {
		result = replacePath(result, from, to)
	}
	return []byte(result), append(findings, more...)
}

// replacement is the text that takes the place of a mention of rw's old name.
// A plain mention of a test that moves is noted in moved.
func replacement(rw row, pathKey string, moved map[string]map[string]bool) string {
	if pathKey != "" {
		if pathKey == rw.File {
			pathKey = rw.dest()
		}
		return pathKey + "#" + rw.New
	}
	if rw.dest() != rw.File {
		if moved[rw.File] == nil {
			moved[rw.File] = map[string]bool{}
		}
		moved[rw.File][rw.dest()] = true
	}
	return rw.New
}

// followMoves points a mention of a test file at the file its tests moved to,
// when the file is gone and they all went to one place; otherwise it reports the
// mention.
func (r *renamer) followMoves(file, text string, moved map[string]map[string]bool) (string, []finding) {
	var findings []finding
	for _, from := range sortedKeys(moved) {
		tos := sortedKeys(moved[from])
		if len(tos) == 1 && r.vanishes[from] {
			text = replacePath(text, from, tos[0])
			continue
		}
		if pathMentions(text, from) {
			findings = append(findings, finding{file, 0, fmt.Sprintf(
				"names %s and a test that moved to %s, and %s: decide by hand which file this mention should name",
				from, strings.Join(tos, " and "), keepsOrSplits(r.vanishes[from]))})
		}
	}
	return text, findings
}

// replacePath replaces from by to wherever from stands as a whole path: not
// as the tail of a longer path or the head of a longer name.
func replacePath(text, from, to string) string {
	var out strings.Builder
	last := 0
	for at := nextPath(text, from, 0); at >= 0; at = nextPath(text, from, at+len(from)) {
		out.WriteString(text[last:at] + to)
		last = at + len(from)
	}
	if last == 0 {
		return text
	}
	return out.String() + text[last:]
}

// nextPath returns the offset of the first whole-path occurrence of from at or
// after start, or -1.
func nextPath(text, from string, start int) int {
	for start <= len(text) {
		i := strings.Index(text[start:], from)
		if i < 0 {
			return -1
		}
		at := start + i
		end := at + len(from)
		inPath := at > 0 && isPathChar(text[at-1])
		inWord := end < len(text) && isWordChar(text[end])
		if !inPath && !inWord {
			return at
		}
		start = end
	}
	return -1
}

func isWordChar(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isPathChar(c byte) bool { return isWordChar(c) || c == '.' || c == '/' || c == '-' }

// pathMentions reports whether text names the file path whole.
func pathMentions(text, file string) bool { return nextPath(text, file, 0) >= 0 }

func keepsOrSplits(vanishes bool) string {
	if vanishes {
		return "its tests went to several files"
	}
	return "it keeps other tests"
}
