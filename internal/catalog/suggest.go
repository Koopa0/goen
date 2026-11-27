package catalog

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/web/respond"
)

// suggestLimit is how many products the search field lists as the shopper
// types: enough to recognise the one meant, few enough to read at a glance.
const suggestLimit = 6

// SuggestView is what the search field lists under itself: the first products
// the search page would list for the same words, and how many it would list.
type SuggestView struct {
	Query    string
	Products []tile.Tile
	Total    int64
}

// SearchHref is the search page for the same words, where every result is.
func (v SuggestView) SearchHref() string {
	return "/search?q=" + url.QueryEscape(v.Query)
}

// suggestPrice is a product's price as the list shows it, "from" where its
// variants differ.
func suggestPrice(ctx context.Context, cents int64, varies bool) string {
	price := money.TWD(cents)
	if varies {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyFromPrice), price)
	}
	return price
}

// Suggest answers the search field as the shopper types: the same search the
// search page runs, its first few products, as a fragment the field swaps in
// under itself. Words that search nothing answer an empty list rather than
// 204: htmx swaps no 204, and the list of the previous words would stay.
func (h *Handler) Suggest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	view := SuggestView{Query: trimForDisplay(q)}
	if pattern := SearchPattern(q); pattern != "" {
		found, err := h.store.Search(r.Context(), pattern, SortRelevance, 1)
		if err != nil {
			h.log.ErrorContext(r.Context(), "suggest", "error", err)
			h.serverError(w, r)
			return
		}
		view.Total = found.Total
		view.Products = found.Products[:min(len(found.Products), suggestLimit)]
	} else {
		view.Query = ""
	}
	respond.Component(w, r, h.log, http.StatusOK, Suggestions(view))
}
