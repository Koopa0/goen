package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// ownDir is this tool's own directory. Its fixtures name tests that do not
// exist, so no subcommand reads or edits it.
const ownDir = "internal/tools/testrename/"

// historyDirs are not rewritten. docs/reviews records what each review round
// said under names since changed (TestEveryNamedTestExists skips it for the
// same reason); docs/test-renames holds the old names on purpose.
var historyDirs = []string{"docs/reviews/", "docs/test-renames/", ownDir}

// tree is a set of files at one moment: the working tree or a git revision.
type tree interface {
	// files returns the slash paths, relative to the repository root, of
	// every file the tree holds.
	files() ([]string, error)
	read(name string) ([]byte, error)
}

// workTree is the checked-out directory. Inside a git checkout it lists the
// files git tracks or would track (untracked files that are not ignored);
// elsewhere it walks the directory. Files that no longer exist are dropped.
type workTree struct{ root string }

func (w workTree) files() ([]string, error) {
	if _, err := os.Stat(filepath.Join(w.root, ".git")); err != nil {
		return w.walk()
	}
	out, err := gitOutput(w.root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var names []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name == "" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(w.root, filepath.FromSlash(name))); err == nil {
			names = append(names, name)
		}
	}
	return names, nil
}

func (w workTree) walk() ([]string, error) {
	var names []string
	err := filepath.WalkDir(w.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(w.root, p)
		if err != nil {
			return err
		}
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	return names, err
}

func (w workTree) read(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(w.root, filepath.FromSlash(name)))
}

// gitTree is the content of a git revision.
type gitTree struct{ root, ref string }

func (g gitTree) files() ([]string, error) {
	out, err := gitOutput(g.root, "ls-tree", "-r", "-z", "--name-only", g.ref)
	if err != nil {
		return nil, err
	}
	var names []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

func (g gitTree) read(name string) ([]byte, error) {
	return gitOutput(g.root, "show", g.ref+":"+name)
}

// readAll reads many files with one git process.
func (g gitTree) readAll(names []string) (map[string][]byte, error) {
	cmd := exec.CommandContext(context.Background(), "git", "-C", g.root, "cat-file", "--batch") //nolint:gosec // G204: the revision is the command line's.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	writeErr := make(chan error, 1)
	go func() { writeErr <- writeNames(stdin, g.ref, names) }()
	out, readErr := readBatch(bufio.NewReader(stdout), names)
	if err := cmd.Wait(); err != nil && readErr == nil {
		readErr = err
	}
	if err := <-writeErr; err != nil && readErr == nil {
		readErr = err
	}
	return out, readErr
}

// writeNames sends one "ref:name" line per file and closes the pipe.
func writeNames(stdin io.WriteCloser, ref string, names []string) error {
	w := bufio.NewWriter(stdin)
	for _, name := range names {
		if _, err := w.WriteString(ref + ":" + name + "\n"); err != nil {
			return errors.Join(err, stdin.Close())
		}
	}
	return errors.Join(w.Flush(), stdin.Close())
}

// readBatch parses the output of git cat-file --batch: a header line
// "oid type size", the content, a newline.
func readBatch(r *bufio.Reader, names []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(names))
	for _, name := range names {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			return nil, fmt.Errorf("read %s: git says %q", name, strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		body := make([]byte, size+1)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		out[name] = body[:size]
	}
	return out, nil
}

func gitOutput(root string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", root}, args...)...) //nolint:gosec // G204: the arguments are this tool's own.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// snapshot is the text of a tree, read once.
type snapshot struct {
	paths []string // sorted
	data  map[string][]byte
}

func (s *snapshot) has(name string) bool { _, ok := s.data[name]; return ok }

// bulkReader is a tree that reads many files at once.
type bulkReader interface {
	readAll(names []string) (map[string][]byte, error)
}

// loadSnapshot reads every text file of t except hidden directories (other
// than .github), node_modules, the history directories and files with a NUL
// byte.
func loadSnapshot(t tree) (*snapshot, error) {
	all, err := t.files()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, name := range all {
		if !skippedPath(name) {
			names = append(names, name)
		}
	}
	bodies, err := readFiles(t, names)
	if err != nil {
		return nil, err
	}
	s := &snapshot{data: map[string][]byte{}}
	for _, name := range names {
		body := bodies[name]
		if bytes.IndexByte(body[:min(len(body), 8000)], 0) >= 0 {
			continue
		}
		s.data[name] = body
		s.paths = append(s.paths, name)
	}
	slices.Sort(s.paths)
	return s, nil
}

func readFiles(t tree, names []string) (map[string][]byte, error) {
	if b, ok := t.(bulkReader); ok {
		return b.readAll(names)
	}
	out := make(map[string][]byte, len(names))
	for _, name := range names {
		body, err := t.read(name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		out[name] = body
	}
	return out, nil
}

func skippedPath(name string) bool {
	for _, h := range historyDirs {
		if strings.HasPrefix(name, h) {
			return true
		}
	}
	segs := strings.Split(name, "/")
	for _, seg := range segs[:len(segs)-1] {
		if seg == "node_modules" || (strings.HasPrefix(seg, ".") && seg != ".github") {
			return true
		}
	}
	return false
}

// dirOf returns the directory of a slash path ("." for the root).
func dirOf(name string) string { return path.Dir(name) }
