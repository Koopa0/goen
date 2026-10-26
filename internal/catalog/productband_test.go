package catalog

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
)

func bandProduct(photos int) *ProductView {
	v := &ProductView{
		Name: "Restwood Mug", Brand: "Restwood", Slug: "restwood-mug", Description: "A stoneware mug.",
		SelectionOK: true, Exact: true, Sellable: true, AnySellable: true, PriceCents: 43200,
		Specs: []ProductSpec{{Label: "Capacity", Value: "350 ml"}},
	}
	for i := range photos {
		v.Images = append(v.Images, ProductImage{
			URL: "/media/p/" + string(rune('a'+i)) + ".webp", Alt: "shot " + string(rune('a'+i)), Width: 1600, Height: 1200,
		})
	}
	return v
}

// TestProductGalleryUntoned holds that the gallery wears no department tone.
func TestProductGalleryUntoned(t *testing.T) {
	t.Parallel()
	page := renderProduct(t, bandProduct(3), i18n.En)
	gallery := between(t, page, `class="goen-pdp__gallery"`, `id="buybox"`)
	if strings.Contains(gallery, "data-tone") {
		t.Errorf("the gallery carries a tone: %.120s", gallery)
	}
	if strings.Contains(gallery, "style=") {
		t.Error("the gallery draws a style attribute")
	}
}

// TestProductDescriptionPlain holds that the description is a plain section
// ahead of the specifications, whatever the number of photographs.
func TestProductDescriptionPlain(t *testing.T) {
	t.Parallel()
	for _, photos := range []int{1, 3} {
		page := renderProduct(t, bandProduct(photos), i18n.En)
		const section = `<section class="goen-pdp__desc" aria-labelledby="desc-heading">`
		more := strings.Index(page, `<div class="goen-pdp__more">`)
		start := strings.Index(page, section)
		specs := strings.Index(page, `id="specs-heading"`)
		if more < 0 || start < more || start > specs {
			t.Errorf("%d photographs: the description is not between the start of the details and the specifications", photos)
		}
		desc := between(t, page, section, `</section>`)
		if !strings.Contains(desc, "A stoneware mug.") {
			t.Errorf("%d photographs: the section does not hold the description", photos)
		}
		for _, unwanted := range []string{"<img", "data-tone", "goen-pdp__brand"} {
			if strings.Contains(desc, unwanted) {
				t.Errorf("%d photographs: the description section holds %q", photos, unwanted)
			}
		}
		if strings.Contains(page, "goen-band") {
			t.Errorf("%d photographs: the page draws a band", photos)
		}
	}
}

// TestProductDescriptionNone holds that a product with no description draws no
// section for it.
func TestProductDescriptionNone(t *testing.T) {
	t.Parallel()
	v := bandProduct(3)
	v.Description = ""
	if page := renderProduct(t, v, i18n.En); strings.Contains(page, "desc-heading") {
		t.Error("a product with no description draws its section")
	}
}

// TestProductReviewDate holds that a review date reads as the shop date and
// keeps its ISO form.
func TestProductReviewDate(t *testing.T) {
	t.Parallel()
	v := bandProduct(1)
	v.Rating, v.RatingCount = 4, 1
	v.Reviews = []ProductReview{{
		Rating: 4, Author: "Mina",
		Date: shoptime.Date{Year: 2025, Month: time.October, Day: 30, OtherYear: true},
	}}
	cases := []struct {
		locale i18n.Locale
		text   string
	}{
		{i18n.ZhHant, "2025\u00a0年 10\u00a0月 30\u00a0日"},
		{i18n.En, "Oct\u00a030, 2025"},
	}
	for _, c := range cases {
		page := renderProduct(t, v, c.locale)
		want := `<time class="goen-pdp__reviewdate" datetime="2025-10-30">` + c.text + `</time>`
		if !strings.Contains(page, want) {
			t.Errorf("locale %s: the review date is not %q", c.locale, want)
		}
	}
}

// TestProductReviewsEmptySentence holds that the empty reviews sentence has no
// pause.
func TestProductReviewsEmptySentence(t *testing.T) {
	t.Parallel()
	for locale, want := range map[i18n.Locale]string{
		i18n.ZhHant: "還沒有評價。買過這個商品、訂單送達後，就可以在這裡評價。",
		i18n.En:     "No reviews yet. Once an order with this product is delivered, its buyer can review it here.",
	} {
		if page := renderProduct(t, bandProduct(1), locale); !strings.Contains(page, ">"+want+"<") {
			t.Errorf("locale %s: the empty reviews sentence is not %q", locale, want)
		}
	}
}

func between(t *testing.T, s, from, to string) string {
	t.Helper()
	start := strings.Index(s, from)
	if start < 0 {
		t.Fatalf("the page has no %q", from)
	}
	rest := s[start:]
	end := strings.Index(rest, to)
	if end < 0 {
		t.Fatalf("no %q after %q", to, from)
	}
	return rest[:end]
}
