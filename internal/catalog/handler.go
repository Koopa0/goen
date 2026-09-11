package catalog

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Handler serves the category listing and search pages.
type Handler struct {
	store *Store
	log   *slog.Logger
}

// NewHandler returns a Handler reading through store.
func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("catalog: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

// Listing serves GET /c/{slug}.
func (h *Handler) Listing(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	f := parseFilters(r.URL.Query())

	view, err := h.store.Listing(r.Context(), slug, f)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "load listing", "error", err, "slug", slug)
		h.serverError(w, r)
		return
	}

	view.Query = canonicalQuery(f)
	view.Filtered = f.Active()
	view.InStockOnly = f.InStockOnly
	view.MinPrice = f.MinPrice
	view.MaxPrice = f.MaxPrice
	view.Sort = string(f.Sort)

	web.Render(w, r, h.log, http.StatusOK, pages.Listing(pages.ListingMeta(r.Context(), view), view))
}

// Search serves GET /search.
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	pattern := SearchPattern(q)
	page := ParsePage(r.URL.Query().Get("page"))

	view := pages.SearchView{Query: trimForDisplay(q), Page: page, PageSize: PageSize}
	if pattern != "" {
		loaded, err := h.store.Search(r.Context(), pattern, page)
		if err != nil {
			h.log.ErrorContext(r.Context(), "search", "error", err)
			h.serverError(w, r)
			return
		}
		loaded.Query = view.Query
		view = loaded
	}

	web.Render(w, r, h.log, http.StatusOK, pages.Search(pages.SearchMeta(r.Context(), view.Query), view))
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyCategoryNotFound)}, "404",
		i18n.T(r.Context(), i18n.KeyCategoryNotFound),
		i18n.T(r.Context(), i18n.KeyCategoryNotFoundBody)))
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyCannotLoad)}, "",
		i18n.T(r.Context(), i18n.KeyCannotLoad),
		i18n.T(r.Context(), i18n.KeyCannotLoadListing)))
}

// parseFilters reads the listing's query string. Every value is bounded or
// discarded here, so nothing downstream has to check again.
func parseFilters(q url.Values) Filters {
	return Filters{
		BrandSlugs:  boundedBrands(q["brand"]),
		InStockOnly: q.Get("in_stock") == "1",
		MinPrice:    ParsePrice(q.Get("min_price")),
		MaxPrice:    ParsePrice(q.Get("max_price")),
		Sort:        ParseSort(q.Get("sort")),
		Page:        ParsePage(q.Get("page")),
	}
}

// maxBrandFilters caps how many brand values one URL may carry.
const maxBrandFilters = 32

func boundedBrands(v []string) []string {
	if len(v) > maxBrandFilters {
		v = v[:maxBrandFilters]
	}
	out := make([]string, 0, len(v))
	seen := make(map[string]bool, len(v))
	for _, s := range v {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// canonicalQuery rebuilds the listing's query string from the parsed filters
// rather than the request, so a pagination href carries nothing a visitor typed.
func canonicalQuery(f Filters) string {
	q := url.Values{}
	for _, b := range f.BrandSlugs {
		q.Add("brand", b)
	}
	if f.InStockOnly {
		q.Set("in_stock", "1")
	}
	if f.MinPrice > 0 {
		q.Set("min_price", strconv.FormatInt(f.MinPrice/100, 10))
	}
	if f.MaxPrice > 0 {
		q.Set("max_price", strconv.FormatInt(f.MaxPrice/100, 10))
	}
	if f.Sort != SortNewest {
		q.Set("sort", string(f.Sort))
	}
	return q.Encode()
}

// trimForDisplay bounds what a search term may be echoed back as.
func trimForDisplay(q string) string {
	return trimmedQuery(q)
}

// Deals serves GET /deals.
func (h *Handler) Deals(w http.ResponseWriter, r *http.Request) {
	page := ParsePage(r.URL.Query().Get("page"))
	view, err := h.store.Deals(r.Context(), page)
	if err != nil {
		h.log.ErrorContext(r.Context(), "load deals", "error", err)
		h.serverError(w, r)
		return
	}
	campaigns, err := h.store.RunningCampaigns(r.Context())
	if err != nil {
		// best-effort: the discounted products are the page's substance.
		h.log.ErrorContext(r.Context(), "load campaigns", "error", err)
	}
	view.Campaigns = campaigns
	web.Render(w, r, h.log, http.StatusOK, pages.Deals(pages.DealsMeta(r.Context()), view))
}

// Campaign serves GET /s/{slug}. One outside its window is a 404.
func (h *Handler) Campaign(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	view, err := h.store.Campaign(r.Context(), slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
				layouts.Page{Title: i18n.T(r.Context(), i18n.KeyCampaignNotFound)}, "404",
				i18n.T(r.Context(), i18n.KeyCampaignNotFound),
				i18n.T(r.Context(), i18n.KeyCampaignNotFoundBody)))
			return
		}
		h.log.ErrorContext(r.Context(), "load campaign", "error", err, "slug", slug)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Campaign(
		pages.CampaignMeta(r.Context(), view.Title), view))
}

// Compare serves GET /compare. The set lives in the URL and nowhere else.
func (h *Handler) Compare(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Compare(r.Context(), r.URL.Query()["p"])
	if err != nil {
		h.log.ErrorContext(r.Context(), "read comparison", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Compare(
		layouts.Page{
			Title:       i18n.T(r.Context(), i18n.KeyCompareTitle),
			Description: i18n.T(r.Context(), i18n.KeyCompareDescription),
		}, view))
}
