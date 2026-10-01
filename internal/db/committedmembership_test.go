package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var committedSubPlan = regexp.MustCompile(`(?is)\bIN\s*\(\s*SELECT\s+(?:[a-z_]+\.)?id\s+FROM\s+committed_orders\b`)

// TestNoQueryTestsOneOrderAgainstTheWholeCommittedView holds the membership
// test of a single order to a correlated EXISTS. IN (SELECT ... FROM
// committed_orders) is planned as a hashed SubPlan over every order and every
// captured payment, on pages that ask about one.
func TestNoQueryTestsOneOrderAgainstTheWholeCommittedView(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	for _, path := range calendarSQLFiles(t, root) {
		src, err := os.ReadFile(path) //nolint:gosec // paths come from sqlc.yaml or the migration glob
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if committedSubPlan.MatchString(maskCalendarSQL(string(src), true)) {
			t.Errorf("%s tests membership with IN (SELECT id FROM committed_orders); "+
				"use EXISTS (SELECT 1 FROM committed_orders c WHERE c.id = <order>.id)", path)
		}
	}
}
