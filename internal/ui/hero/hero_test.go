package hero_test

import (
	"testing"

	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestAllPhotographed holds that the carousel stands its tabs at the
// photographs' foot only when every slide has a photograph: one slide without
// one is enough to take that height away.
func TestAllPhotographed(t *testing.T) {
	t.Parallel()
	photographed := hero.Slide{Photo: widget.Photo{URL: "/img/a.webp"}}
	bare := hero.Slide{}
	for name, tc := range map[string]struct {
		slides []hero.Slide
		want   bool
	}{
		"no slides":          {nil, true},
		"all photographed":   {[]hero.Slide{photographed, photographed}, true},
		"the last has none":  {[]hero.Slide{photographed, bare}, false},
		"the first has none": {[]hero.Slide{bare, photographed}, false},
		"none":               {[]hero.Slide{bare}, false},
	} {
		if got := hero.AllPhotographed(tc.slides); got != tc.want {
			t.Errorf("%s: AllPhotographed = %v, want %v", name, got, tc.want)
		}
	}
}
