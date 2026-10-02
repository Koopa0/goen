package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

const seedBanner = "campaign-banner-01.webp"

func TestCampaignPageShowsItsHeaderOnlyWhenItHasOne(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	with := renderComponent(t, ctx, Campaign(layouts.Page{Title: "c"}, CampaignView{
		Slug: "c", Title: "Sale", EndsOn: "soon",
		Image: Photo{
			URL: assets.ProductImageURL(seedBanner), Srcset: assets.ProductImageSrcsetAt(seedBanner, 1600),
			Alt: "Products on sale",
		},
	}))
	for _, want := range []string{
		`class="goen-campaign__header"`, `src="` + assets.ProductImageURL(seedBanner) + `"`,
		`alt="Products on sale"`, "-400.webp", " 400w", ` width="1600"`,
	} {
		if !strings.Contains(with, want) {
			t.Errorf("a campaign with a header lacks %q", want)
		}
	}
	without := renderComponent(t, ctx, Campaign(layouts.Page{Title: "c"}, CampaignView{Slug: "c", Title: "Sale", EndsOn: "soon"}))
	if strings.Contains(without, "goen-campaign__header") {
		t.Error("a campaign with no header still draws one")
	}
}

func TestCampaignHeaderKeyResolvesLikeAProductImage(t *testing.T) {
	t.Parallel()
	if assets.ProductImageURL(seedBanner) == "" || !strings.Contains(assets.ProductImageSrcsetAt(seedBanner, 1600), "-800.webp") {
		t.Fatal("the embedded banner does not resolve through the product image resolver")
	}
	digest := strings.Repeat("ef", 32)
	if got := assets.ProductImageURL(digest); got != "/media/"+digest {
		t.Fatalf("an uploaded header resolves to %q", got)
	}
}
