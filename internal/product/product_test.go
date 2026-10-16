package product

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// statusCheck is the list products_status_known admits.
var statusCheck = regexp.MustCompile(`CONSTRAINT products_status_known CHECK \(status IN \(([^)]*)\)\)`)

func TestStatusesAreTheSchemasStatuses(t *testing.T) {
	t.Parallel()
	schema, err := os.ReadFile(filepath.Join("..", "..", "migrations", "001_initial_schema.up.sql"))
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	check := statusCheck.FindSubmatch(schema)
	if check == nil {
		t.Fatal("products_status_known not found; the pattern no longer matches the schema")
	}
	admitted := regexp.MustCompile(`'([a-z_]+)'`).FindAllSubmatch(check[1], -1)
	want := make([]string, 0, len(admitted))
	for _, m := range admitted {
		want = append(want, string(m[1]))
	}
	got := make([]string, 0, len(Statuses))
	for _, s := range Statuses {
		got = append(got, string(s))
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("Statuses = %v, the schema admits %v", got, want)
	}
}
