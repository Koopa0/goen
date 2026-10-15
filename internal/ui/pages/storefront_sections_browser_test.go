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

// The home page's hero tabs, departments and rules row, the deals page's
// running campaigns and the search page's compare button, measured as drawn.
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
				Photo: components.Photo{URL: assets.ProductImageURL(c.photo)},
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
