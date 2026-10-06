package pages

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
		SelectionOK: true, Exact: true, Sellable: true, AnySellable: true, PriceCents: 43200, Tone: ToneMist,
	}
	for i := range photos {
		v.Images = append(v.Images, ProductImage{
			URL: "/media/p/" + string(rune('a'+i)) + ".webp", Alt: "shot " + string(rune('a'+i)), Width: 1600, Height: 1200,
		})
	}
	return v
}

func TestTheGalleryWearsTheDepartmentTone(t *testing.T) {
	t.Parallel()
	page := renderProduct(t, bandProduct(3), i18n.En)
	gallery := between(t, page, `class="goen-pdp__gallery"`, `id="buybox"`)
	if !strings.Contains(gallery, `data-tone="mist"`) {
		t.Errorf("the gallery does not carry the department's tone: %.120s", gallery)
	}
	if strings.Contains(gallery, "style=") {
		t.Error("the gallery draws a style attribute")
	}
}

func TestTheBandShowsTheSecondPhotographAndTheDescription(t *testing.T) {
	t.Parallel()
	page := renderProduct(t, bandProduct(3), i18n.En)
	band := between(t, page, `<section class="goen-band goen-band--product"`, `</section>`)
	for _, want := range []string{`data-tone="mist"`, `src="/media/p/b.webp"`, `loading="lazy"`, `width="800"`, `id="desc-heading"`, "A stoneware mug."} {
		if !strings.Contains(band, want) {
			t.Errorf("the band does not contain %q", want)
		}
	}
	if strings.Contains(band, "/media/p/a.webp") || strings.Contains(band, "/media/p/c.webp") {
		t.Error("the band shows a photograph other than the second")
	}
	if buy := strings.Index(page, `id="buybox"`); strings.Index(page, `goen-band--product`) < buy {
		t.Error("the band is above the buy box")
	}
}

func TestTheBandDrawsNoPhotographWithoutASecondOne(t *testing.T) {
	t.Parallel()
	page := renderProduct(t, bandProduct(1), i18n.En)
	band := between(t, page, `<section class="goen-band goen-band--product`, `</section>`)
	if strings.Contains(band, "<img") || strings.Contains(band, "goen-band__media") {
		t.Error("a band with no second photograph draws a media column")
	}
	if !strings.Contains(band, "goen-band--text") {
		t.Error("a band with no photograph does not take its text-only form")
	}
}

func TestTheBandNeedsADescription(t *testing.T) {
	t.Parallel()
	v := bandProduct(3)
	v.Description = ""
	if page := renderProduct(t, v, i18n.En); strings.Contains(page, "goen-band") {
		t.Error("a product with no description draws a band")
	}
}

func TestAReviewDateReadsAsTheShopDateAndKeepsItsISOForm(t *testing.T) {
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

func TestTheEmptyReviewsSentenceHasNoPause(t *testing.T) {
	t.Parallel()
	for locale, want := range map[i18n.Locale]string{
		i18n.ZhHant: "還沒有人評價這個商品，你可以是第一個。",
		i18n.En:     "Nobody has reviewed this yet. You could be the first.",
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
