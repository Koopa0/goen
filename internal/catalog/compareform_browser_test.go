package catalog

import (
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
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/uitest"
	"github.com/koopa0/goen/internal/web/respond"
)

// TestCompareFormRowInBrowser holds the compare bar in a real browser, on a
// listing rendered from the real templates: the row of ticked products that
// goen.js adds from two boxes, its remove buttons and where they leave focus,
// the row after the results are swapped and after a return to the page, the
// bar with scripting off (still a GET to /compare carrying the ticked
// products), and the page not widening with the bar open at any width, in
// either language, at 100% and 200% text.
func TestCompareFormRowInBrowser(t *testing.T) {
	_, port := uitest.Browser(t, "CDP_PORT")
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	type product struct{ slug, name, brand, photo string }
	products := []product{
		{"nimbus-speaker-s1", "Nimbus Speaker S1", "nimbus", "nimbus-speaker-s1-01.webp"},
		{"koto-over-ear-quiet", "Koto Over-Ear Quiet", "koto", "koto-over-ear-01.webp"},
		{"nimbus-buds-pro", "Nimbus Buds Pro", "nimbus", "nimbus-buds-pro-01.webp"},
		// A name long enough to be cut at any width, in either language.
		{"meridian-watch-s3", "Meridian Watch S3 Titanium Edition with Sport Loop and Milanese Band", "meridian", "meridian-watch-s3-01.webp"},
		{"pixelight-9-pro", "Pixelight 9 Pro", "pixelight", "pixelight-9-pro-01.webp"},
		{"orili-canvas-tote", "Orili Canvas Tote", "orili", ""},
	}
	mux := http.NewServeMux()
	mux.Handle("GET "+assets.Prefix, assets.Handler(slog.New(slog.DiscardHandler)))
	mux.HandleFunc("GET /compare", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>compare</title><p>compare</p>"))
	})
	mux.HandleFunc("GET /c/audio", func(w http.ResponseWriter, r *http.Request) {
		locale := i18n.ZhHant
		if cookie, cookieErr := r.Cookie("goen_locale"); cookieErr == nil {
			locale = i18n.Parse(cookie.Value)
		}
		ctx := frame.WithReturnPath(i18n.WithLocale(r.Context(), locale), r.URL.RequestURI())
		// The frame as check-layout's catalogue draws it: the six departments,
		// a cart and the promotion strip.
		names := []string{"書籍文具", "居家生活", "美妝保養", "服飾配件", "美食飲品", "3C 數位"}
		banner := frame.Banner{ID: "layout", Message: "版面檢查用的促銷訊息，長度接近真實的一句文案", MessageShort: "版面檢查促銷", Code: "LAYOUT10", CTALabel: "看看", CTAHref: "/deals"}
		if locale == i18n.En {
			names = []string{"Books & stationery", "Home & living", "Beauty & care", "Fashion", "Food & drink", "Tech"}
			banner.Message, banner.MessageShort, banner.CTALabel = "A layout-check promotion, about as long as a real one", "Layout promo", "Look"
		}
		nav := make([]frame.NavItem, 0, len(names))
		for i, name := range names {
			slug := "department-" + strconv.Itoa(i)
			nav = append(nav, frame.NavItem{Slug: slug, Name: name, Href: "/c/" + slug})
		}
		ctx = frame.WithBanner(frame.WithDeals(frame.WithCartCount(frame.WithTopNav(ctx, nav), 2), true), banner)
		if respond.IsHTMX(r) {
			ctx = AsPartial(ctx)
		}
		brand := r.URL.Query().Get("brand")
		view := ListingView{Slug: "audio", Name: "Audio", Page: 1, PageSize: 20, Filtered: brand != ""}
		counts := map[string]int64{}
		for _, p := range products {
			counts[p.brand]++
			if brand != "" && p.brand != brand {
				continue
			}
			photo := picture.Image{BundledKey: p.photo}
			view.Products = append(view.Products, tile.Tile{
				Slug: p.slug, Name: p.name, PriceCents: 249000, InStock: true, Comparable: true,
				ImageURL: photo.URL(), ImageSrcset: photo.Srcset(), ImageWidth: 1600, ImageHeight: 1600,
			})
		}
		view.Total = int64(len(view.Products))
		group := FacetGroup{Kind: FacetBrand, Label: i18n.T(ctx, i18n.KeyFacetBrand)}
		for _, b := range []string{"nimbus", "koto", "meridian", "pixelight", "orili"} {
			group.Options = append(group.Options, FacetOption{Value: b, Label: strings.ToUpper(b[:1]) + b[1:], Count: counts[b], Selected: b == brand})
		}
		view.Facets = []FacetGroup{group}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := Listing(frame.Page{Title: "Audio", Nav: "audio"}, view, nil, nil).Render(ctx, w); renderErr != nil {
			t.Errorf("render compare listing: %v", renderErr)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	//nolint:gosec // G204: fixed regression script, test-owned server, caller's Chrome debugging port.
	cmd := exec.CommandContext(t.Context(), "node", filepath.Join(root, "scripts", "compare-bar-check.mjs"), server.URL, port)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compare bar: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}
