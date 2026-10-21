// Package site serves goen's standing informational pages and the not-found
// page, and the chrome every storefront page wears: the promotion strip and the
// header's department row. Middleware puts the chrome on the request's context,
// so no page handler has to remember to read it; which paths carry it is the
// route table's decision, in cmd/goen. The policy documents, the shipping page
// and the rules the shop states about itself are internal/policy's, and what
// makes a path or an origin a URL on this site is internal/siteurl's.
package site

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	log       *slog.Logger
	baseURL   string
	catalogue Catalogue
	content   *Store
	secure    bool
}

// Catalogue is what site reads from the catalogue: the sitemap's rows, and
// whether a running campaign has something to buy, which decides whether the
// header links to /deals.
type Catalogue interface {
	SitemapProducts(ctx context.Context, limit int32) ([]db.SitemapProductsRow, error)
	SitemapCategories(ctx context.Context, limit int32) ([]db.SitemapCategoriesRow, error)
	DealsOnOffer(ctx context.Context) (bool, error)
}

func NewHandler(log *slog.Logger, baseURL string, catalogue Catalogue, content *Store, secure bool) *Handler {
	if log == nil || catalogue == nil || content == nil {
		panic("site: NewHandler requires a logger, a catalogue and content")
	}
	return &Handler{log: log, baseURL: baseURL, catalogue: catalogue, content: content, secure: secure}
}

// Routes registers the shop's own pages: the FAQ, the about page, the sitemap,
// the language switch and the promotion strip's dismissal. The catch-all 404 is
// the caller's, because every unmatched path reaches it, not only this
// package's.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /sitemap.xml", h.Sitemap)
	mux.HandleFunc("GET /robots.txt", h.Robots)
	mux.HandleFunc("POST /promo/dismiss", h.DismissBanner)
	mux.HandleFunc("POST /locale", h.SetLocale)
	mux.HandleFunc("GET /faq", h.FAQ)
	mux.HandleFunc("GET /about", h.About)
}

func (h *Handler) About(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusOK, About(AboutMeta(r.Context())))
}

func (h *Handler) NotFound(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusNotFound, layouts.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyPageNotFoundTitle)},
		"404",
		i18n.T(r.Context(), i18n.KeyPageNotFound),
		i18n.T(r.Context(), i18n.KeyPageNotFoundBody),
	))
}

func (h *Handler) BadRequest(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusBadRequest, layouts.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAddressUnreadable)}, "400",
		i18n.T(r.Context(), i18n.KeyAddressUnreadable),
		i18n.T(r.Context(), i18n.KeyAddressRecovery)))
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "500",
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
}
