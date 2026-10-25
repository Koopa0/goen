package home

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

func departmentsOf(n int) []Category {
	cats := make([]Category, n)
	for i := range cats {
		cats[i] = Category{Slug: fmt.Sprintf("d%d", i), Name: fmt.Sprintf("館%d", i)}
	}
	return cats
}

// TestView_ShowsDirectory holds that the directory is drawn only when it lists
// more than the band does.
func TestView_ShowsDirectory(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name        string
		departments int
		band        bool
		want        bool
	}{
		{"none", 0, true, false},
		{"none, no band", 0, false, false},
		{"one with the band: the band is the department", 1, true, false},
		{"one with no band", 1, false, true},
		{"two", 2, true, true},
		{"six", 6, true, true},
		{"ten", 10, true, true},
	} {
		view := View{Categories: departmentsOf(tt.departments)}
		if tt.band {
			view.Band = &DepartmentBand{Name: "館0", Href: "/c/d0", Tiles: tiledShelf(4)}
		}
		if got := view.ShowsDirectory(); got != tt.want {
			t.Errorf("%s: ShowsDirectory() = %t, want %t", tt.name, got, tt.want)
		}
		page := renderIn(t, i18n.ZhHant, Page(frame.Page{}, view))
		if got := strings.Contains(page, "goen-cats__grid"); got != tt.want {
			t.Errorf("%s: directory drawn = %t, want %t", tt.name, got, tt.want)
		}
		if tt.want {
			if got := strings.Count(page, `class="goen-cat"`); got != tt.departments {
				t.Errorf("%s: %d tiles, want one per department (%d)", tt.name, got, tt.departments)
			}
		}
		for _, not := range []string{"goen-cat__stage", "goen-cat__subs", "goen-cat__count", "goen-cat__line", "goen-cats__grid--"} {
			if strings.Contains(page, not) {
				t.Errorf("%s: the page still draws %s", tt.name, not)
			}
		}
	}
}

// TestDepartmentDirectoryOneTile: With one department and no band drawn, the
// department is the directory's one tile: the home page never names it only in
// the header.
func TestDepartmentDirectoryOneTile(t *testing.T) {
	t.Parallel()
	page := renderIn(t, i18n.ZhHant, Page(frame.Page{}, View{Categories: departmentsOf(1)}))
	if !strings.Contains(page, "goen-cats__grid") || !strings.Contains(page, `href="/c/d0"`) {
		t.Error("the only department is not drawn as a tile")
	}
}

// TestDepartmentTilePictureName: A tile is the department's name and nothing
// else: no number of products, no sub-categories.
func TestDepartmentTilePictureName(t *testing.T) {
	t.Parallel()
	page := renderIn(t, i18n.ZhHant, Page(frame.Page{}, View{Categories: departmentsOf(2)}))
	want := `<a class="goen-cat" data-tone="stone" href="/c/d0"><span class="goen-cat__well"><span class="goen-cat__mark" aria-hidden="true">館</span></span> <span class="goen-cat__name">館0</span></a>`
	if !strings.Contains(page, want) {
		t.Errorf("the tile is not its picture and name; want %s", want)
	}
}

// TestDepartmentTileNoPhoto: A department without a photograph is its tone and
// the first character of its name, hidden from a screen reader because the name
// stands beside it; a department with no tone is stone.
func TestDepartmentTileNoPhoto(t *testing.T) {
	t.Parallel()
	cats := departmentsOf(3)
	cats[1] = Category{Slug: "wood", Name: "棲木家居"}
	page := renderIn(t, i18n.ZhHant, Page(frame.Page{}, View{Categories: cats}))
	if !strings.Contains(page, `<span class="goen-cat__mark" aria-hidden="true">棲</span>`) {
		t.Error("the department with no photograph lost its first-character square")
	}
	if !strings.Contains(page, `data-tone="stone" href="/c/wood"`) {
		t.Error("a department with no tone is not stone")
	}
	pair := departmentsOf(2)
	pair[0].Name = "棲木家居"
	tiles := renderIn(t, i18n.ZhHant, Page(frame.Page{}, View{Categories: pair}))
	if !strings.Contains(tiles, `<span class="goen-cat__well"><span class="goen-cat__mark" aria-hidden="true">棲</span></span>`) {
		t.Error("a tile with no photograph lost its first-character square")
	}
	if got := (Category{}).Initial(); got != "" {
		t.Errorf("Initial of no name = %q, want none", got)
	}
}

var rowNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))

func campaignRow(tiles int, endsAt time.Time) ProductRow {
	return ProductRow{
		Title: "秋日選物", Href: "/s/autumn", Tiles: tiledShelf(tiles),
		Campaign: &RowCampaign{Items: 6, End: campaign.NewEnd(endsAt, rowNow)},
	}
}

// TestProductRowEqualTiles holds that a campaign's row is one kind of tile at
// every count, and that its heading always carries the way to the rest.
func TestProductRowEqualTiles(t *testing.T) {
	t.Parallel()
	endsAt := rowNow.AddDate(0, 0, 3)
	for _, tiles := range []int{1, 2, 3, 4, 5, 12} {
		page := renderIn(t, i18n.ZhHant, Page(frame.Page{}, View{Row: campaignRow(tiles, endsAt)}))
		if got := strings.Count(page, `<li class="goen-tile__cell">`); got != tiles {
			t.Errorf("%d products: %d tiles drawn", tiles, got)
		}
		for _, not := range []string{"goen-tiles__grid--lead", "goen-tile__cell--lead", "goen-rowcard", "goen-home__facts", "ui-period"} {
			if strings.Contains(page, not) {
				t.Errorf("%d products: the row draws %s", tiles, not)
			}
		}
		if !strings.Contains(page, `<ul class="goen-tiles__grid goen-home__row" role="list">`) {
			t.Errorf("%d products: the tiles are not the shared grid", tiles)
		}
		if got := strings.Count(page, "看全部 6 件"); got != 1 {
			t.Errorf("%d products: the heading links 看全部 6 件 %d times, want once", tiles, got)
		}
		if got := strings.Count(page, `href="/s/autumn"`); got != 1 {
			t.Errorf("%d products: the row links its campaign %d times, want once", tiles, got)
		}
		if !strings.Contains(page, `sizes="(min-width: 1344px) 286px`) || strings.Contains(page, "596px") {
			t.Errorf("%d products: the tiles do not share the plain sizes", tiles)
		}
	}
}

// TestProductRowCampaignEnds holds that the heading says when the campaign
// ends: the last day and the days left, then on the last two days only that.
func TestProductRowCampaignEnds(t *testing.T) {
	t.Parallel()
	taipei := rowNow.Location()
	for _, tt := range []struct {
		name     string
		locale   i18n.Locale
		endsAt   time.Time
		want     string
		datetime string
		not      string
	}{
		{"days left", i18n.ZhHant, time.Date(2026, 10, 13, 0, 0, 0, 0, taipei), "至10\u00a0月12\u00a0日&nbsp;·剩\u00a03\u00a0天", `<time datetime="2026-10-12">`, ""},
		{"days left in English", i18n.En, time.Date(2026, 10, 13, 0, 0, 0, 0, taipei), "untilOct\u00a012,&nbsp;·3\u00a0daysleft", `<time datetime="2026-10-12">`, ""},
		{"ends at a time of day", i18n.ZhHant, time.Date(2026, 10, 13, 18, 0, 0, 0, taipei), "至10\u00a0月13\u00a0日18:00&nbsp;·剩\u00a04\u00a0天", `<time datetime="2026-10-13T18:00">`, ""},
		{"tomorrow", i18n.ZhHant, time.Date(2026, 10, 11, 0, 0, 0, 0, taipei), "明天結束", `<time class="goen-home__left" datetime="2026-10-10">`, "至"},
	} {
		page := renderIn(t, tt.locale, Page(frame.Page{}, View{Row: campaignRow(4, tt.endsAt)}))
		head := page[strings.Index(page, `<div class="goen-home__head">`):strings.Index(page, `<ul class="goen-tiles__grid`)]
		if !strings.Contains(text(head), tt.want) {
			t.Errorf("%s: the heading reads %q, want it to hold %q", tt.name, text(head), tt.want)
		}
		if !strings.Contains(head, tt.datetime) {
			t.Errorf("%s: the heading lacks %s: %s", tt.name, tt.datetime, head)
		}
		if dot := strings.Index(head, `<span class="goen-home__dot"`); tt.not == "" && !strings.HasSuffix(head[:max(dot, 0)], "&nbsp;") {
			t.Errorf("%s: the dot does not follow a no-break space inside the day's span: %s", tt.name, head)
		}
		if tt.not != "" && strings.Contains(text(head), tt.not) {
			t.Errorf("%s: the heading holds %q: %q", tt.name, tt.not, text(head))
		}
	}
}

// TestProductRowNewIn holds that a row with no campaign is drawn as New in, in
// the same grid and with no campaign sentence.
func TestProductRowNewIn(t *testing.T) {
	t.Parallel()
	row := ProductRow{Title: "新到貨", Href: "/search", Tiles: tiledShelf(4)}
	page := renderIn(t, i18n.ZhHant, Page(frame.Page{}, View{Row: row}))
	for _, want := range []string{">新到貨</h2>", "看全部商品", `href="/search"`, `class="goen-tiles__grid goen-home__row"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the row lacks %q", want)
		}
	}
	for _, not := range []string{"goen-home__ends", "ui-period"} {
		if strings.Contains(page, not) {
			t.Errorf("a row with no campaign draws %s", not)
		}
	}
}

// TestProductRowNewInLink holds that the New in link says products in both
// languages.
func TestProductRowNewInLink(t *testing.T) {
	t.Parallel()
	row := ProductRow{Title: "New in", Href: "/search", Tiles: tiledShelf(4)}
	page := renderIn(t, i18n.En, Page(frame.Page{}, View{Row: row}))
	if !strings.Contains(page, "See all products") {
		t.Error("the New in link does not read See all products")
	}
}

// TestDepartmentBandLinkCount holds that the band link counts the department's
// products.
func TestDepartmentBandLinkCount(t *testing.T) {
	t.Parallel()
	for locale, want := range map[i18n.Locale]string{i18n.ZhHant: "看全部 20 件", i18n.En: "See all 20 items"} {
		page := renderIn(t, locale, Page(frame.Page{}, View{Band: &DepartmentBand{
			Name: "Tech", Items: 20, Href: "/c/tech", Tiles: tiledShelf(4),
		}}))
		if !strings.Contains(page, want) {
			t.Errorf("%s: the band link is not %q", locale, want)
		}
	}
}
