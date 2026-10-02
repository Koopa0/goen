package shoptime_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// anyFormat matches a time rendered with .Format( whatever the layout is: a
// literal, time.DateOnly, a constant or a variable. A value takes whatever
// zone it happens to carry.
var anyFormat = regexp.MustCompile(`\.Format\(`)

// providerUTC matches the one legitimate shape: a value explicitly moved to UTC
// before formatting, because it belongs to somebody else's protocol.
var providerUTC = regexp.MustCompile(`\.UTC\(\)\.Format\(`)

// ambientFormat reports a .Format( on this line that is not preceded by .UTC().
func ambientFormat(line string) bool {
	return len(anyFormat.FindAllStringIndex(line, -1)) >
		len(providerUTC.FindAllStringIndex(line, -1))
}

func TestTheZoneRuleSeesWhatTheOldPatternMissed(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]bool{
		`s := t.Format("2006-01-02 15:04")`:    true,
		`s := t.Format(time.DateOnly)`:         true,
		`s := t.Format(layout)`:                true,
		`s := t.Format("15:04")`:               true,
		`{ o.PlacedAt.Format("2006-01-02") }`:  true,
		`s := t.UTC().Format("2006-01-02")`:    false,
		`s := t.UTC().Format(time.DateOnly)`:   false,
		`s := a.UTC().Format(x) + b.Format(x)`: true,
		`s := strings.Title("no format call")`: false,
	} {
		if got := ambientFormat(line); got != want {
			t.Errorf("ambientFormat(%q) = %v, want %v", line, got, want)
		}
	}
}

// TestNoTimeIsRenderedInAnUnstatedZone derives its corpus from the tree.
//
// pgx hands back a timestamptz in the process's own zone, so `t.Format(...)`
// renders in whatever TZ the host sets — Asia/Taipei on a developer's machine
// and UTC in cgr.dev/chainguard/static, which sets none. Measured on one row:
// "2026-09-03 13:09" locally against "2026-09-03 05:09" deployed. Every order
// time, message, audit row and delivery date was eight hours early in
// production and correct on the machine of anyone who could have noticed.
//
// The rule is every .Format( call, in .go and .templ sources alike, so a layout
// held in a constant or a template expression cannot step round it.
//
// This is the Go half of the rule SQL already follows, where ambient
// current_date is forbidden and shop_day is the one definition.
//
// The exception is a calendar date with no time in it, which a provider protocol
// carries as a bare date parsed as midnight UTC. Those are written with an
// explicit .UTC() and are recognised by shape rather than by an allowlist of
// lines, so the exemption cannot go stale against a file that moved.
func TestNoTimeIsRenderedInAnUnstatedZone(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	var offenders []string

	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// A .templ source is read, and the _templ.go it generates is not:
			// the .templ is what somebody edits.
			if d.IsDir() || (!strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".templ")) ||
				strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_templ.go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			// This package is where the zone is stated, so it is where the
			// layouts live.
			if strings.HasPrefix(rel, filepath.Join("internal", "shoptime")) {
				return nil
			}
			//nolint:gosec // G304: path comes from WalkDir over this repository
			src, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for i, line := range strings.Split(string(src), "\n") {
				if !ambientFormat(line) {
					continue
				}
				offenders = append(offenders,
					rel+":"+strconv.Itoa(i+1)+"  "+strings.TrimSpace(line))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("%d timestamp(s) are rendered in the process's own zone:\n  %s\n\n"+
			"Use shoptime.Day/Minute/Second, which render on the shop's clock the "+
			"way shop_day does in SQL. A value that belongs to a provider's "+
			"protocol keeps its explicit .UTC().",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}
