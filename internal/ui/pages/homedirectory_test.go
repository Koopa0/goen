package pages

import (
	"fmt"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func departmentsOf(n int) []HomeCategory {
	cats := make([]HomeCategory, n)
	for i := range cats {
		cats[i] = HomeCategory{Slug: fmt.Sprintf("d%d", i), Name: fmt.Sprintf("館%d", i)}
	}
	return cats
}

func TestTheDirectoryIsDrawnWhenItListsMoreThanTheBand(t *testing.T) {
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
		view := HomeView{Categories: departmentsOf(tt.departments)}
		if tt.band {
			view.Band = &DepartmentBand{Name: "館0", Href: "/c/d0", Tiles: tiledShelf(4)}
		}
		if got := view.ShowsDirectory(); got != tt.want {
			t.Errorf("%s: ShowsDirectory() = %t, want %t", tt.name, got, tt.want)
		}
		page := renderIn(t, i18n.ZhHant, Home(layouts.Page{}, view))
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

// With one department and no band drawn, the department is the directory's one
// tile: the home page never names it only in the header.
func TestOneDepartmentWithNoBandIsTheDirectorysOneTile(t *testing.T) {
	t.Parallel()
	page := renderIn(t, i18n.ZhHant, Home(layouts.Page{}, HomeView{Categories: departmentsOf(1)}))
	if !strings.Contains(page, "goen-cats__grid") || !strings.Contains(page, `href="/c/d0"`) {
		t.Error("the only department is not drawn as a tile")
	}
}

// A tile is the department's name and nothing else: no number of products, no
// sub-categories.
func TestADepartmentTileHoldsOnlyItsPictureAndName(t *testing.T) {
	t.Parallel()
	page := renderIn(t, i18n.ZhHant, Home(layouts.Page{}, HomeView{Categories: departmentsOf(2)}))
	want := `<a class="goen-cat" data-tone="stone" href="/c/d0"><span class="goen-cat__well"><span class="goen-cat__mark" aria-hidden="true">館</span></span> <span class="goen-cat__name">館0</span></a>`
	if !strings.Contains(page, want) {
		t.Errorf("the tile is not its picture and name; want %s", want)
	}
}

// A department without a photograph is its tone and the first character of its
// name, hidden from a screen reader because the name stands beside it; a
// department with no tone is stone.
func TestADepartmentWithoutAPhotographIsItsToneAndFirstCharacter(t *testing.T) {
	t.Parallel()
	cats := departmentsOf(3)
	cats[1] = HomeCategory{Slug: "wood", Name: "棲木家居"}
	page := renderIn(t, i18n.ZhHant, Home(layouts.Page{}, HomeView{Categories: cats}))
	if !strings.Contains(page, `<span class="goen-cat__mark" aria-hidden="true">棲</span>`) {
		t.Error("the department with no photograph lost its first-character square")
	}
	if !strings.Contains(page, `data-tone="stone" href="/c/wood"`) {
		t.Error("a department with no tone is not stone")
	}
	pair := departmentsOf(2)
	pair[0].Name = "棲木家居"
	tiles := renderIn(t, i18n.ZhHant, Home(layouts.Page{}, HomeView{Categories: pair}))
	if !strings.Contains(tiles, `<span class="goen-cat__well"><span class="goen-cat__mark" aria-hidden="true">棲</span></span>`) {
		t.Error("a tile with no photograph lost its first-character square")
	}
	if got := (HomeCategory{}).Initial(); got != "" {
		t.Errorf("Initial of no name = %q, want none", got)
	}
}

func campaignRow(firstPhotoWidth int32, tiles int) ProductRow {
	shelf := tiledShelf(tiles)
	shelf[0].ImageWidth = firstPhotoWidth
	return ProductRow{
		Title: "秋日選物", Href: "/s/autumn", Tiles: shelf,
		Campaign: &RowCampaign{
			Tone: ToneSage, Items: 6,
			Facts:     []components.Stat{{Label: "商品", Value: components.StatCount(6, "件")}},
			CardFacts: []components.Stat{{Label: "結束", Value: components.StatCount(3, "天")}},
			Period:    &components.PeriodSpec{Cells: []components.PeriodCell{{}, {}}, Description: "兩天"},
		},
	}
}

func TestTheLeadTileNeedsAWidePhotographAndFourProducts(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		width int32
		tiles int
		lead  bool
	}{
		{"photograph of unknown width", 0, 4, false},
		{"just too narrow", 1199, 4, false},
		{"wide enough", 1200, 4, true},
		{"three products", 1600, 3, false},
	} {
		row := campaignRow(tt.width, tt.tiles)
		page := renderIn(t, i18n.ZhHant, Home(layouts.Page{}, HomeView{Row: row}))
		if got := strings.Contains(page, "goen-tiles__grid--lead"); got != tt.lead {
			t.Errorf("%s: lead tile = %t, want %t", tt.name, got, tt.lead)
		}
		if got := strings.Contains(page, `class="goen-rowcard"`); got != tt.lead {
			t.Errorf("%s: campaign card = %t, want %t", tt.name, got, tt.lead)
		}
		if got := strings.Contains(page, "看全部 6 件"); !got {
			t.Errorf("%s: the campaign's link is not 看全部 6 件", tt.name)
		}
		if got := strings.Contains(page, `sizes="(min-width: 1344px) 596px`); got != tt.lead {
			t.Errorf("%s: lead sizes = %t, want %t", tt.name, got, tt.lead)
		}
		if !strings.Contains(page, `sizes="(min-width: 1344px) 286px`) {
			t.Errorf("%s: the plain cards lost their sizes", tt.name)
		}
		// Without a lead the campaign's facts stand under the heading.
		if got := strings.Contains(page, "goen-home__facts"); got == tt.lead {
			t.Errorf("%s: facts under the heading = %t", tt.name, got)
		}
	}
}

// The card's one link is the count: the card is not a second way into the
// campaign page around the heading's, and nothing in it is a link but that.
func TestTheCampaignCardIsNotOneBigLink(t *testing.T) {
	t.Parallel()
	page := renderIn(t, i18n.ZhHant, Home(layouts.Page{}, HomeView{Row: campaignRow(1600, 4)}))
	start := strings.Index(page, `<li class="goen-rowcard">`)
	if start < 0 {
		t.Fatal("no campaign card")
	}
	card := page[start : start+strings.Index(page[start:], "</li>")]
	if got := strings.Count(card, "<a "); got != 1 {
		t.Fatalf("the card holds %d links, want 1", got)
	}
	if !strings.Contains(card, "看全部 6 件") || !strings.Contains(card, `href="/s/autumn"`) {
		t.Errorf("the card's link is not 看全部 6 件 to the campaign: %s", card)
	}
	if !strings.Contains(card, `class="ui-period"`) {
		t.Error("the card lost its day grid")
	}
	if strings.Contains(page, `class="goen-home__more" href="/s/autumn"`) && strings.Count(page, `href="/s/autumn"`) != 1 {
		t.Error("a lead row links its campaign twice")
	}
}

func TestWithNoCampaignTheRowIsNewInWithNoCard(t *testing.T) {
	t.Parallel()
	row := ProductRow{Title: "新到貨", Href: "/search", Tiles: tiledShelf(4)}
	page := renderIn(t, i18n.ZhHant, Home(layouts.Page{}, HomeView{Row: row}))
	for _, want := range []string{">新到貨</h2>", "看全部商品", `href="/search"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the row lacks %q", want)
		}
	}
	for _, not := range []string{"goen-rowcard", "goen-tiles__grid--lead", "ui-period", "goen-home__facts"} {
		if strings.Contains(page, not) {
			t.Errorf("a row with no campaign draws %s", not)
		}
	}
}

func TestTheNewInLinkSaysProductsInBothLanguages(t *testing.T) {
	t.Parallel()
	row := ProductRow{Title: "New in", Href: "/search", Tiles: tiledShelf(4)}
	page := renderIn(t, i18n.En, Home(layouts.Page{}, HomeView{Row: row}))
	if !strings.Contains(page, "See all products") {
		t.Error("the New in link does not read See all products")
	}
}

func TestTheBandLinkCountsTheDepartmentsProducts(t *testing.T) {
	t.Parallel()
	for locale, want := range map[i18n.Locale]string{i18n.ZhHant: "看全部 20 件", i18n.En: "See all 20 items"} {
		page := renderIn(t, locale, Home(layouts.Page{}, HomeView{Band: &DepartmentBand{
			Name: "Tech", Items: 20, Href: "/c/tech", Tiles: tiledShelf(4),
		}}))
		if !strings.Contains(page, want) {
			t.Errorf("%s: the band link is not %q", locale, want)
		}
	}
}
