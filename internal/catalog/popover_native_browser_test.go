package catalog_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/site"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/uitest"
	"github.com/koopa0/goen/internal/web/respond"
)

// TestListingPopoverNative holds that the popovers of the listing page, the
// filter groups and the language menu, keep their pointer and focus behaviour
// in native browsers.
func TestListingPopoverNative(t *testing.T) {
	name, value := uitest.Browser(t, "GOEN_SAFARI_DRIVER_URL", "GOEN_POPOVER_CDP_PORT")
	var driver, port string
	if name == "GOEN_SAFARI_DRIVER_URL" {
		driver = value
	} else {
		port = value
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET "+assets.Prefix, assets.Handler(slog.New(slog.DiscardHandler)))
	localeHandler := &site.Handler{}
	mux.HandleFunc("POST /locale", localeHandler.SetLocale)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		locale := i18n.En
		if cookie, cookieErr := r.Cookie("goen_locale"); cookieErr == nil {
			locale = i18n.Parse(cookie.Value)
		}
		ctx := frame.WithReturnPath(i18n.WithLocale(r.Context(), locale), r.URL.RequestURI())
		// The frame as check-layout's catalogue draws it: the seed's six
		// departments with deals, a cart and the promotion strip. Under that
		// strip at 320 and 200% text the brand group sits at the bottom edge and
		// its panel opens below the fold.
		names := []string{"書籍文具", "居家生活", "美妝保養", "服飾配件", "美食飲品", "3C 數位"}
		banner := frame.Banner{ID: "layout", Message: "版面檢查用的促銷訊息，長度接近真實的一句文案", MessageShort: "版面檢查促銷", Code: "LAYOUT10", CTALabel: "看看", CTAHref: "/deals"}
		if locale == i18n.En {
			names = []string{"Books & stationery", "Home & living", "Beauty & care", "Fashion", "Food & drink", "Tech"}
			banner.Message, banner.MessageShort, banner.CTALabel = "A layout-check promotion, about as long as a real one", "Layout promo", "Look"
		}
		nav := make([]frame.NavItem, 0, len(names))
		for i, name := range names {
			slug := "department-" + strconv.Itoa(i)
			nav = append(nav, frame.NavItem{Slug: slug, Name: name, Href: "/c/" + slug, Children: []frame.NavItem{{Slug: "audio", Name: "Audio", Href: "/c/audio"}}})
		}
		ctx = frame.WithBanner(frame.WithDeals(frame.WithCartCount(frame.WithTopNav(ctx, nav), 2), true), banner)
		if respond.IsHTMX(r) {
			ctx = catalog.AsPartial(ctx)
		}
		view := catalog.ListingView{
			Slug: "audio", Name: "Audio", Total: 2, Page: 1, PageSize: 20, Filtered: r.URL.RawQuery != "",
			Facets: []catalog.FacetGroup{{Kind: catalog.FacetBrand, Label: i18n.T(ctx, i18n.KeyFacetBrand), Options: []catalog.FacetOption{
				{Value: "nimbus", Label: "Nimbus", Count: 1, Selected: r.URL.Query().Get("brand") == "nimbus"},
				{Value: "aurora", Label: "Aurora", Count: 1, Selected: r.URL.Query().Get("brand") == "aurora"},
			}}},
			Products: []tile.Tile{{Slug: "headphones", Name: "Headphones", PriceCents: 10000, InStock: true}},
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := catalog.Listing(frame.Page{Title: "Audio", Nav: "audio"}, view, nil, nil).Render(ctx, w); renderErr != nil {
			t.Errorf("render popover listing: %v", renderErr)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	script := "popover-safari-check.mjs"
	if driver == "" {
		script = "popover-focus-check.mjs"
	}
	//nolint:gosec // G204: script is one of two fixed regressions, with a test-owned server and hosted browser endpoints.
	cmd := exec.CommandContext(t.Context(), "node", filepath.Join(root, "scripts", script), server.URL, driver)
	cmd.Env = append(os.Environ(), "GOEN_URL="+server.URL, "CDP_PORT="+port)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native popover: %v\n%s", err, output)
	}
	t.Log(string(output))
}
