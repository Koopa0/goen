package pages

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

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

func TestTheCampaignNoticeAppearsOnlyWithACampaign(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{Slug: "c", Name: "Books"}
	without := renderComponent(t, ctx, Listing(ListingMeta(ctx, view), view, nil, &DepartmentHead{}))
	if strings.Contains(without, "goen-deptnotice") {
		t.Error("a department with no campaign draws a notice")
	}
	now := time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)
	ends := time.Date(2026, 10, 30, 16, 0, 0, 0, time.UTC)
	notice := &DepartmentNotice{Title: "秋日選物", Href: "/s/autumn", End: NewCampaignEnd(ends, now)}
	with := renderComponent(t, ctx, Listing(ListingMeta(ctx, view), view, nil, &DepartmentHead{Notice: notice}))
	for _, want := range []string{`class="goen-deptnotice"`, `aria-label="活動"`, `href="/s/autumn"`, `<time datetime="2026-10-30">`} {
		if !strings.Contains(with, want) {
			t.Errorf("notice omits %s", want)
		}
	}
}

// The notice is one line of words: its last day and what is left are said, and
// nothing is drawn. The arrow keeps to the last words with a no-break space.
func TestACampaignNoticeSaysItsEndInWords(t *testing.T) {
	t.Parallel()
	cst := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, cst)
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		ends   time.Time
		until  string
		left   string
		time   string
	}{
		{"nine days left", i18n.ZhHant, time.Date(2026, 10, 19, 0, 0, 0, 0, cst), "至 10\u00a0月 18\u00a0日", "剩\u00a09\u00a0天", "2026-10-18"},
		{"nine days left", i18n.En, time.Date(2026, 10, 19, 0, 0, 0, 0, cst), "until Oct\u00a018,", "9\u00a0days left", "2026-10-18"},
		{"ends tomorrow", i18n.ZhHant, time.Date(2026, 10, 11, 0, 0, 0, 0, cst), "", "明天結束", "2026-10-10"},
		{"ends tomorrow", i18n.En, time.Date(2026, 10, 11, 0, 0, 0, 0, cst), "", "ends tomorrow", "2026-10-10"},
		{"ends today", i18n.ZhHant, time.Date(2026, 10, 10, 0, 0, 0, 0, cst), "", "今天結束", "2026-10-09"},
		{"ends today", i18n.En, time.Date(2026, 10, 10, 0, 0, 0, 0, cst), "", "ends today", "2026-10-09"},
		{"ends today at six", i18n.ZhHant, time.Date(2026, 10, 9, 18, 0, 0, 0, cst), "", "今天 18:00 結束", "2026-10-09T18:00"},
		{"ends today at six", i18n.En, time.Date(2026, 10, 9, 18, 0, 0, 0, cst), "", "ends today at 18:00", "2026-10-09T18:00"},
		{"three days left ending at six", i18n.ZhHant, time.Date(2026, 10, 12, 18, 0, 0, 0, cst), "至 10\u00a0月 12\u00a0日 18:00", "剩\u00a03\u00a0天", "2026-10-12T18:00"},
		{"three days left ending at six", i18n.En, time.Date(2026, 10, 12, 18, 0, 0, 0, cst), "until Oct\u00a012 at 18:00,", "3\u00a0days left", "2026-10-12T18:00"},
		{"tomorrow ending at six", i18n.ZhHant, time.Date(2026, 10, 10, 18, 0, 0, 0, cst), "", "明天 18:00 結束", "2026-10-10T18:00"},
		{"tomorrow ending at six", i18n.En, time.Date(2026, 10, 10, 18, 0, 0, 0, cst), "", "ends tomorrow at 18:00", "2026-10-10T18:00"},
	} {
		t.Run(tt.name+" "+string(tt.locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			notice := &DepartmentNotice{Title: "秋日選物", Href: "/s/autumn", End: NewCampaignEnd(tt.ends, now)}
			doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, departmentNotice(notice))))
			if err != nil {
				t.Fatal(err)
			}
			link := findDescendant(doc, func(n *html.Node) bool { return hasClass(n, "goen-deptnotice__name") })
			if link == nil {
				t.Fatal("departmentNotice draws no goen-deptnotice__name")
			}
			eyebrow := i18n.T(ctx, i18n.KeyCampaignEyebrow)
			want := eyebrow + " " + notice.Title + "\u00a0· "
			if tt.until != "" {
				want += tt.until + "\u00a0· "
			}
			want += tt.left + "\u00a0\u2192"
			if got := nodeText(link); got != want {
				t.Errorf("the notice reads %q, want %q", got, want)
			}
			day := findDescendant(link, func(n *html.Node) bool { return n.Data == "time" })
			if day == nil || attrValue(day, "datetime") != tt.time {
				t.Errorf("the notice's time element reads datetime %q, want %q", attrValue(day, "datetime"), tt.time)
			}
			left := findDescendant(link, func(n *html.Node) bool { return hasClass(n, "goen-deptnotice__left") })
			if left == nil || nodeText(left) != tt.left+"\u00a0\u2192" {
				t.Error("the notice does not set apart what is left, with the arrow kept to it")
			}
			for n := range link.Descendants() {
				if n.Type == html.TextNode && strings.TrimSpace(n.Data) == "·" && attrValue(n.Parent, "aria-hidden") != "true" {
					t.Error("a dot between the notice's parts is read aloud")
				}
			}
			if drawn := findDescendant(doc, func(n *html.Node) bool { return hasClass(n, "ui-period") || hasClass(n, "ui-statline") }); drawn != nil {
				t.Errorf("the notice draws %s; it says its end in words", attrValue(drawn, "class"))
			}
		})
	}
}

// The notice is the band's last row, on the department's tone, not a strip under it.
func TestTheCampaignNoticeIsTheBandsLastRow(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{Slug: "c", Name: "Books", Theme: &Theme{Tone: ToneSage, Photo: Photo{URL: "/d.webp"}}}
	now := time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)
	notice := &DepartmentNotice{Title: "秋日選物", Href: "/s/autumn", End: NewCampaignEnd(time.Date(2026, 10, 30, 16, 0, 0, 0, time.UTC), now)}
	doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, Listing(ListingMeta(ctx, view), view, nil, &DepartmentHead{Notice: notice}))))
	if err != nil {
		t.Fatal(err)
	}
	band := findDescendant(doc, func(n *html.Node) bool { return hasClass(n, "goen-band") })
	if band == nil {
		t.Fatal("the department head draws no band")
	}
	var last *html.Node
	for c := band.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			last = c
		}
	}
	if last == nil || !hasClass(last, "goen-deptnotice") {
		t.Error("the campaign notice is not the band's last row")
	}
	if got := nodeText(last); !strings.Contains(got, notice.Title) {
		t.Errorf("the notice does not name the campaign %q: %q", notice.Title, got)
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
