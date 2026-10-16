package pages_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/uitest"
)

// The home page's hero tabs, hero arrows, departments and rules row, the deals
// page's running campaigns, the search page's compare button, the cart's
// stepper and filled button, a department's filter chip, the corners every
// pressable control shares, a product's photograph, the header's search pill
// with the rule under the header, a department's head and the home page's shelf
// as panels in the page column, and the one way a chosen option, a chosen
// delivery card and a refused field are marked, measured as drawn.
func TestStorefrontSectionsAsDrawn(t *testing.T) {
	_, port := uitest.Browser(t, "CDP_PORT")
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(assets.Prefix, assets.Handler(slog.New(slog.DiscardHandler)))
	tones := map[string]components.Tone{"ink": components.ToneInk, "sage": components.ToneSage}
	mux.HandleFunc("GET /home", func(w http.ResponseWriter, r *http.Request) {
		view := pages.HomeView{Rules: pages.ShopRules{FreeDeliveryCents: 99900, LowestFeeCents: 6000, PickupOffered: true}}
		photo := components.Photo{URL: assets.Image{BundledKey: "nimbus-band-2-01.webp"}.URL(), Alt: "Headphones on a desk"}
		view.Slides = []components.HeroSlide{
			{Title: "Technology", Layout: components.SlideSplit, Tone: components.ToneStone, Photo: photo},
			{Title: "Everyday essentials", Layout: components.SlideSplit, Tone: components.ToneInk, Photo: photo, CTA: components.CTA{Label: "Shop now", Href: "/c/audio"}},
			{Title: "Books & stationery", Layout: components.SlidePhoto, Tone: components.ToneSage, Photo: photo},
			{Title: "Slow mornings", Layout: components.SlideSplit, Tone: components.ToneSage, CTA: components.CTA{Label: "See the range", Href: "/c/coffee"}},
			{Title: "After dark", Layout: components.SlideSplit, Tone: components.ToneInk},
		}
		for _, c := range []struct{ slug, name, photo string }{
			{"audio", "Audio", "nimbus-band-2-01.webp"},
			{"bags", "Bags", "orili-canvas-tote-02.webp"},
			{"coffee", "Coffee", "dawnroast-drip-coffee-01.webp"},
		} {
			view.Categories = append(view.Categories, pages.HomeCategory{
				Slug: c.slug, Name: c.name, Tone: components.ToneStone,
				Photo: components.Photo{URL: assets.Image{BundledKey: c.photo}.URL()},
			})
		}
		if tone, ok := tones[r.URL.Query().Get("band")]; ok {
			view.Band = &pages.DepartmentBand{Name: "Technology", Items: 12, Fact: "Audio · Phones", Href: "/c/tech", Tone: tone}
			for _, name := range []string{"Canvas tote", "City tote", "Weekend tote", "Market tote"} {
				view.Band.Tiles = append(view.Band.Tiles, pages.ProductTile{Slug: strings.ToLower(strings.ReplaceAll(name, " ", "-")), Name: name, PriceCents: 89000, InStock: true})
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := pages.Home(layouts.Page{}, view).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render home fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /deals", func(w http.ResponseWriter, r *http.Request) {
		view := pages.SearchView{Campaigns: pages.CampaignPage{Page: 1, Total: 2, PageSize: 12, Rows: []pages.CampaignSummary{
			{Slug: "autumn", Title: "Autumn audio", Products: 4, EndsOn: "31 Oct"},
			{Slug: "desk", Title: "Desk week", Products: 6},
		}}}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := pages.Deals(layouts.Page{}, view).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render deals fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		view := pages.SearchView{Query: "tote", Total: 2, Page: 1, PageSize: 20, Products: []pages.ProductTile{
			{Slug: "canvas-tote", Name: "Canvas tote", PriceCents: 89000, InStock: true, Comparable: true},
			{Slug: "city-tote", Name: "City tote", PriceCents: 129000, InStock: true, Comparable: true},
		}}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := pages.Search(layouts.Page{}, view).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render search fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /cart", func(w http.ResponseWriter, r *http.Request) {
		view := cart.CartView{ItemCount: 1, SubtotalCents: 89000, Lines: []pages.CartLine{
			{VariantID: "tote-natural", Slug: "canvas-tote", Name: "Canvas tote", Quantity: 1, Available: 5, UnitCents: 89000},
		}}
		ctx := layouts.WithStaff(i18n.WithLocale(r.Context(), i18n.En), true)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := cart.Cart(layouts.Page{}, view).Render(ctx, w); renderErr != nil {
			t.Errorf("render cart fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /c/audio", func(w http.ResponseWriter, r *http.Request) {
		view := pages.ListingView{Slug: "audio", Name: "Audio", Filtered: true, InStockOnly: true, Query: "in_stock=1"}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := pages.Listing(layouts.Page{}, view, nil, nil).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render listing fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /department", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		locale, names := i18n.En, []string{"Phones", "Laptops", "Tablets", "Audio", "Wearables", "Accessories"}
		if query.Get("zh") == "1" {
			locale, names = i18n.ZhHant, []string{"手機", "筆電", "平板", "耳機與音響", "穿戴裝置", "周邊配件"}
		}
		theme := &pages.Theme{Tone: tones[query.Get("tone")], Children: []pages.Crumb{
			{Slug: "phones", Name: names[0]}, {Slug: "laptops", Name: names[1]}, {Slug: "tablets", Name: names[2]},
			{Slug: "audio", Name: names[3]}, {Slug: "wearables", Name: names[4]}, {Slug: "accessories", Name: names[5]},
		}}
		if query.Get("photo") == "1" {
			theme.Photo = components.Photo{URL: assets.Image{BundledKey: "nimbus-band-2-01.webp"}.URL(), Alt: "Headphones on a desk"}
		}
		var head *pages.DepartmentHead
		if query.Get("notice") == "1" {
			now := time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)
			head = &pages.DepartmentHead{Notice: &pages.DepartmentNotice{
				Title: "Autumn audio", Href: "/s/autumn", End: pages.NewCampaignEnd(time.Date(2026, 10, 30, 16, 0, 0, 0, time.UTC), now),
			}}
		}
		view := pages.ListingView{Slug: "phones", Name: names[0], Theme: theme}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := pages.Listing(layouts.Page{}, view, nil, head).Render(i18n.WithLocale(r.Context(), locale), w); renderErr != nil {
			t.Errorf("render department fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /product", func(w http.ResponseWriter, r *http.Request) {
		view := &pages.ProductView{
			Name: "Restwood Mug", Brand: "Restwood", Slug: "restwood-mug", Description: "A stoneware mug.",
			SelectionOK: true, Exact: true, Sellable: true, AnySellable: true, PriceCents: 43200,
			Tone: tones[r.URL.Query().Get("tone")],
		}
		if chosen := r.URL.Query().Get("options"); chosen != "" {
			view.Options = []pages.ProductOption{{Name: "storage", Label: "Storage", Values: []pages.ProductOptionValue{
				{Value: "64", Label: "64 GB", Available: true, Href: "/product?options=" + chosen},
				{Value: "128", Label: "128 GB", Selected: true, Available: chosen == "in", Href: "/product?options=" + chosen},
				{Value: "256", Label: "256 GB", Href: "/product?options=" + chosen},
			}}}
		}
		if r.URL.Query().Get("photo") == "1" {
			view.Images = []pages.ProductImage{{
				URL: assets.Image{BundledKey: "nimbus-band-2-01.webp"}.URL(), Alt: "A mug", Width: 1600, Height: 1200,
			}}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := pages.Product(pages.ProductMeta(view), view).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render product fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /checkout", func(w http.ResponseWriter, r *http.Request) {
		view := cart.CheckoutView{
			Cart:     cart.CartView{ItemCount: 1, SubtotalCents: 89000, Lines: []pages.CartLine{{VariantID: "tote-natural", Slug: "canvas-tote", Name: "Canvas tote", Quantity: 1, UnitCents: 89000}}},
			Shipping: []cart.ShippingChoice{{VersionID: "home", Name: "Home delivery", Carrier: "Courier", FeeCents: 8000}, {VersionID: "store", Name: "Convenience store", FeeCents: 6000}},
			Chosen:   "home",
			SavedAddresses: []cart.SavedAddress{
				{ID: "home-address", Label: "Home", Name: "Mei Lin", Phone: "0912345678", PostalCode: "106", City: "Taipei", District: "Da'an", Street: "1 Example Road"},
			},
			ChosenAddress: "home-address",
			Errors:        map[string]string{"email": "Enter an email address like name@example.com."},
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ctx := i18n.WithLocale(r.Context(), i18n.En)
		if renderErr := cart.Checkout(cart.CheckoutMeta(ctx), &view).Render(ctx, w); renderErr != nil {
			t.Errorf("render checkout fixture: %v", renderErr)
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
