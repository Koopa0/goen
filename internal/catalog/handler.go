package catalog

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/policy"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/web/ratelimit"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	store    *Store
	log      *slog.Logger
	baseURL  string
	askLimit *ratelimit.Limiter
}

func NewHandler(store *Store, log *slog.Logger, baseURL string) *Handler {
	if store == nil || log == nil {
		panic("catalog: NewHandler requires a store and a logger")
	}
	return &Handler{
		store: store, log: log, baseURL: baseURL,
		askLimit: ratelimit.New(ratelimit.Config{
			Every: 6 * time.Second, Burst: 10, TTL: time.Hour, MaxKeys: 8_192,
		}),
	}
}

// Routes registers browsing, the product page and its three forms. notify
// bounds the restock form per client before Notify runs; the caller owns that
// budget, and Notify spends none of its own.
func (h *Handler) Routes(mux *http.ServeMux, notify *ratelimit.Limiter) {
	mux.HandleFunc("GET /c/{slug}", h.Listing)
	mux.HandleFunc("GET /search", h.Search)
	mux.HandleFunc("GET /compare", h.Compare)
	mux.HandleFunc("GET /p/{slug}", h.Product)
	mux.HandleFunc("POST /p/{slug}/reviews", h.Review)
	mux.HandleFunc("POST /p/{slug}/notify", ratelimit.Guard(notify, h.log, h.Notify))
	mux.HandleFunc("POST /p/{slug}/questions", h.Ask)
}

func (h *Handler) Listing(w http.ResponseWriter, r *http.Request) {
	if dropEmptyParams(w, r) {
		return
	}
	slug := r.PathValue("slug")
	f := parseFilters(r.URL.Query())

	view, head, err := h.store.ListingPage(r.Context(), slug, f, !respond.IsHTMX(r))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "load listing", "error", err, "slug", slug)
		h.serverError(w, r)
		return
	}

	if redirectUnknownFilters(w, r, view.Facets) || redirectPastLastPage(w, r, int(view.Page), view.Pages()) {
		return
	}

	// A partial swap never draws the rules or the head, so it does not read them.
	var rules *policy.Rules
	if respond.IsHTMX(r) {
		r = r.WithContext(AsPartial(r.Context()))
	} else {
		loaded, err := h.store.ShopRules(r.Context())
		if err != nil {
			h.log.ErrorContext(r.Context(), "load shop rules", "error", err)
			h.serverError(w, r)
			return
		}
		rules = &loaded
	}
	respond.Component(w, r, h.log, http.StatusOK, Listing(ListingMeta(r.Context(), view), view, rules, head))
}

const newestOnEmpty = 4

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	if dropEmptyParams(w, r, "q") {
		return
	}
	q := r.URL.Query().Get("q")
	pattern := SearchPattern(q)
	page := ParsePage(r.URL.Query().Get("page"))

	sort := ParseSort(r.URL.Query().Get("sort"), SortRelevance)

	view := SearchView{Query: trimForDisplay(q), Page: page, PageSize: PageSize}
	if pattern != "" {
		var err error
		if view, err = h.searched(r.Context(), pattern, view.Query, sort, page); err != nil {
			h.log.ErrorContext(r.Context(), "search", "error", err)
			h.serverError(w, r)
			return
		}
	}

	if redirectPastLastPage(w, r, view.Page, view.Pages()) {
		return
	}
	if respond.IsHTMX(r) {
		r = r.WithContext(AsPartial(r.Context()))
	}
	respond.Component(w, r, h.log, http.StatusOK, Search(SearchMeta(r.Context(), view.Query), view))
}

func (h *Handler) searched(ctx context.Context, pattern, query string, sort Sort, page int) (SearchView, error) {
	view, err := h.store.Search(ctx, pattern, sort, page)
	if err != nil {
		return SearchView{}, err
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
	respond.Component(w, r, h.log, http.StatusNotFound, layouts.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyCategoryNotFound)}, "404",
		i18n.T(r.Context(), i18n.KeyCategoryNotFound),
		i18n.T(r.Context(), i18n.KeyCategoryNotFoundBody)))
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	respond.Component(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
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
	respond.Component(w, r, h.log, http.StatusOK, Compare(
		layouts.Page{
			Title:       i18n.T(r.Context(), i18n.KeyCompareTitle),
			Description: i18n.T(r.Context(), i18n.KeyCompareDescription),
		}, view))
}

func compareCandidates(found []tile.Tile, view CompareView) []CompareCandidate {
	out := make([]CompareCandidate, 0, len(found))
	for i := range found {
		if slices.ContainsFunc(view.Products, func(p CompareProduct) bool { return p.Slug == found[i].Slug }) {
			continue
		}
		out = append(out, CompareCandidate{Slug: found[i].Slug, Name: found[i].Name, Brand: found[i].Brand})
	}
	return out
}

func redirectPastLastPage(w http.ResponseWriter, r *http.Request, page, last int) bool {
	q := r.URL.Query()
	requestedPage, err := strconv.Atoi(q.Get("page"))
	if page <= last && (err != nil || requestedPage <= last) {
		return false
	}
	q.Set("page", strconv.Itoa(last))
	target := *r.URL
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.RequestURI(), http.StatusSeeOther) //nolint:gosec // G710: this request's own path with only its query changed
	return true
}

func redirectUnknownFilters(w http.ResponseWriter, r *http.Request, facets []FacetGroup) bool {
	q := r.URL.Query()
	if len(q["brand"]) == 0 && len(q["opt"]) == 0 {
		return false
	}
	// Facets include zero-count choices, so an existing value that the other
	// filters exclude remains a legitimate filter.
	known := map[string]map[string]bool{"brand": {}, "opt": {}}
	for _, group := range facets {
		choices, relevant := known[group.Param()]
		if !relevant {
			continue
		}
		for _, option := range group.Options {
			choices[option.Value] = true
		}
	}
	changed := false
	for _, param := range []string{"brand", "opt"} {
		kept := make([]string, 0, len(q[param]))
		for _, value := range q[param] {
			if known[param][filterIdentity(param, value)] {
				kept = append(kept, value)
			} else {
				changed = true
			}
		}
		if len(kept) == 0 {
			q.Del(param)
		} else {
			q[param] = kept
		}
	}
	if !changed {
		return false
	}
	target := *r.URL
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.RequestURI(), http.StatusSeeOther) //nolint:gosec // G710: this request's own path with only its query changed
	return true
}

func filterIdentity(param, value string) string {
	if param != "opt" {
		return value
	}
	pairs := boundedOptionValues([]string{value})
	if len(pairs) != 1 {
		return ""
	}
	return pairs[0].Name + ":" + pairs[0].Value
}
