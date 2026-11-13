package home_test

import (
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

// The home page's hero (its square photograph across the window, its tabs,
// its campaign's days and products, no filled button), hero arrows,
// departments and rules row, the deals page's running campaigns, the search
// page's compare button, the cart's stepper and filled button, a department's
// filter chip, the corners every pressable control shares, a product's
// photograph, the header's search pill with the rule under the header, a
// department's head as words in the page column and the home page's band
// across the window with its name upright in Chinese where it fits, the one way a chosen option, a chosen
// delivery card and a refused field are marked, the page frame's header,
// department panel and footer, and the ways on from an empty cart, an empty
// wishlist and a missing page with the checkout's one solid button, measured as
// drawn.
func TestStorefrontSectionsAsDrawn(t *testing.T) {
	_, port := uitest.Browser(t, "CDP_PORT")
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(assets.Prefix, assets.Handler(slog.New(slog.DiscardHandler)))
	tones := map[string]tone.Tone{"ink": tone.Ink, "sage": tone.Sage, "stone": tone.Stone}
	mux.HandleFunc("GET /home", func(w http.ResponseWriter, r *http.Request) {
		view := home.View{Rules: policy.Rules{FreeDeliveryCents: 99900, LowestFeeCents: 6000, PickupOffered: true}}
		photo := widget.Photo{URL: picture.Image{BundledKey: "nimbus-band-2-01.webp"}.URL(), Alt: "Headphones on a desk"}
		// The first slide is a campaign's: its end, five days of which two are
		// gone, and its four products.
		now := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
		period := widget.PeriodSpec{Cells: []widget.PeriodCell{{State: widget.CellPast}, {State: widget.CellPast}, {State: widget.CellToday}, {}, {}}}
		products := make([]tile.Tile, 0, 4)
		for _, name := range []string{"Oolong", "Black tea", "Drip coffee", "Coffee beans"} {
			products = append(products, tile.Tile{Slug: strings.ToLower(strings.ReplaceAll(name, " ", "-")), Name: name, PriceCents: 58500, CompareCents: 65000, OnSale: true, InStock: true})
		}
		view.Slides = []hero.Slide{
			{Title: "Technology", Source: hero.SourceCampaign, Layout: hero.LayoutPhoto, Tone: tone.Stone, Photo: photo,
				Ends:   campaign.Ends(campaign.NewEnd(time.Date(2026, 11, 16, 16, 0, 0, 0, time.UTC), now)),
				Period: &period, CTA: hero.CTA{Label: "See all items", Href: "/s/tea"}, Tiles: products},
			{Title: "Everyday essentials", Layout: hero.LayoutSplit, Tone: tone.Ink, Photo: photo, CTA: hero.CTA{Label: "Shop now", Href: "/c/audio"}},
			{Title: "Books & stationery", Layout: hero.LayoutPhoto, Tone: tone.Sage, Photo: photo},
			{Title: "Slow mornings", Layout: hero.LayoutSplit, Tone: tone.Sage, Photo: photo, CTA: hero.CTA{Label: "See the range", Href: "/c/coffee"}},
			{Title: "After dark", Layout: hero.LayoutSplit, Tone: tone.Ink, Photo: photo},
		}
		// ?bare=1 leaves the last two slides without a photograph.
		if r.URL.Query().Get("bare") == "1" {
			view.Slides[3].Photo, view.Slides[4].Photo = widget.Photo{}, widget.Photo{}
		}
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
		// ?zh=1 draws the page in Chinese, the band's name upright where it fits.
		locale, bandName := i18n.En, "Technology"
		if r.URL.Query().Get("zh") == "1" {
			locale, bandName = i18n.ZhHant, "居家生活"
		}
		if bandTone, ok := tones[r.URL.Query().Get("band")]; ok {
			view.Band = &home.DepartmentBand{Name: bandName, Items: 12, Subs: []string{"Audio", "Phones"}, Href: "/c/tech", Tone: bandTone}
			// The first two are on sale until 22 November.
			until := shoptime.LastDay(time.Date(2026, 11, 22, 16, 0, 0, 0, time.UTC), now)
			for i, name := range []string{"Canvas tote", "City tote", "Weekend tote", "Market tote"} {
				t := tile.Tile{Slug: strings.ToLower(strings.ReplaceAll(name, " ", "-")), Name: name, PriceCents: 89000, InStock: true}
				if i < 2 {
					t.CompareCents, t.OnSale, t.SaleUntil = 99000, true, until
				}
				view.Band.Tiles = append(view.Band.Tiles, t)
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ctx := frame.WithTopNav(i18n.WithLocale(r.Context(), locale), departments)
		// ?promo=1 puts the strip above the header, with its code and its word.
		if r.URL.Query().Get("promo") == "1" {
			ctx = frame.WithBanner(ctx, frame.Banner{
				ID: "autumn", Message: "Free delivery on orders over NT$999 this week", MessageShort: "Free delivery",
				Code: "AUTUMN", CTALabel: "Shop now", CTAHref: "/s/tea",
			})
		}
		if renderErr := home.Page(frame.Page{}, view).Render(ctx, w); renderErr != nil {
			t.Errorf("render home fixture: %v", renderErr)
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
	// The page frame on its own: the seed's departments and deals, 3C 數位 with
	// a panel, a cart of two, in either language (?lang=en), with 3C 數位
	// current.
	mux.HandleFunc("GET /frame", func(w http.ResponseWriter, r *http.Request) {
		locale, names := i18n.ZhHant, []string{"書籍文具", "居家生活", "美妝保養", "服飾配件", "美食飲品", "3C 數位"}
		if r.URL.Query().Get("lang") == "en" {
			locale, names = i18n.En, []string{"Books & stationery", "Home & living", "Beauty & care", "Fashion", "Food & drink", "Tech"}
		}
		photo := picture.Image{BundledKey: "nimbus-band-2-01.webp"}
		nav := make([]frame.NavItem, 0, len(names))
		for i, slug := range []string{"books-stationery", "home-living", "beauty", "fashion", "food-drink", "tech"} {
			nav = append(nav, frame.NavItem{Slug: slug, Name: names[i], Href: "/c/" + slug})
		}
		nav[5].Children = []frame.NavItem{{Slug: "phones", Name: "Phones", Href: "/c/phones"}, {Slug: "audio", Name: "Audio", Href: "/c/audio"}}
		nav[5].PhotoURL, nav[5].PhotoSrcset = photo.URL(), photo.Srcset()
		for _, name := range []string{"Nimbus Band 2", "Nimbus Speaker S1", "Meridian Watch C1"} {
			nav[5].Picks = append(nav[5].Picks, frame.NavPick{Slug: "band", Name: name, Price: "NT$1,900", ImageURL: photo.URL(), ImageSrcset: photo.Srcset()})
		}
		ctx := frame.WithCartCount(frame.WithDeals(frame.WithTopNav(i18n.WithLocale(r.Context(), locale), nav), true), 2)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := frame.Base(frame.Page{Title: "Frame", Nav: "tech"}).Render(templ.WithChildren(ctx, templ.NopComponent), w); renderErr != nil {
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
