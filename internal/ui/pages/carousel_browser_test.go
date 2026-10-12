package pages

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestCarouselCurrentTabStaysVisibleWithoutMovingThePage(t *testing.T) {
	port := os.Getenv("CDP_PORT")
	if port == "" {
		t.Skip("set CDP_PORT to run the native carousel regression")
	}
	root, err := filepath.Abs("../../..")
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
		view := HomeView{}
		for _, title := range titles {
			view.Slides = append(view.Slides, HeroSlide{Title: title, Layout: SlideSplit, Tone: ToneStone})
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := Home(layouts.Page{}, view).Render(i18n.WithLocale(r.Context(), locale), w); renderErr != nil {
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
