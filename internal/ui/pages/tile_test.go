package pages

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestTileColourDots(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	base := ProductTile{Slug: "a", Name: "A", PriceCents: 100000, InStock: true}
	hexes := []string{"#111111", "#222222", "#333333", "#444444", "#555555", "#666666", "#777777", "#888888", "#999999"}

	tests := []struct {
		name    string
		colours []string
		dots    int
		more    string
	}{
		{"none", nil, 0, ""},
		{"one colour is not a choice", hexes[:1], 0, ""},
		{"two", hexes[:2], 2, ""},
		{"four", hexes[:4], 4, ""},
		{"nine", hexes, 4, "+5"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tile := base
			tile.Colours = tc.colours
			got := renderComponent(t, ctx, Tile(tile))
			if n := strings.Count(got, `class="goen-tile__dot"`); n != tc.dots {
				t.Errorf("Tile(%d colours) drew %d dots, want %d", len(tc.colours), n, tc.dots)
			}
			if has := strings.Contains(got, "goen-tile__colours"); has != (tc.dots > 0) {
				t.Errorf("Tile(%d colours) colour list present = %v, want %v", len(tc.colours), has, tc.dots > 0)
			}
			if has := strings.Contains(got, ">"+tc.more+"<"); tc.more != "" && !has {
				t.Errorf("Tile(%d colours) lacks the count %q", len(tc.colours), tc.more)
			}
			if tc.more == "" && strings.Contains(got, "goen-tile__more") {
				t.Errorf("Tile(%d colours) counts colours it drew", len(tc.colours))
			}
			if tc.dots > 0 && !strings.Contains(got, `fill="`+tc.colours[0]+`"`) {
				t.Errorf("Tile(%d colours) lost the first colour", len(tc.colours))
			}
		})
	}
}

func TestSoldOutTileSaysSoWithoutABadge(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	sold := ProductTile{Slug: "a", Name: "A", PriceCents: 100000}
	got := renderComponent(t, ctx, Tile(sold))
	if !strings.Contains(got, `<span class="goen-tile__state">已售完</span>`) {
		t.Error("a sold-out tile does not say 已售完 beside its price")
	}
	if !strings.Contains(got, "goen-tile--out") {
		t.Error("a sold-out tile does not fade its photograph")
	}
	if strings.Contains(got, "ui-badge") || strings.Contains(got, "goen-tile__flag") {
		t.Error("a sold-out tile draws a badge")
	}

	sold.InStock = true
	got = renderComponent(t, ctx, Tile(sold))
	if strings.Contains(got, "已售完") || strings.Contains(got, "goen-tile--out") {
		t.Error("an in-stock tile says sold out")
	}
}

func TestTileWithoutPhotoKeepsItsWellAndSaysSo(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	got := renderComponent(t, ctx, Tile(ProductTile{Slug: "a", Name: "A", PriceCents: 100000, InStock: true}))
	if !strings.Contains(got, `<div class="goen-tile__media">`) || !strings.Contains(got, `<span class="goen-tile__ph">沒有照片</span>`) {
		t.Errorf("a tile without a photograph does not say 沒有照片 on its well: %s", got)
	}
}

// A tile is one link that reads the brand and the name. Its photo sits inside
// that link, so a description on the photo is read in front of the name it
// repeats, and in Chinese on an English page when the product has no English
// description. The photo is decorative: present, and empty.
func TestATilePhotoAddsNothingToTheNameItsLinkReads(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			body := renderComponent(t, ctx, Tile(ProductTile{
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

func TestAComparableTileDrawsItsSpecHighlights(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	got := renderComponent(t, ctx, Tile(ProductTile{Slug: "a", Name: "A", Comparable: true, Highlights: []string{"14″", "2.8K OLED"}}))
	if want := `<span class="goen-tile__specs">14″ · 2.8K OLED</span>`; !strings.Contains(got, want) {
		t.Errorf("tile omits %s", want)
	}
}

func TestACardStrikesItsPriceOnlyOnSale(t *testing.T) {
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
		tile := ProductTile{Slug: "a", Name: "A", Brand: "B", PriceCents: 80000, CompareCents: 100000, InStock: true, OnSale: tt.onSale}
		page := renderComponent(t, ctx, Tile(tile))
		if got := strings.Contains(page, `class="goen-tile__was"`); got != tt.wantStruck {
			t.Errorf("Tile %s: struck price drawn = %v, want %v", tt.name, got, tt.wantStruck)
		}
		if strings.Contains(page, "原價") != tt.wantStruck {
			t.Errorf("Tile %s: the was-price label and the struck price disagree", tt.name)
		}
		if strings.Contains(page, "goen-tile__source") {
			t.Errorf("Tile %s: a card has no room for a source line", tt.name)
		}
	}
}
