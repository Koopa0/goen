package campaign_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/tone"
	"github.com/koopa0/goen/internal/ui/widget"
)

const seedBanner = "campaign-banner-01.webp"

// TestPageHeader: a campaign page shows its header only when it has one.
func TestPageHeader(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	with := renderComponent(t, ctx, campaign.Page(frame.Page{Title: "c"}, campaign.View{
		Slug: "c", Title: "Sale",
		Image: widget.Photo{
			URL: picture.Image{BundledKey: seedBanner}.URL(), Srcset: picture.Image{BundledKey: seedBanner}.Srcset(),
			Alt: "Products on sale",
		},
	}))
	for _, want := range []string{
		`class="goen-campaign__header"`, `src="` + picture.Image{BundledKey: seedBanner}.URL() + `"`,
		`alt="Products on sale"`, "-400.webp", " 400w", ` width="1600"`,
	} {
		if !strings.Contains(with, want) {
			t.Errorf("a campaign with a header lacks %q", want)
		}
	}
	without := renderComponent(t, ctx, campaign.Page(frame.Page{Title: "c"}, campaign.View{Slug: "c", Title: "Sale"}))
	if strings.Contains(without, "goen-campaign__header") {
		t.Error("a campaign with no header still draws one")
	}
}

// TestPageHeadTone: a campaign head carries its tone. The attribute is what
// app.css selects the ground on; a head without it is unthemed. The department
// head's half of this test is catalog's TestListingHeadTone.
func TestPageHeadTone(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)

	head := renderComponent(t, ctx, campaign.Page(frame.Page{Title: "c"}, campaign.View{Slug: "c", Title: "Sale", Tone: tone.Ink}))
	if !strings.Contains(head, `data-tone="ink"`) {
		t.Error("the campaign head does not carry its tone")
	}
}
