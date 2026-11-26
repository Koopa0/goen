package home_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/policy"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/tone"
	"github.com/koopa0/goen/internal/ui/uitest"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/wishlist"
)

// The home page's bento hero, its shelf with the pills that swap it, its dark
// stage, its rail under the free-delivery meter, its other departments and its
// rule cards, the product card's plate, colours and quick add; the department
// index, the deals page's running campaigns, the search page's compare button,
// the cart's stepper and filled button, a department's filter chip, the corners
// every pressable control shares, a product's photograph, the header's search
// pill with the rule under the header, a department's head as words in the page
// column, the one way a chosen option, a chosen delivery card and a refused
// field are marked, the page frame's header, department panel and footer, and
// the ways on from an empty cart, an empty wishlist and a missing page with the
// checkout's one solid button, measured as drawn.
func TestStorefrontSectionsAsDrawn(t *testing.T) {
	_, port := uitest.Browser(t, "CDP_PORT")
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(assets.Prefix, assets.Handler(slog.New(slog.DiscardHandler)))
	tones := map[string]tone.Tone{"ink": tone.Ink, "sage": tone.Sage, "stone": tone.Stone}
	// The page at /home, and at / where its pills lead.
	homeFixture := func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		now := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
		// ?zh=1 draws the page in Chinese.
		locale, names := i18n.En, []string{"Tech", "Phones", "Audio", "Home & living", "Books"}
		if query.Get("zh") == "1" {
			locale, names = i18n.ZhHant, []string{"3C 數位", "手機", "耳機與音響", "居家生活", "書籍文具"}
		}
		photo := func(key string) (string, string) {
			image := picture.Image{BundledKey: key}
			return image.URL(), image.Srcset()
		}
		product := func(slug, name, key string, cents int64) tile.Tile {
			url, srcset := photo(key)
			return tile.Tile{Slug: slug, Name: name, Brand: "Koto", PriceCents: cents, InStock: true,
				ImageURL: url, ImageSrcset: srcset, ImageWidth: 1600, ImageHeight: 1200,
				Highlights: []string{"40mm", "30 h"}, WarrantyMonths: 12, Rating: 4.5, RatingCount: 2}
		}
		// The first is on sale until 22 November; the second offers two colours,
		// each with its own photograph; the third adds without a choice.
		budsWhite, budsWhiteSet := photo("nimbus-buds-pro-01.webp")
		budsBlack, budsBlackSet := photo("nimbus-buds-pro-02.webp")
		shelf := []tile.Tile{
			product("koto-over-ear", "Koto Over-Ear", "koto-over-ear-01.webp", 990000),
			product("nimbus-buds-pro", "Nimbus Buds Pro", "nimbus-buds-pro-01.webp", 590000),
			product("koto-cable", "Koto Cable", "koto-cable-braided-01.webp", 49000),
			product("nimbus-band-2", "Nimbus Band 2", "nimbus-band-2-01.webp", 190000),
		}
		shelf[0].CompareCents, shelf[0].OnSale = 1090000, true
		shelf[0].SaleUntil = shoptime.LastDay(time.Date(2026, 11, 22, 16, 0, 0, 0, time.UTC), now)
		shelf[1].Colours, shelf[1].ColourNames = []string{"#f4f5f6", "#1c1c1e"}, []string{"White", "Black"}
		shelf[1].ColourPhotos = []tile.ColourPhoto{{URL: budsWhite, Srcset: budsWhiteSet}, {URL: budsBlack, Srcset: budsBlackSet}}
		shelf[2].QuickAdd = "cable-variant"
		// ?shelf=audio is the shelf's second pill, chosen: its own products.
		chosen := ""
		if query.Get("shelf") == "audio" {
			chosen = "audio"
			shelf = []tile.Tile{product("koto-speaker", "Koto Speaker", "nimbus-speaker-s1-01.webp", 249000)}
		}
		tech := home.Category{Slug: "tech", Name: names[0], Products: 20}
		view := home.View{
			Products:  40,
			Featured:  &tech,
			Lifestyle: &hero.Slide{Source: hero.SourceDepartment, Eyebrow: "4", Title: names[3], Fact: "Kitchen · Fragrance", CTA: hero.CTA{Label: names[3], Href: "/c/home-living"}},
			Shelf: &home.Shelf{Department: tech, Chosen: chosen, Tiles: shelf, Filters: []home.Category{
				{Slug: "phones", Name: names[1], Products: 3}, {Slug: "audio", Name: names[2], Products: 3},
			}},
			Stage: []tile.Tile{
				product("meridian-book-16-pro", "Meridian Book 16 Pro", "meridian-book-16-pro-01.webp", 6890000),
				product("pixelight-9-pro", "Pixelight 9 Pro", "pixelight-9-pro-01.webp", 3390000),
			},
			Rail:   home.Rail{BelowCents: 100000},
			Choice: home.Choice{Shelf: chosen},
			Rules:  policy.Rules{FreeDeliveryCents: 300000, LowestFeeCents: 6000, PickupOffered: true, RescissionDays: 7, ReturnDays: 14, PayStartMinutes: 29},
		}
		view.Stage[0].QuickAdd = "book-variant"
		for i, key := range []string{"paperwick-gel-pen-set-01.webp", "paperwick-dot-notebook-a5-01.webp", "kumoleaf-black-tea-bags-01.webp", "dawnroast-drip-coffee-01.webp", "saelo-hand-cream-01.webp", "fernway-mountain-tea-seasons-01.webp"} {
			view.Rail.Tiles = append(view.Rail.Tiles, product(strings.TrimSuffix(key, "-01.webp"), "Everyday "+strings.Repeat("piece ", i%2+1), key, int64(15000+i*5000)))
		}
		moreURL, moreSrcset := photo("department-home-living.webp")
		view.More = &home.More{
			Tabs:   []home.Category{{Slug: "home-living", Name: names[3], Products: 4}, {Slug: "books", Name: names[4], Products: 4}},
			Chosen: home.Category{Slug: "home-living", Name: names[3], Products: 4},
			Photo:  widget.Photo{URL: moreURL, Srcset: moreSrcset},
			Subs:   []string{"Kitchen", "Fragrance"},
			Tiles:  view.Rail.Tiles[:4],
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// A cart of two short of free delivery, so the meter and the header's
		// rail have something to say.
		basket := frame.CartSummary{Units: 2, SubtotalCents: 190700, Delivery: frame.CartDelivery{ShortCents: 109300}}
		ctx := frame.WithCart(frame.WithFreeDeliveryOver(i18n.WithLocale(r.Context(), locale), 300000), basket)
		// ?promo=1 puts the strip above the header, with its code and its word.
		if query.Get("promo") == "1" {
			ctx = frame.WithBanner(ctx, frame.Banner{
				ID: "autumn", Message: "Free delivery on orders over NT$999 this week", MessageShort: "Free delivery",
				Code: "AUTUMN", CTALabel: "Shop now", CTAHref: "/s/tea",
			})
		}
		if renderErr := home.Page(frame.Page{}, view).Render(ctx, w); renderErr != nil {
			t.Errorf("render home fixture: %v", renderErr)
		}
	}
	mux.HandleFunc("GET /home", homeFixture)
	mux.HandleFunc("GET /{$}", homeFixture)
	// The department index the missing page and the empty search draw, under a
	// heading in the page column.
	mux.HandleFunc("GET /departments", func(w http.ResponseWriter, r *http.Request) {
		departments := make([]frame.NavItem, 0, 3)
		for _, c := range []struct{ slug, name, photo string }{
			{"audio", "Audio", "nimbus-band-2-01.webp"},
			{"bags", "Bags", "orili-canvas-tote-02.webp"},
			{"coffee", "Coffee", "dawnroast-drip-coffee-01.webp"},
		} {
			departments = append(departments, frame.NavItem{
				Slug: c.slug, Name: c.name, Href: "/c/" + c.slug, Tone: tone.Stone,
				PhotoURL: picture.Image{BundledKey: c.photo}.URL(),
			})
		}
		// ?depts=mark leaves the second department without a photograph, and gives
		// the first its sub-categories.
		if r.URL.Query().Get("depts") == "mark" {
			departments[1].PhotoURL = ""
			departments[0].Children = []frame.NavItem{{Name: "Wearables", Href: "/c/wearables"}, {Name: "Speakers", Href: "/c/speakers"}, {Name: "Cables and chargers", Href: "/c/cables"}}
		}
		ctx := frame.WithTopNav(i18n.WithLocale(r.Context(), i18n.En), departments)
		index := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			if _, writeErr := io.WriteString(w, `<div class="goen-home"><section aria-labelledby="cats-heading"><h2 id="cats-heading" class="goen-home__heading">Departments</h2>`); writeErr != nil {
				return writeErr
			}
			if indexErr := frame.DepartmentIndex().Render(ctx, w); indexErr != nil {
				return indexErr
			}
			_, writeErr := io.WriteString(w, `</section></div>`)
			return writeErr
		})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := frame.Base(frame.Page{Title: "Departments"}).Render(templ.WithChildren(ctx, index), w); renderErr != nil {
			t.Errorf("render department index fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /deals", func(w http.ResponseWriter, r *http.Request) {
		now := time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)
		view := campaign.DealsView{Campaigns: campaign.Listed{Page: 1, Total: 2, PageSize: 12, Rows: []campaign.Summary{
			{Slug: "autumn", Title: "Autumn audio", Products: 4, End: campaign.NewEnd(time.Date(2026, 10, 30, 16, 0, 0, 0, time.UTC), now)},
			{Slug: "desk", Title: "Desk week", Products: 6, End: campaign.NewEnd(time.Date(2026, 11, 20, 16, 0, 0, 0, time.UTC), now)},
		}}}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := campaign.Deals(frame.Page{}, view).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render deals fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		view := catalog.SearchView{Query: "tote", Total: 2, Page: 1, PageSize: 20, Products: []tile.Tile{
			{Slug: "canvas-tote", Name: "Canvas tote", PriceCents: 89000, InStock: true, Comparable: true},
			{Slug: "city-tote", Name: "City tote", PriceCents: 129000, InStock: true, Comparable: true},
		}}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := catalog.Search(frame.Page{}, view).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render search fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /cart", func(w http.ResponseWriter, r *http.Request) {
		view := cart.View{ItemCount: 1, SubtotalCents: 89000, Lines: []cart.Line{
			{VariantID: "tote-natural", Slug: "canvas-tote", Name: "Canvas tote", Quantity: 1, Available: 5, UnitCents: 89000},
		}}
		ctx := frame.WithStaff(i18n.WithLocale(r.Context(), i18n.En), true)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := cart.Page(frame.Page{}, view).Render(ctx, w); renderErr != nil {
			t.Errorf("render cart fixture: %v", renderErr)
		}
	})
	// The pages with nothing to show: an empty cart for a customer with saved
	// products, an empty wishlist and a missing page, each with its ways on.
	mux.HandleFunc("GET /cart/empty", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := cart.Page(frame.Page{}, cart.View{SavedProducts: true}).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render empty cart fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /wishlist", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := wishlist.Page(frame.Page{}, wishlist.View{}).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render wishlist fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /missing", func(w http.ResponseWriter, r *http.Request) {
		photo := picture.Image{BundledKey: "nimbus-band-2-01.webp"}
		ctx := frame.WithTopNav(i18n.WithLocale(r.Context(), i18n.En), []frame.NavItem{
			{Slug: "tech", Name: "Tech", Href: "/c/tech", PhotoURL: photo.URL(), PhotoSrcset: photo.Srcset(),
				Children: []frame.NavItem{{Slug: "phones", Name: "Phones", Href: "/c/phones"}}},
		})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		if renderErr := frame.Notice(frame.Page{}, "404", "Page not found", "Nothing lives at this address.").Render(ctx, w); renderErr != nil {
			t.Errorf("render missing page fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /c/audio", func(w http.ResponseWriter, r *http.Request) {
		view := catalog.ListingView{Slug: "audio", Name: "Audio", Filtered: true, InStockOnly: true, Query: "in_stock=1"}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := catalog.Listing(frame.Page{}, view, nil, nil).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render listing fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /department", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		locale, names := i18n.En, []string{"Phones", "Laptops", "Tablets", "Audio", "Wearables", "Accessories"}
		if query.Get("zh") == "1" {
			locale, names = i18n.ZhHant, []string{"手機", "筆電", "平板", "耳機與音響", "穿戴裝置", "周邊配件"}
		}
		theme := &catalog.Theme{Tone: tones[query.Get("tone")], Children: []catalog.Crumb{
			{Slug: "phones", Name: names[0]}, {Slug: "laptops", Name: names[1]}, {Slug: "tablets", Name: names[2]},
			{Slug: "audio", Name: names[3]}, {Slug: "wearables", Name: names[4]}, {Slug: "accessories", Name: names[5]},
		}}
		if query.Get("photo") == "1" {
			theme.Photo = widget.Photo{URL: picture.Image{BundledKey: "nimbus-band-2-01.webp"}.URL(), Alt: "Headphones on a desk"}
		}
		var head *catalog.DepartmentHead
		if query.Get("notice") == "1" {
			now := time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)
			head = &catalog.DepartmentHead{Notice: &catalog.DepartmentNotice{
				Title: "Autumn audio", Href: "/s/autumn", End: campaign.NewEnd(time.Date(2026, 10, 30, 16, 0, 0, 0, time.UTC), now),
			}}
		}
		view := catalog.ListingView{Slug: "phones", Name: names[0], Theme: theme}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := catalog.Listing(frame.Page{}, view, nil, head).Render(i18n.WithLocale(r.Context(), locale), w); renderErr != nil {
			t.Errorf("render department fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /product", func(w http.ResponseWriter, r *http.Request) {
		view := &catalog.ProductView{
			Name: "Restwood Mug", Brand: "Restwood", Slug: "restwood-mug", Description: "A stoneware mug.",
			SelectionOK: true, Exact: true, Sellable: true, AnySellable: true, PriceCents: 43200,
		}
		if chosen := r.URL.Query().Get("options"); chosen != "" {
			view.Options = []catalog.ProductOption{{Name: "storage", Label: "Storage", Values: []catalog.ProductOptionValue{
				{Value: "64", Label: "64 GB", Available: true, Href: "/product?options=" + chosen},
				{Value: "128", Label: "128 GB", Selected: true, Available: chosen == "in", Href: "/product?options=" + chosen},
				{Value: "256", Label: "256 GB", Href: "/product?options=" + chosen},
			}}}
		}
		if r.URL.Query().Get("photo") == "1" {
			view.Images = []catalog.ProductImage{{
				URL: picture.Image{BundledKey: "nimbus-band-2-01.webp"}.URL(), Alt: "A mug", Width: 1600, Height: 1200,
			}}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := catalog.Product(catalog.ProductMeta(view), view).Render(i18n.WithLocale(r.Context(), i18n.En), w); renderErr != nil {
			t.Errorf("render product fixture: %v", renderErr)
		}
	})
	mux.HandleFunc("GET /checkout", func(w http.ResponseWriter, r *http.Request) {
		view := checkout.View{
			Cart:     cart.View{ItemCount: 1, SubtotalCents: 89000, Lines: []cart.Line{{VariantID: "tote-natural", Slug: "canvas-tote", Name: "Canvas tote", Quantity: 1, UnitCents: 89000}}},
			Shipping: []cart.ShippingChoice{{VersionID: "home", Name: "Home delivery", Carrier: "Courier", FeeCents: 8000}, {VersionID: "store", Name: "Convenience store", FeeCents: 6000}},
			Chosen:   "home",
			SavedAddresses: []checkout.SavedAddress{
				{ID: "home-address", Label: "Home", Name: "Mei Lin", Phone: "0912345678", PostalCode: "106", City: "Taipei", District: "Da'an", Street: "1 Example Road"},
			},
			ChosenAddress: "home-address",
			Errors:        map[string]string{"email": "Enter an email address like name@example.com."},
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ctx := i18n.WithLocale(r.Context(), i18n.En)
		if renderErr := checkout.Page(checkout.Meta(ctx), &view).Render(ctx, w); renderErr != nil {
			t.Errorf("render checkout fixture: %v", renderErr)
		}
	})
	// The page frame on its own: the seed's departments and deals, a cart of two
	// short of free delivery, in either language (?lang=en), with 3C 數位
	// current unless ?nav= names another department.
	mux.HandleFunc("GET /frame", func(w http.ResponseWriter, r *http.Request) {
		locale, names := i18n.ZhHant, []string{"書籍文具", "居家生活", "美妝保養", "服飾配件", "美食飲品", "3C 數位"}
		if r.URL.Query().Get("lang") == "en" {
			locale, names = i18n.En, []string{"Books & stationery", "Home & living", "Beauty & care", "Fashion", "Food & drink", "Tech"}
		}
		photo := picture.Image{BundledKey: "nimbus-band-2-01.webp"}
		nav := make([]frame.NavItem, 0, len(names))
		// The seed's product counts, which the rail prints beside each name.
		counts := []int{4, 4, 4, 4, 4, 20}
		for i, slug := range []string{"books-stationery", "home-living", "beauty", "fashion", "food-drink", "tech"} {
			nav = append(nav, frame.NavItem{Slug: slug, Name: names[i], Href: "/c/" + slug, ProductCount: counts[i]})
		}
		nav[5].Children = []frame.NavItem{{Slug: "phones", Name: "Phones", Href: "/c/phones"}, {Slug: "audio", Name: "Audio", Href: "/c/audio"}}
		nav[5].PhotoURL, nav[5].PhotoSrcset = photo.URL(), photo.Srcset()
		basket := frame.CartSummary{Units: 2, SubtotalCents: 190700, Delivery: frame.CartDelivery{ShortCents: 109300}}
		ctx := frame.WithCart(frame.WithDeals(frame.WithTopNav(i18n.WithLocale(r.Context(), locale), nav), true), basket)
		current := "tech"
		if slug := r.URL.Query().Get("nav"); slug != "" {
			current = slug
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := frame.Base(frame.Page{Title: "Frame", Nav: current}).Render(templ.WithChildren(ctx, templ.NopComponent), w); renderErr != nil {
			t.Errorf("render frame fixture: %v", renderErr)
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
