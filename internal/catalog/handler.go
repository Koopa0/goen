package catalog

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
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
	mux.HandleFunc("GET /shop", h.Shop)
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
	q := r.URL.Query()
	view, err := h.store.ListingPage(r.Context(), slug, parseFilters(q, SortNewest), ParseView(q.Get("view")))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "load listing", "error", err, "slug", slug)
		h.serverError(w, r)
		return
	}
	h.listing(w, r, &view)
}

// Shop is every product on one shelf, under the same filters as a category.
func (h *Handler) Shop(w http.ResponseWriter, r *http.Request) {
	if dropEmptyParams(w, r) {
		return
	}
	q := r.URL.Query()
	view, err := h.store.ShopPage(r.Context(), parseFilters(q, SortNewest), ParseView(q.Get("view")))
	if err != nil {
		h.log.ErrorContext(r.Context(), "load every product", "error", err)
		h.serverError(w, r)
		return
	}
	h.listing(w, r, &view)
}

func (h *Handler) listing(w http.ResponseWriter, r *http.Request, view *ListingView) {
	if redirectUnknownFilters(w, r, &view.Shelf) || redirectPastLastPage(w, r, int(view.Page), view.Pages()) {
		return
	}
	if respond.IsHTMX(r) {
		r = r.WithContext(AsPartial(r.Context()))
	}
	respond.Component(w, r, h.log, http.StatusOK, Listing(ListingMeta(r.Context(), view), view))
}

const newestOnEmpty = 4

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	if dropEmptyParams(w, r, "q") {
		return
	}
	q := r.URL.Query()
	pattern := SearchPattern(q.Get("q"))
	f := parseFilters(q, SortRelevance)
	f.Department = boundedSlug(q.Get("c"))

	view := SearchView{Query: trimForDisplay(q.Get("q"))}
	if pattern != "" {
		found, err := h.store.SearchPage(r.Context(), view.Query, f, ParseView(q.Get("view")))
		if err != nil {
			h.log.ErrorContext(r.Context(), "search", "error", err)
			h.serverError(w, r)
			return
		}
		view = found
	}

	if view.Searched() && !view.Unmatched() && redirectUnknownFilters(w, r, &view.Shelf) {
		return
	}
	if redirectPastLastPage(w, r, int(view.Page), view.Pages()) {
		return
	}
	if !view.Searched() || view.Unmatched() {
		// Best effort: the page says what to do next without them, and the
		// department chips below it still lead somewhere.
		newest, err := h.store.NewestProducts(r.Context(), newestOnEmpty)
		if err != nil {
			h.log.ErrorContext(r.Context(), "newest products for a search with nothing to show", "error", err)
		}
		view.Newest = newest
	}
	if respond.IsHTMX(r) {
		r = r.WithContext(AsPartial(r.Context()))
	}
	respond.Component(w, r, h.log, http.StatusOK, Search(SearchMeta(r.Context(), view.Query), &view))
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	respond.Component(w, r, h.log, http.StatusNotFound, frame.Notice(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyCategoryNotFound)}, "404",
		i18n.T(r.Context(), i18n.KeyCategoryNotFound),
		i18n.T(r.Context(), i18n.KeyCategoryNotFoundBody)))
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	respond.Component(w, r, h.log, http.StatusInternalServerError, frame.Notice(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyCannotLoad)}, "500",
		i18n.T(r.Context(), i18n.KeyCannotLoad),
		i18n.T(r.Context(), i18n.KeyCannotLoadListing)))
}

// parseFilters bounds or discards every value, so nothing downstream checks
// again. unchosen is the page's own order, which no address names.
func parseFilters(q url.Values, unchosen Sort) Filters {
	return Filters{
		BrandSlugs:   boundedBrands(q["brand"]),
		OptionValues: boundedOptionValues(q["opt"]),
		InStockOnly:  q.Get("in_stock") == "1",
		OnSaleOnly:   q.Get("sale") == "1",
		Price:        parsePriceBand(q.Get("price")),
		Sort:         ParseSort(q.Get("sort"), unchosen),
		Page:         ParsePage(q.Get("page")),
	}
}

// maxSlugRunes is longer than any slug the catalogue writes; a longer value
// names no department and is dropped before it reaches a query.
const maxSlugRunes = 64

func boundedSlug(s string) string {
	if utf8.RuneCountInString(s) > maxSlugRunes {
		return ""
	}
	return s
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
		frame.Page{
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

// redirectUnknownFilters sends an address naming a brand, an option value or a
// search's department the shelf does not offer to the same address without it.
func redirectUnknownFilters(w http.ResponseWriter, r *http.Request, s *Shelf) bool {
	q := r.URL.Query()
	if len(q["brand"]) == 0 && len(q["opt"]) == 0 && len(q["c"]) == 0 {
		return false
	}
	known := offeredFilters(s)
	changed := false
	for _, param := range []string{"brand", "opt", "c"} {
		kept := make([]string, 0, len(q[param]))
		for _, value := range q[param] {
			// An empty value is an untouched field, which dropEmptyParams owns:
			// a search's 「全部結果」 sends c= with every change.
			if value == "" {
				continue
			}
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

// offeredFilters is every value of brand, opt and c the shelf offers, by
// parameter. Facets include zero-count choices, so an existing value that the
// other filters exclude remains a legitimate filter.
func offeredFilters(s *Shelf) map[string]map[string]bool {
	known := map[string]map[string]bool{"brand": {}, "opt": {}, "c": {}}
	for _, group := range s.Facets {
		choices, relevant := known[group.Param()]
		if !relevant {
			continue
		}
		for _, option := range group.Options {
			choices[option.Value] = true
		}
	}
	if s.Searching {
		for _, d := range s.Departments {
			known["c"][d.Slug] = true
		}
	}
	return known
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
