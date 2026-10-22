package campaign_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
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
			URL: assets.Image{BundledKey: seedBanner}.URL(), Srcset: assets.Image{BundledKey: seedBanner}.Srcset(),
			Alt: "Products on sale",
		},
	}))
	for _, want := range []string{
		`class="goen-campaign__header"`, `src="` + assets.Image{BundledKey: seedBanner}.URL() + `"`,
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

// TestImage_URLHeaderKey: a campaign header key resolves like a product image:
// a bundled banner resolves through the image resolver, and an uploaded header
// to its media path.
func TestImage_URLHeaderKey(t *testing.T) {
	t.Parallel()
	if banner := (assets.Image{BundledKey: seedBanner}); banner.URL() == "" || !strings.Contains(banner.Srcset(), "-800.webp") {
		t.Fatal("the embedded banner does not resolve through the product image resolver")
	}
	digest := strings.Repeat("ef", 32)
	if got := (assets.Image{Digest: digest}).URL(); got != "/media/"+digest {
		t.Fatalf("an uploaded header resolves to %q", got)
	}
}

// TestPageHeadTone: a campaign head carries its tone. The attribute is what
// app.css selects the ground on; a head without it is unthemed. The department
// head's half of this test is catalog's TestADepartmentHeadCarriesItsTone.
func TestPageHeadTone(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)

	head := renderComponent(t, ctx, campaign.Page(frame.Page{Title: "c"}, campaign.View{Slug: "c", Title: "Sale", Tone: tone.Ink}))
	if !strings.Contains(head, `data-tone="ink"`) {
		t.Error("the campaign head does not carry its tone")
	}
}
