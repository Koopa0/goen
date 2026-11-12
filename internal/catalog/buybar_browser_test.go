package catalog

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/policy"
	"github.com/koopa0/goen/internal/ui/frame"
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
