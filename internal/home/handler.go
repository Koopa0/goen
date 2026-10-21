package home

import (
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/web"
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

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Load(r.Context())
	if err != nil {
		h.log.Error("load home page", "error", err)
		web.Render(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyHome)}, "",
			i18n.T(r.Context(), i18n.KeyCannotLoad),
			i18n.T(r.Context(), i18n.KeyCannotLoadHome)))
		return
	}
	view.AccountErased = r.URL.Query().Get("erased") == "1"
	web.Render(w, r, h.log, http.StatusOK, Page(Meta(r.Context()), view))
}
