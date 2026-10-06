package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// The attribute is what app.css selects the ground on; a head without it is
// unthemed, and one carrying a tone outside the set would match no rule.
func TestADepartmentAndCampaignHeadCarryTheirTone(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)

	listing := renderComponent(t, ctx, Listing(layouts.Page{Title: "c"}, ListingView{Slug: "c", Name: "Books", Theme: &Theme{Tone: ToneSage}}, nil))
	if !strings.Contains(listing, `class="goen-pagehead" data-tone="sage"`) {
		t.Errorf("the category head does not carry its tone:\n%s", listing)
	}
	unset := renderComponent(t, ctx, Listing(layouts.Page{Title: "c"}, ListingView{Slug: "c", Name: "Books"}, nil))
	if !strings.Contains(unset, `data-tone="stone"`) {
		t.Error("a category head with no tone is not stone")
	}
	campaign := renderComponent(t, ctx, Campaign(layouts.Page{Title: "c"}, CampaignView{Slug: "c", Title: "Sale", Tone: ToneInk}))
	if !strings.Contains(campaign, `data-tone="ink"`) {
		t.Error("the campaign head does not carry its tone")
	}
}
