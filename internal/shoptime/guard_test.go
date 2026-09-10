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

// ambientFormat matches a date or time rendered straight off a time.Time, which
// takes whatever zone the value happens to carry.
var ambientFormat = regexp.MustCompile(`\.Format\("2006-01-02[^"]*"\)`)

// providerUTC matches the one legitimate shape: a value explicitly moved to UTC
// before formatting, because it belongs to somebody else's protocol.
var providerUTC = regexp.MustCompile(`\.UTC\(\)\.Format\("2006-01-02[^"]*"\)`)

// TestNoTimeIsRenderedInAnUnstatedZone derives its corpus from the tree.
//
// pgx hands back a timestamptz in the process's own zone, so `t.Format(...)`
// renders in whatever TZ the host sets — Asia/Taipei on a developer's machine
// and UTC in cgr.dev/chainguard/static, which sets none. Measured on one row:
// "2026-09-03 13:09" locally against "2026-09-03 05:09" deployed. Every order
// time, message, audit row and delivery date was eight hours early in
// production and correct on the machine of anyone who could have noticed.
//
// This is the Go half of the rule CLAUDE.md already states for SQL, where
// ambient current_date is forbidden and shop_day is the one definition.
//
// The exception is a timestamp that is not the shop's to interpret: ECPay's
// invoice dates are a wall clock labelled UTC, and moving one to Taipei is a
// filing the provider rejects with 1600003. Those are written with an explicit
// .UTC() and are recognised by shape rather than by an allowlist of lines, so
// the exemption cannot go stale against a file that moved.
func TestNoTimeIsRenderedInAnUnstatedZone(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	var offenders []string

	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") ||
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
				if !ambientFormat.MatchString(line) || providerUTC.MatchString(line) {
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
