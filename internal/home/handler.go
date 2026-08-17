package home

import (
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

const recommendedCount = 8

// Handler serves the storefront home page.
type Handler struct {
	store *Store
	log   *slog.Logger
	// secure selects the dismissal cookie's name.
	secure bool
}

// NewHandler returns a Handler reading through store.
func NewHandler(store *Store, log *slog.Logger, secure bool) *Handler {
	if store == nil || log == nil {
		panic("home: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log, secure: secure}
}

// Home renders the home page.
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Load(r.Context(), recommendedCount)
	if err != nil {
		h.log.Error("load home page", "error", err)
		web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyHome)}, "",
			i18n.T(r.Context(), i18n.KeyCannotLoad),
			i18n.T(r.Context(), i18n.KeyCannotLoadHome)))
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Home(pages.HomeMeta(r.Context()), view))
}
