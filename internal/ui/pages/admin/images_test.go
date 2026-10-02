package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// A seeded image's key is a file name, not a digest, so /media/<key> is a 404:
// the back office must resolve it from the embedded assets, as the storefront
// does, and offer the 400px rendition to its small tiles.
func TestAdminProductImagesResolveSeedAndUploadedThumbnails(t *testing.T) {
	t.Parallel()
	const seedKey = "pixelight-9-pro-01.webp"
	digest := strings.Repeat("ab", 32)
	v := ProductView{
		Slug:    "p",
		Images:  []Image{{Key: seedKey, Alt: "seed", Width: 1600, Height: 1200}},
		Library: []Image{{Key: digest, Width: 1600, Height: 1200}},
	}
	var b strings.Builder
	if err := productImages(v).Render(i18n.WithLocale(t.Context(), i18n.En), &b); err != nil {
		t.Fatal(err)
	}
	page := b.String()

	if strings.Contains(page, `src="/media/`+seedKey) {
		t.Errorf("the seed image is still built as /media/%s, which is a 404", seedKey)
	}
	seedSrc := assets.ProductImageURL(seedKey)
	small := strings.Split(strings.Split(assets.ProductImageSrcset(seedKey), " 400w")[0], ", ")[0]
	if !strings.HasPrefix(seedSrc, "/static/") || !strings.Contains(small, "-400") {
		t.Fatalf("test premise: seed URL %q, 400 rendition %q", seedSrc, small)
	}
	for _, want := range []string{`src="` + seedSrc + `"`, small + ` 400w`, `sizes="120px"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the seed image in the product view lacks %q", want)
		}
	}
	for _, want := range []string{
		`src="/media/` + digest + `"`,
		`/media/` + digest + `/400 400w`,
		`sizes="80px"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the uploaded image in the library lacks %q", want)
		}
	}
}

// The hero list's 160px tiles get the 400px rendition, not the original upload.
func TestAdminHeroTilesOfferTheSmallRendition(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("cd", 32)
	v := &HeroView{Rows: []HeroSlide{{ID: "s", Headline: "h", ImageKey: digest, Active: true}}}
	var b strings.Builder
	if err := Home(layouts.Page{Title: "Home"}, v).Render(i18n.WithLocale(t.Context(), i18n.En), &b); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`src="/media/` + digest + `"`, `/media/` + digest + `/400 400w`, `sizes="160px"`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the hero tile lacks %q", want)
		}
	}
}
