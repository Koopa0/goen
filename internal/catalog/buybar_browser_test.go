package catalog

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/policy"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/uitest"
)

// buyBarFixture is a product on sale with two option groups, both picked, so
// the page has a price, a struck original, chosen options and a live add button.
func buyBarFixture() *ProductView {
	photo := func(key, alt string) ProductImage {
		return ProductImage{URL: picture.Image{BundledKey: key}.URL(), Alt: alt}
	}
	return &ProductView{
		Slug: "meridian-watch-s3", Name: "Meridian Watch S3", Brand: "Meridian", Summary: "全天候健康量測智慧手錶。",
		Images: []ProductImage{
			photo("meridian-watch-s3-03.webp", "Rose gold"), photo("meridian-watch-s3-02.webp", "Silver"),
			photo("meridian-watch-s3-01.webp", "Product"),
		},
		Options: []ProductOption{
			{Name: "colour", Label: "顏色", Values: []ProductOptionValue{
				{Value: "silver", Label: "銀", Available: true, Href: "/p/meridian-watch-s3?c=silver", SwatchHex: "#c9ccce"},
				{Value: "rose", Label: "玫瑰金", Selected: true, Available: true, Href: "/p/meridian-watch-s3?c=rose", SwatchHex: "#e0bfb8"},
			}},
			{Name: "size", Label: "尺寸", Values: []ProductOptionValue{
				{Value: "41", Label: "41mm", Available: true, Href: "/p/meridian-watch-s3?s=41"},
				{Value: "45", Label: "45mm", Selected: true, Available: true, Href: "/p/meridian-watch-s3?s=45"},
			}},
		},
		Specs:       []ProductSpec{{Label: "螢幕", Value: "AMOLED"}, {Label: "續航", Value: "48 小時"}},
		Rules:       policy.Rules{FreeDeliveryCents: 300000, LowestFeeCents: 6000, PickupOffered: true, RescissionDays: 7, ReturnDays: 14, PayStartMinutes: 29},
		SelectionOK: true, Exact: true, Sellable: true, AnySellable: true, VariantID: "v", Available: 5,
		PriceCents: 1190000, CompareCents: 1290000, OnSale: true, WarrantyMonths: 24,
		Rating: 4.5, RatingCount: 2,
	}
}

// TestProductBuyBarMoments holds the mobile buy bar's four moments on the real
// product page markup, stylesheet and script: on load with the buy box's price
// on screen it shows the chosen options and the button but no price; on load
// at 320x640, with that price off screen, it shows the price, the struck
// original outside .goen-buybar__price and the options; it hides while the add
// button is on screen; and past the button it shows the price again. It also
// steps aside for a field being typed in.
func TestProductBuyBarMoments(t *testing.T) {
	_, port := uitest.Browser(t, "CDP_PORT")
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(assets.Prefix, assets.Handler(slog.New(slog.DiscardHandler)))
	mux.HandleFunc("GET /p/meridian-watch-s3", func(w http.ResponseWriter, r *http.Request) {
		ctx := i18n.WithLocale(r.Context(), i18n.ZhHant)
		if r.URL.Query().Get("lang") == "en" {
			ctx = i18n.WithLocale(r.Context(), i18n.En)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := Product(frame.Page{}, buyBarFixture()).Render(ctx, w); renderErr != nil {
			t.Errorf("render buy bar fixture: %v", renderErr)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	//nolint:gosec // G204: fixed regression script, test-owned server, caller's Chrome debugging port.
	cmd := exec.CommandContext(t.Context(), "node", filepath.Join(root, "scripts", "buybar-check.mjs"), server.URL, port)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("buy bar moments: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

// longNames are the seed's longest product names in each script, with the
// model numbers and word lengths a line cannot break inside on its own.
var longNames = []string{
	"Aurora 行動電源 10000mAh",
	"Pixelight 9 Pro 保護殼",
	"紙芯 中性筆 0.5 五色組",
	`Meridian 筆電內袋 14"`,
	"Koto 編織 USB-C 線 2m",
	"Restwood Reed Diffuser, White Tea",
	"Paperwick 0.5 Gel Pens, Set of 5",
	"Kumo Leaf High Mountain Oolong",
	"Aurora Power Bank 10000mAh",
	"Meridian Book 16 Pro",
}

// TestProductPageReflow holds that the product page reflows with the seed's
// longest names: in the title and the breadcrumb, beside long option values,
// and on the related products' cards, at 320 to 1440 wide, in Chinese and
// English, at 100% and 200% text, nothing widens the page or runs out of its
// box. The 40px name is in rem, so at 200% text a model number such as
// "10000mAh" is wider than a phone's line unless the name may break inside it.
func TestProductPageReflow(t *testing.T) {
	_, port := uitest.Browser(t, "CDP_PORT")
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	view := func(name string) *ProductView {
		v := buyBarFixture()
		v.Name = name
		v.Options[0].Values[1].Label = "Midnight Graphite Aluminium with Sport Loop"
		v.Options[1].Values[1].Label = "45mm GPS plus Cellular Titanium"
		for i, related := range longNames[:4] {
			v.Related = append(v.Related, tile.Tile{
				Slug: fmt.Sprintf("related-%d", i), Name: related, Brand: "Aurora",
				PriceCents: 129000, CompareCents: 159000, OnSale: true, InStock: true,
			})
		}
		return v
	}
	mux := http.NewServeMux()
	mux.Handle(assets.Prefix, assets.Handler(slog.New(slog.DiscardHandler)))
	mux.HandleFunc("GET /p/{index}", func(w http.ResponseWriter, r *http.Request) {
		index, convErr := strconv.Atoi(r.PathValue("index"))
		if convErr != nil || index < 0 || index >= len(longNames) {
			http.NotFound(w, r)
			return
		}
		ctx := i18n.WithLocale(r.Context(), i18n.ZhHant)
		if r.URL.Query().Get("lang") == "en" {
			ctx = i18n.WithLocale(r.Context(), i18n.En)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		v := view(longNames[index])
		if renderErr := Product(ProductMeta(v), v).Render(ctx, w); renderErr != nil {
			t.Errorf("render reflow fixture %d: %v", index, renderErr)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	args := make([]string, 0, 3+len(longNames))
	args = append(args, filepath.Join(root, "scripts", "product-reflow-check.mjs"), server.URL, port)
	for i := range longNames {
		args = append(args, strconv.Itoa(i))
	}
	//nolint:gosec // G204: fixed regression script, test-owned server, caller's Chrome debugging port.
	cmd := exec.CommandContext(t.Context(), "node", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("product page reflow: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}
