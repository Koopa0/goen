package tiers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("tiers: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/tiers", ac.RequireStaff(h.Tiers))
	mux.HandleFunc("POST /admin/tiers", ac.RequireStaff(h.CreateTier))
	mux.HandleFunc("POST /admin/tiers/delete", ac.RequireStaff(h.DeleteTier))
}

var notices = map[string]widget.NoticeEntry{
	"ok":         widget.NoticeDone(i18n.KeyAdminNoticeOK),
	"refused":    widget.NoticeRefused(i18n.KeyAdminNoticeRefused),
	"tiersneeds": widget.NoticeRefused(i18n.KeyAdminNoticeTiersNeeds),
}

func (h *Handler) Tiers(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Tiers(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read membership tiers", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = widget.NoticeFrom(r, notices)
	respond.Component(w, r, h.log, http.StatusOK, Tiers(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageTiers)}, view))
}

func (h *Handler) CreateTier(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	threshold, tErr := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("threshold")), 10, 64)
	percent, pErr := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("percent")), 10, 64)
	if tErr != nil || pErr != nil {
		http.Redirect(w, r, "/admin/tiers?tiersneeds=1", http.StatusSeeOther)
		return
	}
	err := h.store.CreateTier(r.Context(), r.PostFormValue("code"),
		r.PostFormValue("name"), r.PostFormValue("name_en"), threshold, percent)
	h.redirectTiers(w, r, err)
}

func (h *Handler) DeleteTier(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	h.redirectTiers(w, r, h.store.DeleteTier(r.Context(), r.PostFormValue("tier")))
}

func (h *Handler) redirectTiers(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/tiers?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrNotFound):
		http.Redirect(w, r, "/admin/tiers?tiersneeds=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "tier change refused", "error", err)
		http.Redirect(w, r, "/admin/tiers?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "change membership tiers", "error", err)
		access.ServerError(w, r, h.log)
	}
}
