package home

import (
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("home: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
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
