package catalog

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestCompareKeptPostsToSet holds that a kept set is changed by posting to it:
// the page shows the cookie's products, so a link to another address would
// show something else and change nothing the header counts.
func TestCompareKeptPostsToSet(t *testing.T) {
	t.Parallel()
	v := compareOf("a", "b")
	v.Kept = true
	v.Query = "phone"
	v.Candidates = []CompareCandidate{{Slug: "c", Name: "Cee"}}
	html := renderToString(t, Compare(frame.Page{Title: "比較"}, v))
	for _, want := range []string{
		`<form class="goen-compare__change" method="post" action="/compare/items"><input type="hidden" name="slug" value="a"> <input type="hidden" name="action" value="remove"> <input type="hidden" name="return" value="/compare">`,
		`<input type="hidden" name="slug" value="c"> <input type="hidden" name="action" value="add">`,
		`href="/p/a"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the kept set's page lacks %s", want)
		}
	}
	for _, bad := range []string{`href="/compare?p=`, `name="p" value="a"`, `href="/p/a?p=`} {
		if strings.Contains(html, bad) {
			t.Errorf("the kept set's page links as a link's comparison does: %s", bad)
		}
	}
}

// TestComparePresets holds that the ready-made comparisons are links named by
// their count and category, that the one shown is marked as the current page,
// and that a kept set or a page with none offers no such mark or row.
func TestComparePresets(t *testing.T) {
	t.Parallel()
	presets := []ComparePreset{
		{Category: "手機", Slugs: []string{"a", "b", "c"}},
		{Category: "平板", Slugs: []string{"d", "e"}},
	}
	// The preset's products in another order are still its comparison.
	v := compareOf("c", "a", "b")
	v.Presets = presets
	html := renderToString(t, Compare(frame.Page{Title: "比較"}, v))
	for _, want := range []string{
		`<nav class="goen-compare__presets" aria-label="預設比較組合">`,
		`<a class="goen-compare__preset" aria-current="page" href="/compare?p=a&amp;p=b&amp;p=c">比較 3 款手機</a>`,
		`<a class="goen-compare__preset" href="/compare?p=d&amp;p=e">比較 2 款平板</a>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the presets lack %s", want)
		}
	}
	en := renderIn(t, i18n.En, Compare(frame.Page{Title: "Compare"}, v))
	if !strings.Contains(en, ">Compare 2 in 平板</a>") {
		t.Error("the English preset does not read Compare 2 in its category")
	}
	other := compareOf("a", "b", "d")
	other.Presets = presets
	if strings.Contains(renderToString(t, Compare(frame.Page{Title: "比較"}, other)), `aria-current="page" href="/compare?p=`) {
		t.Error("a comparison of other products marks a preset as the page it is on")
	}
	v.Kept = true
	if strings.Contains(renderToString(t, Compare(frame.Page{Title: "比較"}, v)), `aria-current="page" href="/compare?p=`) {
		t.Error("a kept set marks a preset's link as the page it is on")
	}
	if strings.Contains(renderToString(t, Compare(frame.Page{Title: "比較"}, compareOf("a", "b"))), "goen-compare__presets") {
		t.Error("a page with no presets draws their row")
	}
}

// TestCompareDiffSwitch holds that the differences-only switch is offered only
// where it would change something, that every differing row carries the class
// it keeps, a spec one product leaves unstated included, and that the row that
// buys never carries it, since the stylesheet keeps that row by its own name.
func TestCompareDiffSwitch(t *testing.T) {
	t.Parallel()
	differ := CompareRow{Label: "螢幕", Values: []string{"6.1", "6.7"}, SharedBy: 2}
	alike := CompareRow{Label: "重量", Values: []string{"199 g", "199 g"}, SharedBy: 2}
	partial := CompareRow{Label: "電池", Values: []string{"4000 mAh", ""}, SharedBy: 1}
	for _, tt := range []struct {
		name string
		rows []CompareRow
		want bool
	}{
		{"some of each", []CompareRow{differ, alike}, true},
		{"only alike", []CompareRow{alike}, false},
		{"only differing", []CompareRow{differ, partial}, false},
	} {
		// No reviews and no warranty: the facts state nothing, so no row of them
		// is drawn and only these rows decide.
		v := compareOf("a", "b")
		v.Rows = tt.rows
		html := renderToString(t, Compare(frame.Page{Title: "比較"}, v))
		if got := strings.Contains(html, `id="compare-diff"`); got != tt.want {
			t.Errorf("%s: the switch is offered = %v, want %v", tt.name, got, tt.want)
		}
	}
	v := compareOf("a", "b")
	v.Rows = []CompareRow{differ, alike, partial}
	html := renderToString(t, Compare(frame.Page{Title: "比較"}, v))
	if n := strings.Count(html, `<tr class="is-differs">`); n != 2 {
		t.Errorf("%d rows carry is-differs, want the two that differ", n)
	}
	if !strings.Contains(html, `<tr class="goen-compare__buy">`) {
		t.Error("the row that buys is not named for the stylesheet to keep")
	}
	if !strings.Contains(html, `<label class="goen-compare__diffonly"><input class="goen-check__box" type="checkbox" id="compare-diff"> <span>僅顯示差異項目</span></label>`) {
		t.Error("the switch is not a labelled checkbox")
	}
}

// TestCompareBuyColumn holds what each column offers to buy: a product with one
// variant in stock is added by the cart's own form, whose script answer lands in
// the column's status and the header's cart; any other in stock is chosen on
// its page; one sold out says so, once.
func TestCompareBuyColumn(t *testing.T) {
	t.Parallel()
	v := CompareView{Products: []CompareProduct{
		{Slug: "tea", Name: "Tea", InStock: true, SoleVariantID: "7c1f2b9e-5d34-4a6e-9b0c-3e8a1d2f4c60"},
		{Slug: "phone", Name: "Phone", InStock: true},
		{Slug: "gone", Name: "Gone"},
	}}
	html := renderToString(t, Compare(frame.Page{Title: "比較"}, v))
	for _, want := range []string{
		`<form class="goen-compare__cart" method="post" action="/cart/items" hx-post="/cart/items" hx-target="#compare-added-tea" hx-select="#added" hx-swap="innerHTML" hx-select-oob="#cart-link" hx-push-url="false">`,
		`<input type="hidden" name="variant" value="7c1f2b9e-5d34-4a6e-9b0c-3e8a1d2f4c60"> <input type="hidden" name="quantity" value="1"> <input type="hidden" name="back" value="tea">`,
		`<div class="goen-compare__added" id="compare-added-tea" role="status"></div>`,
		`href="/p/phone?p=tea&amp;p=phone&amp;p=gone">請選擇選項 <span class="goen-sr-only"> Phone</span>`,
		`<span class="goen-compare__soldout">已售完</span>`,
		`aria-label="移除 Gone"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the columns lack %s", want)
		}
	}
	if n := strings.Count(html, `action="/cart/items"`); n != 1 {
		t.Errorf("%d columns post to the cart, want only the one whose variant is named", n)
	}
}

// TestGroupPresets holds that the query's rows fold into one preset per
// category, by the category's slug, not its name, and that no more than
// presetLimit are offered.
func TestGroupPresets(t *testing.T) {
	t.Parallel()
	rows := make([]db.ComparePresetsRow, 0, 10)
	for i, c := range []string{"p", "p", "q", "q", "r", "r", "s", "s", "t", "t"} {
		rows = append(rows, db.ComparePresetsRow{CategorySlug: c, Category: "同名", Slug: c + strconv.Itoa(i)})
	}
	got := groupPresets(rows)
	want := []ComparePreset{
		{Category: "同名", Slugs: []string{"p0", "p1"}, slug: "p"},
		{Category: "同名", Slugs: []string{"q2", "q3"}, slug: "q"},
		{Category: "同名", Slugs: []string{"r4", "r5"}, slug: "r"},
		{Category: "同名", Slugs: []string{"s6", "s7"}, slug: "s"},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(ComparePreset{})); diff != "" {
		t.Errorf("groupPresets mismatch (-want +got):\n%s", diff)
	}
}

// TestCompareFacts holds the rows before the specifications: a product with no
// reviews or no stated warranty states nothing there, and the department row
// appears only when the products are not all from one.
func TestCompareFacts(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	v := CompareView{Products: []CompareProduct{
		{Slug: "a", Category: "手機", Rating: 4.5, RatingCount: 2, WarrantyMonths: 24},
		{Slug: "b", Category: "手機"},
	}}
	want := []CompareRow{
		{Label: "評價", Values: []string{"4.5 (2)", ""}, SharedBy: 1},
		{Label: "保固", Values: []string{"24 個月", ""}, SharedBy: 1},
	}
	if diff := cmp.Diff(want, v.Facts(ctx)); diff != "" {
		t.Errorf("Facts mismatch (-want +got):\n%s", diff)
	}
	if rows := (CompareView{Products: []CompareProduct{{Slug: "a"}, {Slug: "b"}}}).Facts(ctx); len(rows) != 0 {
		t.Errorf("products stating no rating and no warranty get rows of dashes: %+v", rows)
	}
	v.Products[1].Category = "平板"
	if rows := v.Facts(ctx); len(rows) != 3 || rows[2].Label != "館別" || !slices.Equal(rows[2].Values, []string{"手機", "平板"}) {
		t.Errorf("products from two departments do not say which: %+v", rows)
	}
}
