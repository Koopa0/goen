package products

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
)

// TestProductImagesThumbnails: the admin product images resolve seed and
// uploaded thumbnails.
//
// A seeded image's key is a file name, not a digest, so /media/<key> is a 404:
// the back office must resolve it from the embedded assets, as the storefront
// does, and offer the 400px rendition to its small tiles.
func TestProductImagesThumbnails(t *testing.T) {
	t.Parallel()
	const seedKey = "pixelight-9-pro-01.webp"
	digest := strings.Repeat("ab", 32)
	v := ProductView{
		Slug:    "p",
		Images:  []Image{{Photo: picture.Image{BundledKey: seedKey, Width: 1600}, Alt: "seed"}},
		Library: []Image{{Photo: picture.Image{Digest: digest, Width: 1600}}},
	}
	var b strings.Builder
	if err := productImages(v).Render(i18n.WithLocale(t.Context(), i18n.En), &b); err != nil {
		t.Fatal(err)
	}
	page := b.String()

	if strings.Contains(page, `src="/media/`+seedKey) {
		t.Errorf("the seed image is still built as /media/%s, which is a 404", seedKey)
	}
	seedSrc := picture.Image{BundledKey: seedKey}.URL()
	small := strings.Split(strings.Split(picture.Image{BundledKey: seedKey}.Srcset(), " 400w")[0], ", ")[0]
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
