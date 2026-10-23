package campaigns

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestCampaignFormProductOffers: campaign products show the offer in both lists and languages.
func TestCampaignFormProductOffers(t *testing.T) {
	t.Parallel()
	products := []CampaignProduct{
		{Slug: "on-sale", Name: "Discounted product", Image: picture.Image{Digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, HasVariant: true, HasDiscountedVariant: true, PriceCents: 99000, CompareCents: 129000, SellableUnits: 5},
		{Slug: "sold-out", Name: "Unavailable product", HasVariant: true, HasDiscountedVariant: true, PriceCents: 199000, CompareCents: 299000},
		{Slug: "plain-selected", Name: "Available regular variant with a sold-out discount", HasVariant: true, HasDiscountedVariant: true, PriceCents: 70000, SellableUnits: 3},
		{Slug: "no-original", Name: "Draft without original price", HasVariant: true, Draft: true, PriceCents: 50000, SellableUnits: 1},
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		view := CampaignView{Slug: "product-offers", Term: "product", Products: products[:2], Matches: products}
		body := renderComponent(t, ctx, CampaignForm(frame.Page{Title: "Campaign"}, view))
		for _, want := range []string{"NT$1,290", "NT$990", "23%", i18n.T(ctx, i18n.KeySoldOut), "/media/" + products[0].Image.Digest} {
			if want == "" || strings.Count(body, want) < 2 {
				t.Errorf("%s campaign HTML count of %q = %d, want at least 2", locale, want, strings.Count(body, want))
			}
		}
		if strings.Count(body, `width="40" height="40"`) != 2 {
			t.Errorf("%s campaign HTML does not size both product photos at 40px", locale)
		}
		for _, slug := range []string{"on-sale", "sold-out"} {
			if strings.Count(body, `name="product" value="`+slug+`"`) != 2 {
				t.Errorf("%s campaign HTML lost %s's add or remove form", locale, slug)
			}
		}
		if strings.Count(body, `name="product" value="no-original"`) != 0 {
			t.Errorf("%s campaign HTML offers adding a product without an original price", locale)
		}
		priceLine := "原價 NT$1,290 → NT$990（−23%）"
		if locale == i18n.En {
			priceLine = "Was NT$1,290 → NT$990 (−23%)"
		}
		if strings.Count(body, priceLine) != 2 {
			t.Errorf("%s campaign HTML lacks exact price line %q twice", locale, priceLine)
		}
		if strings.Count(body, `href="/admin/products/no-original"`) != 1 {
			t.Errorf("%s search does not link the unfeatureable product to its editor", locale)
		}
		if strings.Count(body, `name="product" value="plain-selected"`) != 1 {
			t.Errorf("%s search loses a product with a discounted alternative", locale)
		}
		feedback := []string{"沒有原價", "未上架", "可售 5 件"}
		if locale == i18n.En {
			feedback = []string{"No original price", "Not published", "5 sellable units"}
		}
		for _, want := range feedback {
			expectedCount := 1
			if want == "可售 5 件" || want == "5 sellable units" {
				expectedCount = 2
			}
			if strings.Count(body, want) != expectedCount {
				t.Errorf("%s campaign HTML count of %q = %d, want %d", locale, want, strings.Count(body, want), expectedCount)
			}
		}
	}
}
