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
//
// A slug naming no category is a 404 with the site's not-found page, not an
// empty listing: an empty listing tells a visitor the category exists and
// happens to be bare, which is a different and false statement.
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
//
// A blank term is the page itself rather than a redirect or an error: someone
// pressing enter on an empty box gets the search page back, with the prompt
// still there.
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
// discarded here, so nothing downstream has to wonder whether it was checked.
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

// maxBrandFilters caps how many brand values one URL may carry. Unknown slugs
// are dropped by the store anyway, but an unbounded list would still be built
// into an array parameter first.
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

// canonicalQuery rebuilds the listing's query string from the PARSED filters,
// not from the request.
//
// Rebuilding is what makes a pagination link safe: the request's own query
// string can carry anything, and echoing it into an href would put a visitor's
// text back into the page's links. Everything here has already been validated
// or clamped, and url.Values.Encode sorts the keys, so the same filters always
// produce the same URL.
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

// trimForDisplay bounds what a search term may be echoed back as, so a very
// long query cannot become a very long heading.
func trimForDisplay(q string) string {
	r := []rune(q)
	// Trim leading and trailing whitespace the same way SearchPattern does, so
	// the heading and the search agree about what was searched for.
	for len(r) > 0 && (r[0] == ' ' || r[0] == '\t' || r[0] == '\n' || r[0] == '\r') {
		r = r[1:]
	}
	for len(r) > 0 {
		last := r[len(r)-1]
		if last != ' ' && last != '\t' && last != '\n' && last != '\r' {
			break
		}
		r = r[:len(r)-1]
	}
	if len(r) > MaxQueryRunes {
		r = r[:MaxQueryRunes]
	}
	return string(r)
}

// Deals serves GET /deals.
//
// It renders the search page, because a deals page IS a search results page
// with the query fixed: a heading, a grid, a pager. The header and footer have
// linked here since the chrome was built and it answered 404 on every page.
func (h *Handler) Deals(w http.ResponseWriter, r *http.Request) {
	page := ParsePage(r.URL.Query().Get("page"))
	view, err := h.store.Deals(r.Context(), page)
	if err != nil {
		h.log.ErrorContext(r.Context(), "load deals", "error", err)
		h.serverError(w, r)
		return
	}
	// Running promotions lead. A campaign is a curated, ending thing and a
	// marked-down product is a standing one — a shopper reading a deals page
	// wants the first before the second.
	campaigns, err := h.store.RunningCampaigns(r.Context())
	if err != nil {
		// Not fatal: the discounted products are the page's substance, and
		// losing the campaign strip is smaller than losing the page.
		h.log.ErrorContext(r.Context(), "load campaigns", "error", err)
	}
	view.Campaigns = campaigns
	web.Render(w, r, h.log, http.StatusOK, pages.Deals(pages.DealsMeta(r.Context()), view))
}

// Campaign serves GET /s/{slug}.
//
// A campaign outside its window is the 404 an unknown slug gets. That is the
// honest answer: the promotion is over, and a page saying so would be a page
// somebody keeps linking to.
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

// Compare serves GET /compare.
//
// The comparison set lives in the URL — `?p=a&p=b` — and nowhere else. No
// cookie, no session row, no write of any kind: the result is shareable,
// bookmarkable, and there is no state to expire or to get out of step with what
// the page shows.
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
