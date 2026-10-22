package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

var (
	// addsUpAnOrder is the order total's own arithmetic, the discount taken
	// off and delivery added, in either order.
	addsUpAnOrder = regexp.MustCompile(
		`(?i)-\s*(?:\w+\.)?discount_cents\s*\+\s*(?:\w+\.)?shipping_cents\b` +
			`|\+\s*(?:\w+\.)?shipping_cents\s*-\s*(?:\w+\.)?discount_cents\b`)
	orderTotalsView = regexp.MustCompile(`(?is)\bCREATE\s+VIEW\s+order_totals\b[^;]*;`)
)

// TestEveryOrderTotalReadsTheOneView: every query that states an order's total
// reads the view order_totals, the one definition, except those listed with a
// reason.
//
// It was written out in 27 places, and the copies had already drifted: the
// health page's whole-NT$ amount divided a numeric sum, so it did not round
// where the invoice issuer it predicts does (#1933).
func TestEveryOrderTotalReadsTheOneView(t *testing.T) {
	t.Parallel()

	sources := queryFiles(t)
	migrations, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations/*.up.sql found; the view has nowhere to be defined")
	}
	for _, path := range migrations {
		src, readErr := os.ReadFile(path) //nolint:gosec // G304: paths come from the migration glob
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		sources[path] = string(src)
	}
	paths := make([]string, 0, len(sources))
	for path := range sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	defined := 0
	for _, path := range paths {
		src := maskCalendarSQL(sources[path], true)
		view := orderTotalsView.FindStringIndex(src)
		for _, at := range addsUpAnOrder.FindAllStringIndex(src, -1) {
			if len(view) == 2 && at[0] >= view[0] && at[1] <= view[1] {
				defined++
				continue
			}
			t.Errorf("%s:%s adds up an order total itself.\n"+
				"  Read order_totals.total_cents (or subtotal_cents) instead, so a "+
				"change to what an order costs, or to how it rounds, is made once.",
				path, migrationLine(src, at[0]))
		}
	}
	if defined != 1 {
		t.Errorf("found the total's arithmetic %d times inside CREATE VIEW order_totals, "+
			"want 1: the view is gone or no longer computes it, so this test guards nothing", defined)
	}
}
