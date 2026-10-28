package home

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
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/tone"
	"github.com/koopa0/goen/internal/ui/uitest"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestHeroCarouselCurrentTab holds that the carousel's current tab stays
// visible without moving the page, and that a slide chosen with a tab fades in
// where the browser can.
func TestHeroCarouselCurrentTab(t *testing.T) {
	name, value := uitest.Browser(t, "GOEN_SAFARI_DRIVER_URL", "CDP_PORT")
	script := "carousel-tab-check.mjs"
	if name == "GOEN_SAFARI_DRIVER_URL" {
		script = "carousel-safari-check.mjs"
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(assets.Prefix, assets.Handler(slog.New(slog.DiscardHandler)))
	mux.HandleFunc("GET /carousel", func(w http.ResponseWriter, r *http.Request) {
		locale := i18n.ZhHant
		titles := []string{"科技配件", "生活選物", "書籍文具"}
		if r.URL.Query().Get("lang") == "en" {
			locale = i18n.En
			titles = []string{"Technology", "Everyday essentials", "Books & stationery"}
		}
		view := View{}
		photos := []string{"restwood-stoneware-mug-01.webp", "koto-over-ear-01.webp", ""}
		for i, title := range titles {
			slide := hero.Slide{Title: title, Layout: hero.LayoutSplit, Tone: tone.Stone}
			if r.URL.Query().Get("photos") == "1" && photos[i] != "" {
				slide.Photo = widget.Photo{URL: picture.Image{BundledKey: photos[i]}.URL(), Alt: title}
			}
			view.Slides = append(view.Slides, slide)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := Page(frame.Page{}, view).Render(i18n.WithLocale(r.Context(), locale), w); renderErr != nil {
			t.Errorf("render carousel fixture: %v", renderErr)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	//nolint:gosec // G204: one of two fixed regression scripts, test-owned server and the caller's browser endpoint.
	cmd := exec.CommandContext(t.Context(), "node", filepath.Join(root, "scripts", script), server.URL, value)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("carousel tabs: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}
