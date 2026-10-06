package pages

import (
	"strings"
	"testing"

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
