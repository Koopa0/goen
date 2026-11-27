package catalog

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestListingNotCalledLimitedTime holds that nothing offered is called limited
// time: not the deals link, not the shelf's switch for what is on sale, not the
// chip it leaves when it is on.
//
// The deals page, the campaign page and the campaigns strip are campaign's
// TestDealsNotCalledLimitedTime.
func TestListingNotCalledLimitedTime(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{Slug: "tech", Name: "3C 數位", Shelf: Shelf{Path: "/c/tech", Filters: Filters{OnSaleOnly: true}}}
	for name, text := range map[string]string{
		"nav":   i18n.T(ctx, i18n.KeyDeals),
		"shelf": renderComponent(t, ctx, Listing(frame.Page{}, &view)),
	} {
		if strings.Contains(text, "限時") {
			t.Errorf("%s says 限時", name)
		}
	}
}
