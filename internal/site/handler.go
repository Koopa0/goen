// Package site serves goen's standing informational pages and the not-found page.
package site

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Handler serves the informational pages.
type Handler struct {
	log       *slog.Logger
	baseURL   string
	catalogue Catalogue
	content   *Store
	secure    bool
}

// Catalogue is the subset of the catalogue this package needs.
type Catalogue interface {
	SitemapProducts(ctx context.Context, limit int32) ([]db.SitemapProductsRow, error)
	SitemapCategories(ctx context.Context, limit int32) ([]db.SitemapCategoriesRow, error)
}

// NewHandler returns a Handler logging to log.
func NewHandler(log *slog.Logger, baseURL string, catalogue Catalogue, content *Store, secure bool) *Handler {
	if log == nil || catalogue == nil || content == nil {
		panic("site: NewHandler requires a logger, a catalogue and content")
	}
	return &Handler{log: log, baseURL: baseURL, catalogue: catalogue, content: content, secure: secure}
}

// About serves GET /about.
func (h *Handler) About(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusOK, pages.About(pages.AboutMeta(r.Context())))
}

// NotFound answers every route goen does not serve.
func (h *Handler) NotFound(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyPageNotFoundTitle)},
		"404",
		i18n.T(r.Context(), i18n.KeyPageNotFound),
		i18n.T(r.Context(), i18n.KeyPageNotFoundBody),
	))
}
