package pages

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
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/uitest"
)

// The home page's hero tabs, hero arrows, departments and rules row, the deals
// page's running campaigns, the search page's compare button, the cart's
// stepper and filled button, a department's filter chip, the corners every
// pressable control shares, and a product's photograph, measured as drawn.
func TestStorefrontSectionsAsDrawn(t *testing.T) {
	_, port := uitest.Browser(t, "CDP_PORT")
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(assets.Prefix, assets.Handler(slog.New(slog.DiscardHandler)))
	mux.HandleFunc("GET /home", func(w http.ResponseWriter, r *http.Request) {
		view := HomeView{Rules: ShopRules{FreeDeliveryCents: 99900, LowestFeeCents: 6000, PickupOffered: true}}
		for _, title := range []string{"Technology", "Everyday essentials", "Books & stationery"} {
			view.Slides = append(view.Slides, HeroSlide{Title: title, Layout: SlideSplit, Tone: components.ToneStone})
		}
		for _, c := range []struct{ slug, name, photo string }{
			{"audio", "Audio", "nimbus-band-2-01.webp"},
			{"bags", "Bags", "orili-canvas-tote-02.webp"},
			{"coffee", "Coffee", "dawnroast-drip-coffee-01.webp"},
		} {
			view.Categories = append(view.Categories, HomeCategory{
				Slug: c.slug, Name: c.name, Tone: components.ToneStone,
				Photo: components.Photo{URL: assets.Image{BundledKey: c.photo}.URL()},
			})
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := Home(layouts.Page{}, view).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render home fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /deals", func(w http.ResponseWriter, r *http.Request) {
		view := SearchView{Campaigns: CampaignPage{Page: 1, Total: 2, PageSize: 12, Rows: []CampaignSummary{
			{Slug: "autumn", Title: "Autumn audio", Products: 4, EndsOn: "31 Oct"},
			{Slug: "desk", Title: "Desk week", Products: 6},
		}}}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := Deals(layouts.Page{}, view).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render deals fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		view := SearchView{Query: "tote", Total: 2, Page: 1, PageSize: 20, Products: []ProductTile{
			{Slug: "canvas-tote", Name: "Canvas tote", PriceCents: 89000, InStock: true, Comparable: true},
			{Slug: "city-tote", Name: "City tote", PriceCents: 129000, InStock: true, Comparable: true},
		}}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := Search(layouts.Page{}, view).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render search fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /cart", func(w http.ResponseWriter, r *http.Request) {
		view := CartView{ItemCount: 1, SubtotalCents: 89000, Lines: []CartLine{
			{VariantID: "tote-natural", Slug: "canvas-tote", Name: "Canvas tote", Quantity: 1, Available: 5, UnitCents: 89000},
		}}
		ctx := layouts.WithStaff(i18n.WithLocale(r.Context(), i18n.En), true)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := Cart(layouts.Page{}, view).Render(ctx, w); renderErr != nil {
			t.Errorf("render cart fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /c/audio", func(w http.ResponseWriter, r *http.Request) {
		view := ListingView{Slug: "audio", Name: "Audio", Filtered: true, InStockOnly: true, Query: "in_stock=1"}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := Listing(layouts.Page{}, view, nil, nil).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render listing fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /product", func(w http.ResponseWriter, r *http.Request) {
		tones := map[string]components.Tone{"ink": components.ToneInk, "sage": components.ToneSage}
		view := &ProductView{
			Name: "Restwood Mug", Brand: "Restwood", Slug: "restwood-mug", Description: "A stoneware mug.",
			SelectionOK: true, Exact: true, Sellable: true, AnySellable: true, PriceCents: 43200,
			Tone: tones[r.URL.Query().Get("tone")],
		}
		if r.URL.Query().Get("photo") == "1" {
			view.Images = []ProductImage{{
				URL: assets.Image{BundledKey: "nimbus-band-2-01.webp"}.URL(), Alt: "A mug", Width: 1600, Height: 1200,
			}}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := Product(ProductMeta(view), view).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render product fixture: %v", renderErr)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	//nolint:gosec // G204: fixed regression script, test-owned server and caller's Chrome debugging port.
	cmd := exec.CommandContext(t.Context(), "node", filepath.Join(root, "scripts", "storefront-sections-check.mjs"), server.URL, port)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("storefront sections: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}
