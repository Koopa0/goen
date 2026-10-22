package catalog

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/uitest"
)

// TestProductGalleryThumbnails holds that the gallery thumbnails wrap and keep
// native selection.
func TestProductGalleryThumbnails(t *testing.T) {
	_, port := uitest.Browser(t, "CDP_PORT")
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	images := []string{
		"meridian-book-sleeve-14-01.webp", "orili-cotton-tee-01-800.webp",
		"fernway-city-sketch-walks-01.webp", "orili-canvas-tote-02.webp",
		"orili-bucket-hat-01-400.webp", "koto-pad-mini-01.webp",
		"dawnroast-drip-coffee-01.webp", "paperwick-dot-notebook-a5-03.webp",
		"aurora-power-bank-10000-02.webp", "nimbus-band-2-01.webp",
	}
	mux := http.NewServeMux()
	mux.Handle(assets.Prefix, assets.Handler(slog.New(slog.DiscardHandler)))
	mux.HandleFunc("GET /gallery/{count}", func(w http.ResponseWriter, r *http.Request) {
		count, parseErr := strconv.Atoi(r.PathValue("count"))
		if parseErr != nil || !slices.Contains([]int{0, 1, 5, 6, 10}, count) {
			http.NotFound(w, r)
			return
		}
		ctx := i18n.WithLocale(r.Context(), i18n.En)
		if r.URL.Query().Get("lang") == "zh-Hant" {
			ctx = i18n.WithLocale(r.Context(), i18n.ZhHant)
		}
		view := &ProductView{Slug: "gallery-fixture", Name: "Gallery fixture"}
		for i := range count {
			view.Images = append(view.Images, ProductImage{
				URL: assets.Image{BundledKey: images[i]}.URL(), Alt: fmt.Sprintf("Photo %d", i+1),
			})
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := Product(frame.Page{}, view).Render(ctx, w); renderErr != nil {
			t.Errorf("render gallery fixture: %v", renderErr)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	//nolint:gosec // G204: fixed regression script, test-owned server, caller's Chrome debugging port.
	cmd := exec.CommandContext(t.Context(), "node", filepath.Join(root, "scripts", "gallery-thumbnail-check.mjs"), server.URL, port)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gallery thumbnails: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}
