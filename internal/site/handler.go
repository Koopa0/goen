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

type Handler struct {
	log       *slog.Logger
	baseURL   string
	catalogue Catalogue
	content   *Store
	secure    bool
}

type Catalogue interface {
	SitemapProducts(ctx context.Context, limit int32) ([]db.SitemapProductsRow, error)
	SitemapCategories(ctx context.Context, limit int32) ([]db.SitemapCategoriesRow, error)
}

func NewHandler(log *slog.Logger, baseURL string, catalogue Catalogue, content *Store, secure bool) *Handler {
	if log == nil || catalogue == nil || content == nil {
		panic("site: NewHandler requires a logger, a catalogue and content")
	}
	return &Handler{log: log, baseURL: baseURL, catalogue: catalogue, content: content, secure: secure}
}

func (h *Handler) About(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusOK, pages.About(pages.AboutMeta(r.Context())))
}

func (h *Handler) NotFound(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyPageNotFoundTitle)},
		"404",
		i18n.T(r.Context(), i18n.KeyPageNotFound),
		i18n.T(r.Context(), i18n.KeyPageNotFoundBody),
	))
}

func (h *Handler) BadRequest(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusBadRequest, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAddressUnreadable)}, "400",
		i18n.T(r.Context(), i18n.KeyAddressUnreadable),
		i18n.T(r.Context(), i18n.KeyAddressRecovery)))
}
