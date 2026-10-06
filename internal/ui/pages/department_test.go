package pages

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
)

func previewOf(products int, rows ...CompareRow) CompareView {
	v := CompareView{Rows: rows}
	for i := range products {
		v.Products = append(v.Products, CompareProduct{Slug: "p" + strconv.Itoa(i), Name: "Product " + strconv.Itoa(i)})
	}
	return v
}

func sharedRows(n, products int) []CompareRow {
	rows := make([]CompareRow, n)
	for i := range rows {
		values := make([]string, products)
		for j := range values {
			values[j] = "v"
		}
		rows[i] = CompareRow{Label: "Spec " + strconv.Itoa(i), Values: values}
	}
	return rows
}

func TestAComparisonPreviewNeedsTwoProductsAndThreeSharedRows(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		view CompareView
		want bool
	}{
		{"one product", previewOf(1, sharedRows(5, 1)...), false},
		{"two products, two rows", previewOf(2, sharedRows(2, 2)...), false},
		{"two products, three rows", previewOf(2, sharedRows(3, 2)...), true},
		{"rows only one product states", previewOf(2, CompareRow{Label: "a", Values: []string{"x", ""}}, CompareRow{Label: "b", Values: []string{"", "y"}}, CompareRow{Label: "c", Values: []string{"x", ""}}), false},
	} {
		if _, got := NewComparePreview("Laptops", tt.view); got != tt.want {
			t.Errorf("%s: NewComparePreview ok = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestAComparisonPreviewHoldsThreeProductsAndSixRows(t *testing.T) {
	t.Parallel()
	got, ok := NewComparePreview("Laptops", previewOf(4, sharedRows(8, 4)...))
	if !ok {
		t.Fatal("four products sharing eight rows are not a comparison")
	}
	if n := len(got.Table.Products); n != 3 {
		t.Errorf("preview shows %d products, want 3", n)
	}
	if n := len(got.Table.Rows); n != 6 {
		t.Errorf("preview shows %d rows, want 6", n)
	}
	for _, r := range got.Table.Rows {
		if len(r.Values) != 3 {
			t.Errorf("row %q has %d values, want one per shown product", r.Label, len(r.Values))
		}
	}
}

func TestTheComparisonPreviewLeavesOutWhatIsNotASpecification(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	preview, _ := NewComparePreview("Laptops", previewOf(2, sharedRows(3, 2)...))
	page := renderComponent(t, ctx, comparePreview(&preview))
	for _, banned := range []string{"goen-compare__remove", i18n.T(ctx, i18n.KeyCompareRowStock)} {
		if strings.Contains(page, banned) {
			t.Errorf("preview draws %q, which belongs to the comparison page", banned)
		}
	}
	for _, want := range []string{`class="goen-compare__table"`, `href="/compare?p=p0&amp;p=p1"`, "Spec 2"} {
		if !strings.Contains(page, want) {
			t.Errorf("preview omits %s", want)
		}
	}
}

func TestADepartmentHeadSaysOnlyTheCountsItHolds(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{Slug: "c", Name: "Books"}
	got := renderComponent(t, ctx, Listing(ListingMeta(ctx, view), view, nil, &DepartmentHead{Products: 4, Categories: 2}))
	for _, want := range []string{"<dt>商品</dt>", "<dt>分類</dt>"} {
		if !strings.Contains(got, want) {
			t.Errorf("head omits %s", want)
		}
	}
	if strings.Contains(got, "<dt>品牌</dt>") {
		t.Error("head states a brand count of zero")
	}
	none := renderComponent(t, ctx, Listing(ListingMeta(ctx, view), view, nil, &DepartmentHead{}))
	if strings.Contains(none, "ui-statline") {
		t.Error("a department holding nothing draws a fact line")
	}
}

func TestTheCampaignNoticeAppearsOnlyWithACampaign(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{Slug: "c", Name: "Books"}
	without := renderComponent(t, ctx, Listing(ListingMeta(ctx, view), view, nil, &DepartmentHead{Products: 1}))
	if strings.Contains(without, "goen-deptnotice") {
		t.Error("a department with no campaign draws a notice")
	}
	grid := components.PeriodSpec{Description: "秋日選物", TodayLabel: "今天", Cells: []components.PeriodCell{{State: components.CellToday}, {}}}
	notice := &DepartmentNotice{
		Title: "秋日選物", Href: "/s/autumn",
		Ends:   components.Stat{Label: "結束", Value: components.StatDate("10 月 30 日", "").WithDatetime("2026-10-30")},
		Period: &grid,
	}
	with := renderComponent(t, ctx, Listing(ListingMeta(ctx, view), view, nil, &DepartmentHead{Notice: notice}))
	for _, want := range []string{`class="goen-deptnotice"`, `href="/s/autumn"`, "<dt>結束</dt>", `class="ui-period"`, `datetime="2026-10-30"`} {
		if !strings.Contains(with, want) {
			t.Errorf("notice omits %s", want)
		}
	}
}

func TestTheEditorialSlotHoldsTheFirstThatApplies(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	preview, _ := NewComparePreview("Laptops", previewOf(2, sharedRows(3, 2)...))
	story := &ColourStory{Slug: "mug", Name: "Mug", Plates: []ColourPlate{{Colour: "燕麥"}, {Colour: "墨綠"}, {Colour: "霧灰"}}}

	for _, tt := range []struct {
		name string
		head DepartmentHead
		slot EditorialSlot
		want string
	}{
		{"both", DepartmentHead{Preview: &preview, Story: story}, SlotCompare, "goen-deptcompare"},
		{"story only", DepartmentHead{Story: story}, SlotStory, "goen-story"},
		{"neither", DepartmentHead{}, SlotNone, ""},
	} {
		if got := tt.head.Slot(); got != tt.slot {
			t.Errorf("%s: Slot = %v, want %v", tt.name, got, tt.slot)
		}
		page := renderComponent(t, ctx, departmentSlot(&tt.head))
		for _, other := range []string{"goen-deptcompare", "goen-story"} {
			if has := strings.Contains(page, `class="`+other); has != (other == tt.want) {
				t.Errorf("%s: draws %s = %v", tt.name, other, has)
			}
		}
	}
	if got, want := story.Title(ctx), "燕麥、墨綠、霧灰"; got != want {
		t.Errorf("story title = %q, want %q", got, want)
	}
}

func TestAComparableDepartmentSaysSoOnItsPage(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, offered := range []bool{true, false} {
		view := ListingView{Slug: "c", Name: "Tech", Theme: &Theme{Comparable: offered}}
		got := renderComponent(t, ctx, Listing(ListingMeta(ctx, view), view, nil, nil))
		if want := `data-comparable="` + strconv.FormatBool(offered) + `"`; !strings.Contains(got, want) {
			t.Errorf("Comparable %v: page omits %s", offered, want)
		}
	}
}

func TestTheFactLineFollowsTheTitleAndPrecedesTheSubCategories(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{Slug: "c", Name: "Books", Theme: &Theme{Children: []Crumb{{Slug: "a", Name: "Novels"}}}}
	got := renderComponent(t, ctx, Listing(ListingMeta(ctx, view), view, nil, &DepartmentHead{Products: 4}))
	title, facts, chips := strings.Index(got, "<h1"), strings.Index(got, "ui-statline"), strings.Index(got, "goen-pagehead__chips")
	if title < 0 || facts < 0 || chips < 0 || !(title < facts && facts < chips) {
		t.Errorf("order title=%d facts=%d chips=%d, want title < facts < chips", title, facts, chips)
	}
}

func TestAComparableTileDrawsItsSpecHighlights(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	got := renderComponent(t, ctx, Tile(ProductTile{Slug: "a", Name: "A", Comparable: true, Highlights: []string{"14″", "2.8K OLED"}}))
	if want := `<span class="goen-tile__specs">14″ · 2.8K OLED</span>`; !strings.Contains(got, want) {
		t.Errorf("tile omits %s", want)
	}
}

func TestTheDepartmentFrontIsTheUnfilteredFirstPage(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		view ListingView
		want bool
	}{
		{"first page", ListingView{Page: 1}, true},
		{"no page given", ListingView{}, true},
		{"second page", ListingView{Page: 2}, false},
		{"filtered", ListingView{Page: 1, Filtered: true}, false},
	} {
		if got := tt.view.IsFront(); got != tt.want {
			t.Errorf("%s: IsFront = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestTheCampaignEndStatNamesItsLastDayAndTheLastTwoDays(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	now := time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)
	ends := time.Date(2026, 10, 10, 16, 0, 0, 0, time.UTC)
	got := renderComponent(t, ctx, components.StatLine([]components.Stat{CampaignEndStat(ctx, ends, now)}, components.StatLinePlain))
	for _, want := range []string{`datetime="2026-10-10"`, "明天結束"} {
		if !strings.Contains(got, want) {
			t.Errorf("end stat omits %s:\n%s", want, got)
		}
	}
}
