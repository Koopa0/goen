package pages_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/site"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

func TestPopoverPointerAndFocusInNativeBrowsers(t *testing.T) {
	driver := os.Getenv("GOEN_SAFARI_DRIVER_URL")
	port := os.Getenv("GOEN_POPOVER_CDP_PORT")
	if driver == "" && port == "" {
		t.Skip("set a hosted browser endpoint to run the native popover regression")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(assets.Prefix, assets.Handler(slog.New(slog.DiscardHandler)))
	localeHandler := &site.Handler{}
	mux.HandleFunc("POST /locale", localeHandler.SetLocale)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		locale := i18n.En
		if cookie, cookieErr := r.Cookie("goen_locale"); cookieErr == nil {
			locale = i18n.Parse(cookie.Value)
		}
		ctx := web.WithRequestPath(i18n.WithLocale(r.Context(), locale), r.URL.RequestURI())
		ctx = layouts.WithTopNav(ctx, []layouts.NavItem{{Slug: "audio", Name: "Audio", Href: "/c/audio"}})
		if web.IsHTMX(r) {
			ctx = pages.AsPartial(ctx)
		}
		view := pages.ListingView{
			Slug: "audio", Name: "Audio", Total: 2, Page: 1, PageSize: 20, Filtered: r.URL.RawQuery != "",
			Facets: []pages.FacetGroup{{Kind: pages.FacetBrand, Label: i18n.T(ctx, i18n.KeyFacetBrand), Options: []pages.FacetOption{
				{Value: "nimbus", Label: "Nimbus", Count: 1, Selected: r.URL.Query().Get("brand") == "nimbus"},
				{Value: "aurora", Label: "Aurora", Count: 1, Selected: r.URL.Query().Get("brand") == "aurora"},
			}}},
			Products: []pages.ProductTile{{Slug: "headphones", Name: "Headphones", PriceCents: 10000, InStock: true}},
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := pages.Listing(layouts.Page{Title: "Audio", Nav: "audio"}, view, nil, nil).Render(ctx, w); renderErr != nil {
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
