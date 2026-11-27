package home

import (
	"log/slog"
	"net/http"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	store *Store
	log   *slog.Logger
	// compare is the catalogue's compare toggle, which the featured shelf's
	// cards carry where the shelf compares. It is handed in rather than
	// imported: the compare set is the catalogue's, and the home page imports no
	// feature that serves pages of its own.
	compare func(slug, name string) templ.Component
}

func NewHandler(store *Store, log *slog.Logger, compare func(slug, name string) templ.Component) *Handler {
	if store == nil || log == nil || compare == nil {
		panic("home: NewHandler requires a store, a logger and the compare toggle")
	}
	return &Handler{store: store, log: log, compare: compare}
}

// Routes registers the home page.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.Index)
}

// Index draws the whole page for every request, the shelf and showcase pills'
// htmx swaps included: each takes its own section out of the page a link
// would load, so the two can never disagree.
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	view, err := h.store.Load(r.Context(), Choice{Shelf: slugParam(q.Get("shelf")), More: slugParam(q.Get("more"))})
	if err != nil {
		h.log.Error("load home page", "error", err)
		respond.Component(w, r, h.log, http.StatusInternalServerError, frame.Notice(
			frame.Page{Title: i18n.T(r.Context(), i18n.KeyHome)}, "",
			i18n.T(r.Context(), i18n.KeyCannotLoad),
			i18n.T(r.Context(), i18n.KeyCannotLoadHome)))
		return
	}
	view.AccountErased = q.Get("erased") == "1"
	if view.Shelf != nil && view.Shelf.Compares {
		for i := range view.Shelf.Tiles {
			t := &view.Shelf.Tiles[i]
			t.CompareControl = h.compare(t.Slug, t.Name)
		}
	}
	respond.Component(w, r, h.log, http.StatusOK, Page(Meta(r.Context()), view))
}

// slugParam is a category slug as a query names it, or "" for anything that
// cannot be one; the store matches it against what the page offers.
func slugParam(s string) string {
	if s == "" || len(s) > 120 {
		return ""
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return ""
		}
	}
	return s
}
