// Package site serves goen's standing informational pages — the ones with no
// state behind them beyond the copy itself — and the not-found page.
package site

import (
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Handler serves the informational pages.
type Handler struct {
	log *slog.Logger
}

// NewHandler returns a Handler logging to log.
func NewHandler(log *slog.Logger) *Handler {
	if log == nil {
		panic("site: NewHandler requires a logger")
	}
	return &Handler{log: log}
}

// About serves GET /about.
func (h *Handler) About(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusOK, pages.About(pages.AboutMeta))
}

// Home serves GET /. The storefront home page is the next delivery batch; the
// route answers honestly in the meantime rather than 404ing the site root that
// every logo in the chrome points at.
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	meta := layouts.Page{Title: "首頁建置中"}
	web.Render(w, r, h.log, http.StatusOK, pages.Notice(
		meta,
		"",
		"首頁正在建置中",
		"goen 的商店首頁是下一批交付的畫面。目前可以先看看關於我們,或直接與我們聯絡。",
	))
}

// NotFound answers every route goen does not serve.
func (h *Handler) NotFound(w http.ResponseWriter, r *http.Request) {
	meta := layouts.Page{Title: "找不到頁面"}
	web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
		meta,
		"404",
		"找不到這個頁面",
		"這個網址目前沒有對應的內容。商品、分類與結帳流程會在後續批次陸續上線。",
	))
}
