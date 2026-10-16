package products

import (
	"slices"
	"testing"

	"github.com/koopa0/goen/internal/product"
)

// TestTheStatusTableIsTheProductClosedSet holds the desk to the closed set:
// knownStatus admits and statusLabel words exactly the statuses in the table,
// so a status product.Statuses gains and the table lacks would be refused by
// the write and panic on the page.
func TestTheStatusTableIsTheProductClosedSet(t *testing.T) {
	t.Parallel()
	got := make([]product.Status, 0, len(statuses))
	for _, s := range statuses {
		got = append(got, s.value)
	}
	want := slices.Clone(product.Statuses[:])
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the status table has %v, product.Statuses has %v", got, want)
	}
}
