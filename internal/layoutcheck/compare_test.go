package layoutcheck_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestCompareLayoutRowsMeasureTheTableNotTheEmptyState holds the /compare
// fixture. Enough() needs two columns; one p= is the too-few empty state, and
// .goen-compare wraps that state too. A row that names one slug or marks the
// wrapper never reaches the table.
func TestCompareLayoutRowsMeasureTheTableNotTheEmptyState(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	body := readLayoutScript(t, root)
	makefile := readMakefile(t, root)

	for _, label := range []string{"compare 375", "compare 1440"} {
		row := compareLayoutRow(t, body, label)
		if strings.Count(row, "p=") < 2 {
			t.Errorf("%s names fewer than two products:\n%s", label, row)
		}
		if !strings.Contains(row, "p=PRODUCT_SLUG") || !strings.Contains(row, "p=COMPARE_SLUG_B") {
			t.Errorf("%s does not name two seed slugs:\n%s", label, row)
		}
		if !strings.Contains(row, `marker: '.goen-compare__table'`) {
			t.Errorf("%s does not mark the table:\n%s", label, row)
		}
		if !strings.Contains(row, "table: true") {
			t.Errorf("%s does not require the table geometry:\n%s", label, row)
		}
	}

	if !strings.Contains(makefile, "COMPARE_SLUG_B=") {
		t.Fatal("check-layout does not supply a second compare product")
	}
	if !strings.Contains(body, "COMPARE_SLUG_B") {
		t.Fatal("check-layout never substitutes COMPARE_SLUG_B")
	}
}

// TestCompareLayoutProbeReadsColumnsStickyAndInnerScroll holds the measurements
// that only exist on the table: two product columns, a sticky first cell, and
// (at 375) overflow inside the scroll box. A marker check cannot see any of them.
func TestCompareLayoutProbeReadsColumnsStickyAndInnerScroll(t *testing.T) {
	t.Parallel()

	body := readLayoutScript(t, repoRoot(t))
	for _, needle := range []string{
		"thead .goen-compare__head",
		"goen-compare__table tbody th",
		"sticky.position === 'sticky'",
		"scroller.scrollWidth > scroller.clientWidth",
		"want.width === 375 && !got.tableScrolls",
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("the compare probe never reads %q", needle)
		}
	}
}

func readLayoutScript(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "scripts", "check-layout.mjs")
	//nolint:gosec // G304: the repository root joined to a fixed name
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read check-layout.mjs: %v", err)
	}
	return string(raw)
}

func compareLayoutRow(t *testing.T, body, label string) string {
	t.Helper()
	re := regexp.MustCompile(`\{ label: '` + regexp.QuoteMeta(label) + `',[^}]+\}`)
	row := re.FindString(body)
	if row == "" {
		t.Fatalf("no layout row labelled %q", label)
	}
	return row
}
