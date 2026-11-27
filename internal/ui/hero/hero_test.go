package hero_test

import (
	"testing"

	"github.com/koopa0/goen/internal/ui/hero"
)

// TestCTA_Shown holds that a slide leads somewhere only with both a label and
// a link: a link the store refused leaves the label alone, and the tile is
// then drawn without one.
func TestCTA_Shown(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		cta  hero.CTA
		want bool
	}{
		"both":     {hero.CTA{Label: "看活動", Href: "/s/autumn"}, true},
		"no link":  {hero.CTA{Label: "看活動"}, false},
		"no label": {hero.CTA{Href: "/s/autumn"}, false},
		"neither":  {hero.CTA{}, false},
	} {
		if got := tc.cta.Shown(); got != tc.want {
			t.Errorf("%s: %+v.Shown() = %v, want %v", name, tc.cta, got, tc.want)
		}
	}
}
