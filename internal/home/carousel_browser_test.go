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
	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/tone"
	"github.com/koopa0/goen/internal/ui/uitest"
)

func TestCarouselCurrentTabStaysVisibleWithoutMovingThePage(t *testing.T) {
	_, port := uitest.Browser(t, "CDP_PORT")
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
		for _, title := range titles {
			view.Slides = append(view.Slides, hero.Slide{Title: title, Layout: hero.LayoutSplit, Tone: tone.Stone})
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := Page(layouts.Page{}, view).Render(i18n.WithLocale(r.Context(), locale), w); renderErr != nil {
			t.Errorf("render carousel fixture: %v", renderErr)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	//nolint:gosec // G204: fixed regression script, test-owned server and caller's Chrome debugging port.
	cmd := exec.CommandContext(t.Context(), "node", filepath.Join(root, "scripts", "carousel-tab-check.mjs"), server.URL, port)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("carousel tabs: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}
