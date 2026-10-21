package campaign

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/web/respond"
)

// Handler serves a campaign's page and /deals.
type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("campaign: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

// Routes registers /deals and a campaign's page.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /deals", h.Deals)
	mux.HandleFunc("GET /s/{slug}", h.Campaign)
}

func (h *Handler) Deals(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r.URL.Query().Get("page"))
	view, err := h.store.Deals(r.Context(), page)
	if err != nil {
		h.log.ErrorContext(r.Context(), "load deals", "error", err)
		h.serverError(w, r)
		return
	}
	campaigns, err := h.store.Listed(r.Context(), parsePage(r.URL.Query().Get("campaign_page")))
	if err != nil {
		// Best effort: the discounted products are the page's substance.
		h.log.ErrorContext(r.Context(), "load campaigns", "error", err)
	}
	view.Campaigns = campaigns
	respond.Component(w, r, h.log, http.StatusOK, Deals(DealsMeta(r.Context()), view))
}

// Campaign answers 404 for a campaign that is switched off or does not exist.
func (h *Handler) Campaign(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	view, err := h.store.Campaign(r.Context(), slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			respond.Component(w, r, h.log, http.StatusNotFound, layouts.Notice(
				layouts.Page{Title: i18n.T(r.Context(), i18n.KeyCampaignNotFound)}, "404",
				i18n.T(r.Context(), i18n.KeyCampaignNotFound),
				i18n.T(r.Context(), i18n.KeyCampaignNotFoundBody),
				layouts.NoticeActions{Primary: layouts.NoticeLink{Href: "/deals", Label: i18n.KeyCurrentDeals}}))
			return
		}
		h.log.ErrorContext(r.Context(), "load campaign", "error", err, "slug", slug)
		h.serverError(w, r)
		return
	}
	respond.Component(w, r, h.log, http.StatusOK, Page(Meta(r.Context(), view.Title, view.Image), view))
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	respond.Component(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyCannotLoad)}, "500",
		i18n.T(r.Context(), i18n.KeyCannotLoad),
		i18n.T(r.Context(), i18n.KeyCannotLoadListing)))
}
