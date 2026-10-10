package catalog

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("catalog: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Listing(w http.ResponseWriter, r *http.Request) {
	if web.DropEmptyParams(w, r) {
		return
	}
	slug := r.PathValue("slug")
	f := parseFilters(r.URL.Query())

	view, head, err := h.store.ListingPage(r.Context(), slug, f, !web.IsHTMX(r))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "load listing", "error", err, "slug", slug)
		h.serverError(w, r)
		return
	}

	// A partial swap never draws the rules or the head, so it does not read them.
	var rules *pages.ShopRules
	if web.IsHTMX(r) {
		r = r.WithContext(pages.AsPartial(r.Context()))
	} else {
		loaded, err := h.store.ShopRules(r.Context())
		if err != nil {
			h.log.ErrorContext(r.Context(), "load shop rules", "error", err)
			h.serverError(w, r)
			return
		}
		rules = &loaded
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Listing(pages.ListingMeta(r.Context(), view), view, rules, head))
}

const newestOnEmpty = 4

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	if web.DropEmptyParams(w, r, "q") {
		return
	}
	q := r.URL.Query().Get("q")
	pattern := SearchPattern(q)
	page := ParsePage(r.URL.Query().Get("page"))

	sort := ParseSort(r.URL.Query().Get("sort"), SortRelevance)

	view := pages.SearchView{Query: trimForDisplay(q), Page: page, PageSize: PageSize}
	if pattern != "" {
		var err error
		if view, err = h.searched(r.Context(), pattern, view.Query, sort, page); err != nil {
			h.log.ErrorContext(r.Context(), "search", "error", err)
			h.serverError(w, r)
			return
		}
	}

	if web.IsHTMX(r) {
		r = r.WithContext(pages.AsPartial(r.Context()))
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Search(pages.SearchMeta(r.Context(), view.Query), view))
}

func (h *Handler) searched(ctx context.Context, pattern, query string, sort Sort, page int) (pages.SearchView, error) {
	view, err := h.store.Search(ctx, pattern, sort, page)
	if err != nil {
		return pages.SearchView{}, err
	}
	view.Query = query
	if view.Empty() {
		// Best effort: the page already says nothing matched, and the
		// department chips below it still lead somewhere.
		newest, newestErr := h.store.NewestProducts(ctx, newestOnEmpty)
		if newestErr != nil {
			h.log.ErrorContext(ctx, "newest products for an empty search", "error", newestErr)
		}
		view.Newest = newest
	}
	return view, nil
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyCategoryNotFound)}, "404",
		i18n.T(r.Context(), i18n.KeyCategoryNotFound),
		i18n.T(r.Context(), i18n.KeyCategoryNotFoundBody)))
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyCannotLoad)}, "500",
		i18n.T(r.Context(), i18n.KeyCannotLoad),
		i18n.T(r.Context(), i18n.KeyCannotLoadListing)))
}

// parseFilters bounds or discards every value, so nothing downstream checks
// again.
func parseFilters(q url.Values) Filters {
	return Filters{
		BrandSlugs:   boundedBrands(q["brand"]),
		OptionValues: boundedOptionValues(q["opt"]),
		InStockOnly:  q.Get("in_stock") == "1",
		MinPrice:     ParsePrice(q.Get("min_price")),
		MaxPrice:     ParsePrice(q.Get("max_price")),
		Sort:         ParseSort(q.Get("sort"), SortNewest),
		Page:         ParsePage(q.Get("page")),
	}
}

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

// canonicalQuery rebuilds the query string from the parsed filters rather than
// the request, so a pagination href carries nothing a visitor typed.
func canonicalQuery(f Filters) string {
	q := url.Values{}
	for _, pair := range f.OptionValues {
		q.Add("opt", pair.Name+":"+pair.Value)
	}
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
	if sort := f.Sort.Param(); sort != "" {
		q.Set("sort", sort)
	}
	return q.Encode()
}

func trimForDisplay(q string) string {
	return trimmedQuery(q)
}

func (h *Handler) Deals(w http.ResponseWriter, r *http.Request) {
	page := ParsePage(r.URL.Query().Get("page"))
	view, err := h.store.Deals(r.Context(), page)
	if err != nil {
		h.log.ErrorContext(r.Context(), "load deals", "error", err)
		h.serverError(w, r)
		return
	}
	campaigns, err := h.store.ListedCampaigns(r.Context(), ParsePage(r.URL.Query().Get("campaign_page")))
	if err != nil {
		// Best effort: the discounted products are the page's substance.
		h.log.ErrorContext(r.Context(), "load campaigns", "error", err)
	}
	view.Campaigns = campaigns
	web.Render(w, r, h.log, http.StatusOK, pages.Deals(pages.DealsMeta(r.Context()), view))
}

// Campaign answers 404 for a campaign that is switched off or does not exist.
func (h *Handler) Campaign(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	view, err := h.store.Campaign(r.Context(), slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
				layouts.Page{Title: i18n.T(r.Context(), i18n.KeyCampaignNotFound)}, "404",
				i18n.T(r.Context(), i18n.KeyCampaignNotFound),
				i18n.T(r.Context(), i18n.KeyCampaignNotFoundBody),
				pages.NoticeActions{Primary: pages.NoticeLink{Href: "/deals", Label: i18n.KeyCurrentDeals}}))
			return
		}
		h.log.ErrorContext(r.Context(), "load campaign", "error", err, "slug", slug)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Campaign(
		pages.CampaignMeta(r.Context(), view.Title, view.Image), view))
}

// Compare keeps the set in the URL and nowhere else.
func (h *Handler) Compare(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Compare(r.Context(), r.URL.Query()["p"])
	if err != nil {
		h.log.ErrorContext(r.Context(), "read comparison", "error", err)
		h.serverError(w, r)
		return
	}
	if q := r.URL.Query().Get("q"); !view.Full() {
		view.Query = trimForDisplay(q)
		if pattern := SearchPattern(q); pattern != "" {
			found, searchErr := h.store.Search(r.Context(), pattern, SortRelevance, 1)
			if searchErr != nil {
				h.log.ErrorContext(r.Context(), "compare search", "error", searchErr)
				h.serverError(w, r)
				return
			}
			view.Candidates = compareCandidates(found.Products, view)
		}
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Compare(
		layouts.Page{
			Title:       i18n.T(r.Context(), i18n.KeyCompareTitle),
			Description: i18n.T(r.Context(), i18n.KeyCompareDescription),
		}, view))
}

func compareCandidates(found []pages.ProductTile, view pages.CompareView) []pages.CompareCandidate {
	out := make([]pages.CompareCandidate, 0, len(found))
	for i := range found {
		if slices.ContainsFunc(view.Products, func(p pages.CompareProduct) bool { return p.Slug == found[i].Slug }) {
			continue
		}
		out = append(out, pages.CompareCandidate{Slug: found[i].Slug, Name: found[i].Name, Brand: found[i].Brand})
	}
	return out
}
