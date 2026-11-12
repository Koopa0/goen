package tile

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
)

// TestTileColourDots holds that a card draws up to four colours and counts the
// rest.
//
// A colour no variant of which is in stock is drawn faded and struck through;
// a colour past the fourth is counted, not drawn, so it is not marked. The
// spoken name says which colours are sold out, drawn or not, in either
// language; with none it is the count alone.
func TestTileColourDots(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	base := Tile{Slug: "a", Name: "A", PriceCents: 100000, InStock: true}
	hexes := []string{"#111111", "#222222", "#333333", "#444444", "#555555", "#666666", "#777777", "#888888", "#999999"}

	tests := []struct {
		name    string
		colours []string
		soldOut []string
		dots    int
		more    string
		struck  []string
		// soldOutEn is soldOut as an English page's query names them;
		// spoken and spokenEn are the colours' spoken names.
		soldOutEn        []string
		spoken, spokenEn string
	}{
		{"none", nil, nil, 0, "", nil, nil, "", ""},
		{"one colour is not a choice", hexes[:1], nil, 0, "", nil, nil, "", ""},
		{"two", hexes[:2], nil, 2, "", nil, nil, "2 種顏色", "2 colours"},
		{"four", hexes[:4], nil, 4, "", nil, nil, "4 種顏色", "4 colours"},
		{"nine", hexes, nil, 4, "+5", nil, nil, "9 種顏色", "9 colours"},
		{"walnut sold out", hexes[:2], []string{"胡桃", ""}, 2, "", []string{"#111111"}, []string{"Walnut", ""},
			"2 種顏色，胡桃已售完", "2 colours, Walnut sold out"},
		{"two sold out", hexes[:3], []string{"胡桃", "", "墨黑"}, 3, "", []string{"#111111", "#333333"}, []string{"Walnut", "", "Ink"},
			"3 種顏色，胡桃、墨黑已售完", "3 colours, Walnut and Ink sold out"},
		{"sold out past the dots", hexes[:6], []string{"", "", "", "", "", "白"}, 4, "+2", nil, []string{"", "", "", "", "", "White"},
			"6 種顏色，白已售完", "6 colours, White sold out"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tile := base
			tile.Colours = tc.colours
			tile.ColoursSoldOut = tc.soldOut
			got := renderComponent(t, ctx, Cell(tile))
			if n := strings.Count(got, `class="goen-tile__slash"`); n != len(tc.struck) {
				t.Errorf("Cell(%d colours, sold out %q) struck %d dots, want %d", len(tc.colours), tc.soldOut, n, len(tc.struck))
			}
			for _, hex := range tc.struck {
				if !strings.Contains(got, `fill="`+hex+`" opacity="0.55"`) {
					t.Errorf("Cell(sold out %q): the dot %s is not faded", tc.soldOut, hex)
				}
			}
			if n := strings.Count(got, `opacity="0.55"`); n != len(tc.struck) {
				t.Errorf("Cell(%d colours, sold out %q) faded %d dots, want %d", len(tc.colours), tc.soldOut, n, len(tc.struck))
			}
			if n := strings.Count(got, `class="goen-tile__dot"`); n != tc.dots {
				t.Errorf("Cell(%d colours) drew %d dots, want %d", len(tc.colours), n, tc.dots)
			}
			if has := strings.Contains(got, "goen-tile__colours"); has != (tc.dots > 0) {
				t.Errorf("Cell(%d colours) colour list present = %v, want %v", len(tc.colours), has, tc.dots > 0)
			}
			if has := strings.Contains(got, ">"+tc.more+"<"); tc.more != "" && !has {
				t.Errorf("Cell(%d colours) lacks the count %q", len(tc.colours), tc.more)
			}
			if tc.more == "" && strings.Contains(got, "goen-tile__more") {
				t.Errorf("Cell(%d colours) counts colours it drew", len(tc.colours))
			}
			if tc.dots > 0 && !strings.Contains(got, `fill="`+tc.colours[0]+`"`) {
				t.Errorf("Cell(%d colours) lost the first colour", len(tc.colours))
			}
			if tc.spoken != "" && !strings.Contains(got, `<span class="goen-sr-only">`+tc.spoken+`</span>`) {
				t.Errorf("Cell(%d colours, sold out %q) does not say %q", len(tc.colours), tc.soldOut, tc.spoken)
			}
			if tc.spokenEn != "" {
				en := tile
				en.ColoursSoldOut = tc.soldOutEn
				if body := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), Cell(en)); !strings.Contains(body, `<span class="goen-sr-only">`+tc.spokenEn+`</span>`) {
					t.Errorf("Cell(%d colours, sold out %q) in English does not say %q", len(tc.colours), tc.soldOutEn, tc.spokenEn)
				}
			}
		})
	}
}

// TestCellSoldOut: a sold-out tile says so without a badge.
func TestCellSoldOut(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	sold := Tile{Slug: "a", Name: "A", PriceCents: 100000}
	got := renderComponent(t, ctx, Cell(sold))
	if !strings.Contains(got, `<span class="goen-tile__state">已售完</span>`) {
		t.Error("a sold-out tile does not say 已售完 beside its price")
	}
	if !strings.Contains(got, "goen-tile--out") {
		t.Error("a sold-out tile does not fade its photograph")
	}
	if strings.Contains(got, "ui-badge") || strings.Contains(got, "goen-tile__flag") {
		t.Error("a sold-out tile draws a badge")
	}

	// Every colour of a sold-out product is sold out; the card has stepped back
	// as a whole, so no dot is struck on its own.
	sold.Colours, sold.ColoursSoldOut = []string{"#111111", "#222222"}, []string{"胡桃", "炭黑"}
	if got = renderComponent(t, ctx, Cell(sold)); strings.Contains(got, "goen-tile__slash") {
		t.Error("a sold-out tile strikes its colours one by one")
	}
	if strings.Contains(got, "種顏色，") {
		t.Error("a sold-out tile names its colours sold out one by one")
	}

	sold.InStock = true
	sold.Colours, sold.ColoursSoldOut = nil, nil
	got = renderComponent(t, ctx, Cell(sold))
	if strings.Contains(got, "已售完") || strings.Contains(got, "goen-tile--out") {
		t.Error("an in-stock tile says sold out")
	}
}

// TestCellWithoutPhoto: a tile without a photo keeps its well and says so.
func TestCellWithoutPhoto(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	got := renderComponent(t, ctx, Cell(Tile{Slug: "a", Name: "A", PriceCents: 100000, InStock: true}))
	if !strings.Contains(got, `<div class="goen-tile__media">`) || !strings.Contains(got, `<span class="goen-tile__ph">沒有照片</span>`) {
		t.Errorf("a tile without a photograph does not say 沒有照片 on its well: %s", got)
	}
}

// TestCellPhotoDecorative: a tile's photo adds nothing to the name its link
// reads.
//
// A tile is one link that reads the brand and the name. Its photo sits inside
// that link, so a description on the photo is read in front of the name it
// repeats, and in Chinese on an English page when the product has no English
// description. The photo is decorative: present, and empty.
func TestCellPhotoDecorative(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			body := renderComponent(t, ctx, Cell(Tile{
				Slug: "pixelight-9", Name: "Pixelight 9 5G", Brand: "Pixelight", PriceCents: 2590000, InStock: true,
				ImageURL: "/img/pixelight-9.webp", ImageAlt: "Pixelight 9 5G 商品照", ImageWidth: 1600, ImageHeight: 1200,
			}))
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			var link, img *html.Node
			for n := range doc.Descendants() {
				switch {
				case n.Type == html.ElementNode && n.Data == "a" && link == nil:
					link = n
				case n.Type == html.ElementNode && n.Data == "img":
					img = n
				}
			}
			if link == nil || img == nil {
				t.Fatalf("the tile has no link or no photo:\n%s", body)
			}
			alt, present := "", false
			for _, a := range img.Attr {
				if a.Key == "alt" {
					alt, present = a.Val, true
				}
			}
			if !present || alt != "" {
				t.Errorf("tile photo alt = %q (present %t), want present and empty", alt, present)
			}
			if !strings.Contains(textOf(link), "Pixelight 9 5G") {
				t.Errorf("the tile link does not carry the product name as text: %q", textOf(link))
			}
			// The brand is not shown on the card, but the link still reads it,
			// before the name.
			if text := textOf(link); !strings.HasPrefix(strings.TrimSpace(text), "Pixelight") || strings.Index(text, "Pixelight") == strings.Index(text, "Pixelight 9 5G") {
				t.Errorf("the tile link does not read the brand before the name: %q", text)
			}
		})
	}
}

func textOf(n *html.Node) string {
	var b strings.Builder
	for d := range n.Descendants() {
		if d.Type == html.TextNode {
			b.WriteString(d.Data)
		}
	}
	return b.String()
}

// TestCellSpecHighlights: a comparable tile draws its spec highlights.
func TestCellSpecHighlights(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	got := renderComponent(t, ctx, Cell(Tile{Slug: "a", Name: "A", Comparable: true, Highlights: []string{"14″", "2.8K OLED"}}))
	if want := `<span class="goen-tile__specs">14″ · 2.8K OLED</span>`; !strings.Contains(got, want) {
		t.Errorf("tile omits %s", want)
	}
}

// TestCellStrikesPriceOnlyOnSale: a card strikes its price only on sale.
func TestCellStrikesPriceOnlyOnSale(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tt := range []struct {
		name       string
		onSale     bool
		wantStruck bool
	}{
		{"on sale", true, true},
		{"not on sale", false, false},
	} {
		tile := Tile{Slug: "a", Name: "A", Brand: "B", PriceCents: 80000, CompareCents: 100000, InStock: true, OnSale: tt.onSale}
		page := renderComponent(t, ctx, Cell(tile))
		if got := strings.Contains(page, `class="goen-tile__was"`); got != tt.wantStruck {
			t.Errorf("Tile %s: struck price drawn = %v, want %v", tt.name, got, tt.wantStruck)
		}
		// The amount takes the campaign's colour only beside a struck price.
		if got := strings.Contains(page, `class="goen-tile__amount goen-tile__amount--sale"`); got != tt.wantStruck {
			t.Errorf("Tile %s: sale amount = %v, want %v", tt.name, got, tt.wantStruck)
		}
		if strings.Contains(page, "原價") != tt.wantStruck {
			t.Errorf("Tile %s: the was-price label and the struck price disagree", tt.name)
		}
		if strings.Contains(page, "goen-tile__source") {
			t.Errorf("Tile %s: a card has no room for a source line", tt.name)
		}
	}
}

// TestCellGridCell: a tile still emits its own grid cell. It holds other Cell
// consumers: extracting Card for the wishlist must not drop the list item the
// grid expects.
func TestCellGridCell(t *testing.T) {
	t.Parallel()

	out := renderComponent(t, i18n.WithLocale(t.Context(), i18n.ZhHant), Cell(Tile{
		Slug: "pixelight-9-pro", Name: "Pixelight 9 Pro", Brand: "Pixelight",
		PriceCents: 3690000, InStock: true, Comparable: true,
	}))
	if strings.Count(out, "<li") != 1 {
		t.Fatalf("Cell rendered %d list items, want 1", strings.Count(out, "<li"))
	}
	if !strings.Contains(out, `class="goen-tile__cell"`) {
		t.Error("Cell lost its grid cell wrapper")
	}
	if !strings.Contains(out, `class="goen-tile__compare"`) {
		t.Error("Cell lost its compare checkbox")
	}
}

// TestCellSaleWithoutChip: a tile says sale by its price and not by a chip.
//
// The struck-through original price already says a product is reduced, so a
// card carries no "on sale" chip.
func TestCellSaleWithoutChip(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	sale := Tile{Slug: "a", Name: "A", Brand: "B", PriceCents: 80000, CompareCents: 100000, InStock: true, OnSale: true}
	page := renderComponent(t, ctx, Cell(sale))
	if strings.Contains(page, i18n.T(ctx, i18n.KeyOnSale)) {
		t.Error("a reduced tile draws an on-sale chip")
	}
	if !strings.Contains(page, `class="goen-tile__was"`) {
		t.Error("a reduced tile lost its struck-through original price")
	}
}

// TestTileColoursCountText: the colour count selects the singular in its
// rendered text.
func TestTileColoursCountText(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, n := range []int{0, 1, 2} {
			ctx := i18n.WithLocale(t.Context(), locale)
			v := Tile{Colours: make([]string, n)}
			want := fmt.Sprintf("%d colours", n)
			if n == 1 {
				want = "1 colour"
			}
			if locale == i18n.ZhHant {
				want = fmt.Sprintf("%d 種顏色", n)
			}
			body := renderComponent(t, ctx, tileColours(v))
			if !strings.Contains(body, ">"+want+"</span>") {
				t.Errorf("colour text (%s, %d) does not contain %q", locale, n, want)
			}
		}
	}
}

// TestCardSaleUntil holds that a card says how long its struck price holds
// only where a page asks it to: 「特價至 11 月 22 日」 in Chinese, "On sale until
// Nov 22" in English with the phrase's comma read and not shown, and the last
// day as a time.
func TestCardSaleUntil(t *testing.T) {
	t.Parallel()
	ends := time.Date(2026, 11, 23, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
	sale := Tile{Slug: "mug", Name: "Mug", PriceCents: 43200, CompareCents: 48000, OnSale: true, InStock: true, SaleEndsAt: ends}
	drawn := sale
	drawn.SaleUntil = shoptime.LastDay(ends, ends)
	for _, tt := range []struct {
		locale   i18n.Locale
		shown    string
		readOnly string
	}{
		{i18n.ZhHant, "特價至 11\u00a0月 22\u00a0日", ""},
		{i18n.En, "On sale until Nov\u00a022", ","},
	} {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		page := renderComponent(t, ctx, Card(drawn))
		start := strings.Index(page, `<span class="goen-tile__until">`)
		if start < 0 {
			t.Errorf("%s: a card asked to say its sale's end does not", tt.locale)
			continue
		}
		line := page[start : start+strings.Index(page[start:], "</span></span>")+len("</span></span>")]
		doc, err := html.Parse(strings.NewReader(line))
		if err != nil {
			t.Fatal(err)
		}
		var shown, read strings.Builder
		for n := range doc.Descendants() {
			if n.Type != html.TextNode {
				continue
			}
			if n.Parent != nil && attr(n.Parent, "class") == "goen-sr-only" {
				read.WriteString(n.Data)
				continue
			}
			shown.WriteString(n.Data)
		}
		if got := shown.String(); got != tt.shown {
			t.Errorf("%s: the card shows %q, want %q", tt.locale, got, tt.shown)
		}
		if got := read.String(); got != tt.readOnly {
			t.Errorf("%s: the card reads %q beyond what it shows, want %q", tt.locale, got, tt.readOnly)
		}
		if !strings.Contains(line, `<time datetime="2026-11-22">`) {
			t.Errorf("%s: the last day is not a time: %s", tt.locale, line)
		}
		for name, tile := range map[string]Tile{"not asked": sale, "not on sale": {Slug: "b", Name: "B", PriceCents: 1, InStock: true, SaleUntil: drawn.SaleUntil}} {
			if got := renderComponent(t, ctx, Card(tile)); strings.Contains(got, "goen-tile__until") {
				t.Errorf("%s: a card %s says its sale's end", tt.locale, name)
			}
		}
	}
}

// TestSaleEndOnlyOnSale holds that a tile carries the end of a sale only for a
// price that is struck.
func TestSaleEndOnlyOnSale(t *testing.T) {
	t.Parallel()
	ends := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	if got := SaleEnd(true, ends); !got.Equal(ends) {
		t.Errorf("SaleEnd(true) = %v, want %v", got, ends)
	}
	if got := SaleEnd(false, ends); !got.IsZero() {
		t.Errorf("SaleEnd(false) = %v, want the zero time", got)
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func renderComponent(t *testing.T, ctx context.Context, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(ctx, &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}
