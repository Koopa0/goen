// Command testrename renames Go tests without losing one and without leaving a
// stale reference to the old name. It exists for the migration that gives every
// test a name that follows the repository's convention (see CONTRIBUTING.md),
// and is deleted by the migration's last pull request.
//
// A migration is a set of maps, one file per package under
// internal/tools/testrename/maps (committed there, because git ignores most of
// docs/): tab-separated rows of file, old name, new name, unit, source, action and new
// file. The subcommands:
//
//	inventory           count where the repository names an existing test: go
//	                    test -run lines, file#Test keys, strings, comments and
//	                    prose, and which guard notices a rename
//	propose DIR...      write a map for each package directory, naming each test
//	                    after the unit it exercises where that can be found
//	apply MAP...        rename the declarations, their doc comments and every
//	                    mention in every text file, and move tests between files
//	verify MAP...       compare the tree with a git revision (-base, default
//	                    HEAD) through the map: no test lost or added, every body
//	                    unchanged, no old name left, every -run line selecting
//	                    what it selected
//
// apply -inverse and verify -inverse use the maps backwards. apply is
// idempotent, so a branch that rebased onto a renamed main runs it again to
// repair the references it added.
//
// The tool lists tests from the syntax tree, not with go test -list: with the
// integration tag that runs TestMain, which starts Docker. It never reads
// docs/reviews (a record of what was said) or its own directory, which holds
// the maps (the old names on purpose).
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage: testrename [-C DIR] inventory
       testrename [-C DIR] propose [-stdout] PKGDIR...
       testrename [-C DIR] apply [-inverse] MAP...
       testrename [-C DIR] verify [-base REV] [-inverse] [-golist] MAP...
`

func run(args []string, stdout, stderr io.Writer) int {
	out, errs := &printer{w: stdout}, &printer{w: stderr}
	fs := flag.NewFlagSet("testrename", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("C", ".", "repository root")
	fs.Usage = func() { errs.printf("%s", usage) }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return 2
	}
	var err error
	switch rest[0] {
	case "inventory":
		err = runInventory(*root, out)
	case "propose":
		err = runPropose(*root, rest[1:], out)
	case "apply":
		err = runApply(*root, rest[1:], out, errs)
	case "verify":
		err = runVerify(*root, rest[1:], out, errs)
	default:
		fs.Usage()
		return 2
	}
	if err != nil {
		errs.printf("testrename: %v\n", err)
	}
	if err != nil || out.err != nil || errs.err != nil {
		return 1
	}
	return 0
}

func runInventory(root string, out *printer) error {
	s, err := loadSnapshot(workTree{root})
	if err != nil {
		return err
	}
	return inventory(s, out.w)
}

func runPropose(root string, args []string, out *printer) error {
	fs := flag.NewFlagSet("propose", flag.ContinueOnError)
	toStdout := fs.Bool("stdout", false, "print the map instead of writing it under "+mapsDir)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("propose needs a package directory")
	}
	s, err := loadSnapshot(workTree{root})
	if err != nil {
		return err
	}
	for _, dir := range fs.Args() {
		dir = strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(dir), "./"), "/")
		rows, err := propose(root, s, dir)
		if err != nil {
			return err
		}
		if err := emitMap(root, dir, rows, *toStdout, out); err != nil {
			return err
		}
	}
	return nil
}

func emitMap(root, dir string, rows []row, toStdout bool, out *printer) error {
	var buf bytes.Buffer
	if err := writeMap(&buf, rows); err != nil {
		return err
	}
	if toStdout {
		out.printf("%s", buf.String())
		return nil
	}
	name := filepath.Join(root, filepath.FromSlash(mapPath(dir)))
	if err := os.MkdirAll(filepath.Dir(name), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(name, buf.Bytes(), 0o600); err != nil {
		return err
	}
	out.printf("%s: %d rows (%s)\n", mapPath(dir), len(rows), sourceCounts(rows))
	return nil
}

func sourceCounts(rows []row) string {
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Source]++
	}
	keys := sortedKeys(counts)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, counts[k]))
	}
	return strings.Join(parts, ", ")
}

// readMaps reads the maps named on the command line as one set of rows.
func readMaps(names []string, back bool) ([]row, error) {
	if len(names) == 0 {
		return nil, errors.New("no map named")
	}
	var all []row
	for _, name := range names {
		rows, err := readMap(name)
		if err != nil {
			return nil, err
		}
		all = append(all, rows...)
	}
	if err := checkUnique(all); err != nil {
		return nil, err
	}
	if back {
		all = inverse(all)
	}
	return all, nil
}

func runApply(root string, args []string, out, errs *printer) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	back := fs.Bool("inverse", false, "apply the maps backwards")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rows, err := readMaps(fs.Args(), *back)
	if err != nil {
		return err
	}
	s, err := loadSnapshot(workTree{root})
	if err != nil {
		return err
	}
	res, err := applyRows(s, rows)
	if err != nil {
		return err
	}
	if err := writeResult(root, res); err != nil {
		return err
	}
	out.printf("apply: %d rows applied, %d already applied, %d files changed, %d removed\n",
		res.pending, res.done, len(res.changed), len(res.removed))
	for _, f := range res.findings {
		errs.printf("ambiguous: %s\n", f)
	}
	if len(res.findings) > 0 {
		return fmt.Errorf("%d mentions are ambiguous; resolve them by hand, then run apply again", len(res.findings))
	}
	return nil
}

func runVerify(root string, args []string, out, errs *printer) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	base := fs.String("base", "HEAD", "git revision the maps were applied to")
	back := fs.Bool("inverse", false, "verify the maps read backwards")
	golist := fs.Bool("golist", false, "also compare the listing with go test -list (runs TestMain of untagged files)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rows, err := readMaps(fs.Args(), *back)
	if err != nil {
		return err
	}
	before, err := loadSnapshot(gitTree{root, *base})
	if err != nil {
		return err
	}
	after, err := loadSnapshot(workTree{root})
	if err != nil {
		return err
	}
	v, err := verify(before, after, rows)
	if err != nil {
		return err
	}
	if *golist {
		v.checkGoList(root)
	}
	return v.report(out, errs)
}

func (v *verification) report(out, errs *printer) error {
	for _, f := range v.findings {
		errs.printf("%s\n", f)
	}
	for _, set := range tagSets {
		name := tagSetName(set)
		out.printf("%s: %d tests before, %d after\n", name, v.before[name], v.now[name])
	}
	out.printf("%d rows, %d go test -run commands checked\n", len(v.rows), v.runs)
	if len(v.findings) > 0 {
		return fmt.Errorf("verify: %d problems", len(v.findings))
	}
	out.println("verify: ok")
	return nil
}
