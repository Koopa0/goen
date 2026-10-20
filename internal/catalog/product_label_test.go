package catalog

import (
	"fmt"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/product/labelling"
)

func TestProductLabelRendersWithoutOtherDetails(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, set := range []bool{false, true} {
			view := ProductView{Slug: "label", Name: "Label fixture"}
			if set {
				age := int16(0)
				view.LabelFacts = &labelling.Facts{Origin: "<script>origin</script>", NetQuantity: "0.01", NetUnit: labelling.Gram, MinAgeMonths: &age}
			}
			var body strings.Builder
			if err := Product(ProductMeta(&view), &view).Render(ctx, &body); err != nil {
				t.Fatal(err)
			}
			markup := body.String()
			if strings.Contains(markup, `id="product-label-heading"`) != set {
				t.Errorf("%s facts=%v: section visibility wrong", locale, set)
			}
			if set && (!strings.Contains(markup, "&lt;script&gt;origin&lt;/script&gt;") || !strings.Contains(markup, "0.01 g") || !strings.Contains(markup, i18n.T(ctx, i18n.KeyProductLabelMinAge))) {
				t.Errorf("%s: facts missing or not escaped", locale)
			}
			if strings.Contains(markup, "<script>origin</script>") {
				t.Error("origin was not escaped")
			}
		}
	}
}

func TestMinimumAgeCountsMonthsInBothLanguages(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, n := range []int16{1, 2} {
			view := ProductView{LabelFacts: &labelling.Facts{MinAgeMonths: &n}}
			rows := view.LabelRows(i18n.WithLocale(t.Context(), locale))
			want := fmt.Sprintf("%d months and over", n)
			if n == 1 {
				want = "1 month and over"
			}
			if locale == i18n.ZhHant {
				want = fmt.Sprintf("%d 個月以上", n)
			}
			if len(rows) != 1 || rows[0].Value != want {
				t.Errorf("minimum age (%s, %d) = %+v, want %q", locale, n, rows, want)
			}
		}
	}
}

func TestLabelRowsKeepZeroAgeAndHideUnsetFields(t *testing.T) {
	t.Parallel()
	var none ProductView
	if len(none.LabelRows(t.Context())) != 0 || len((&ProductView{LabelFacts: &labelling.Facts{}}).LabelRows(t.Context())) != 0 {
		t.Fatal("unset facts rendered rows")
	}
	age := int16(0)
	view := ProductView{LabelFacts: &labelling.Facts{NetQuantity: "1.2", NetUnit: labelling.Piece, MinAgeMonths: &age}}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		rows := view.LabelRows(ctx)
		if len(rows) != 2 || rows[0].Value != "1.2 "+i18n.T(ctx, i18n.KeyProductLabelPiece) || rows[1].Value != i18n.T(ctx, i18n.KeyProductLabelAgeAll) {
			t.Fatalf("%s: rows=%+v", locale, rows)
		}
	}
}

func TestDomesticPartyCaptionsIdentifyTheDomesticContact(t *testing.T) {
	t.Parallel()
	view := ProductView{LabelFacts: &labelling.Facts{DomesticPartyName: "Maker", DomesticPartyPhone: "0123456789", DomesticPartyAddress: "Address"}}
	for _, tc := range []struct {
		locale   i18n.Locale
		captions []string
	}{
		{locale: i18n.ZhHant, captions: []string{"國內負責廠商名稱", "國內負責廠商電話", "國內負責廠商地址"}},
		{locale: i18n.En, captions: []string{"Domestic responsible party name", "Domestic responsible party phone", "Domestic responsible party address"}},
	} {
		t.Run(string(tc.locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tc.locale)
			rows := view.LabelRows(ctx)
			if len(rows) != len(tc.captions) {
				t.Fatalf("domestic facts=%v, want three contact rows", rows)
			}
			for i, row := range rows {
				if got := i18n.T(ctx, row.Term); got != tc.captions[i] {
					t.Errorf("domestic caption=%q, want %q", got, tc.captions[i])
				}
			}
		})
	}
}
