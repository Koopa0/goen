package catalog

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/tone"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestBandPhoto holds that the band takes the department's photograph, else the
// first product's photograph on a well, else none.
func TestBandPhoto(t *testing.T) {
	t.Parallel()

	own := widget.Photo{URL: "/dept.webp"}
	first := tile.Tile{ImageURL: "/first.webp", ImageSrcset: "/first-400.webp 400w", ImageAlt: "first"}
	second := tile.Tile{ImageURL: "/second.webp"}

	for name, tt := range map[string]struct {
		department widget.Photo
		products   []tile.Tile
		wantURL    string
		wantWell   bool
	}{
		"department photograph":   {own, []tile.Tile{first}, "/dept.webp", false},
		"first product on a well": {widget.Photo{}, []tile.Tile{first, second}, "/first.webp", true},
		"first product has none":  {widget.Photo{}, []tile.Tile{{}, second}, "", false},
		"no products":             {widget.Photo{}, nil, "", false},
	} {
		got, onWell := bandPhoto(tt.department, tt.products)
		if got.URL != tt.wantURL || onWell != tt.wantWell {
			t.Errorf("%s: bandPhoto = (%q, %v), want (%q, %v)", name, got.URL, onWell, tt.wantURL, tt.wantWell)
		}
	}
}

// TestBandNameLong holds that a department name steps the band's heading down
// once it is past its limit.
func TestBandNameLong(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]bool{
		"3C 數位":                 false,
		"居家生活與廚房":               true,
		"六個字的名字":                false,
		"Tech":                  false,
		"Home and living goods": true,
		"Eighteen letters!!":    false,
	} {
		if got := bandNameLong(name); got != want {
			t.Errorf("bandNameLong(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestListingHeadPhoto holds that a department head draws no image when it has
// neither a department photograph nor a product, and takes its first product's
// photograph on the well when it has one.
func TestListingHeadPhoto(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)

	page := renderComponent(t, ctx, Listing(frame.Page{}, ListingView{Slug: "c", Name: "Books", Theme: &Theme{Tone: tone.Blush}}, nil, nil))
	if strings.Contains(page, "goen-pagehead__photo") {
		t.Error("a head with neither a department nor a product photograph drew an image")
	}
	withProduct := renderComponent(t, ctx, Listing(frame.Page{}, ListingView{Slug: "c", Name: "Books", Products: []tile.Tile{{Slug: "p", Name: "P", ImageURL: "/p.webp"}}}, nil, nil))
	if !strings.Contains(withProduct, `class="goen-band__media goen-band__media--well"`) {
		t.Error("a head with no department photograph did not take its first product's, on the well")
	}
}

// TestListingHeadBand holds that the department head is the band: band, grid,
// media with the photograph first, then body, in that order.
func TestListingHeadBand(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)

	page := renderComponent(t, ctx, Listing(frame.Page{}, ListingView{Slug: "c", Name: "Books", Theme: &Theme{Tone: tone.Sage, Photo: widget.Photo{URL: "/d.webp"}}}, nil, nil))
	band := strings.Index(page, `class="goen-band"`)
	grid := strings.Index(page, `class="goen-band__grid"`)
	media := strings.Index(page, `class="goen-band__media"`)
	body := strings.Index(page, `class="goen-band__body goen-pagehead__text"`)
	if band < 0 || band >= grid || grid >= media || media >= body {
		t.Errorf("the head is not band, grid, media (the photograph first), body in that order:\n%s", page)
	}
	if want := `sizes="(min-width: 48rem) 57vw, 100vw"`; !strings.Contains(page, want) {
		t.Errorf("the head photograph does not carry %s:\n%s", want, page)
	}
	if strings.Contains(page, `fetchpriority="low"`) {
		t.Errorf("the head photograph is the first screen's largest image and must not be fetched at low priority:\n%s", page)
	}
}
