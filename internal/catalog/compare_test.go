package catalog

import (
	"testing"

	"github.com/koopa0/goen/internal/ui/pages"
)

func TestNormaliseSlugsBoundsAndReportsWhatItDropped(t *testing.T) {
	t.Parallel()
	got, dropped := normaliseSlugs([]string{"a", "", "a", "b", "c", "d"})
	if len(got) != 4 || dropped {
		t.Errorf("four distinct slugs: got %v dropped=%v", got, dropped)
	}
	got, dropped = normaliseSlugs([]string{"a", "b", "c", "d", "e"})
	if len(got) != pages.MaxCompare || !dropped {
		t.Errorf("five slugs: got %v dropped=%v, want %d and true", got, dropped, pages.MaxCompare)
	}
}
