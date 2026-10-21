package reviews

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/web"
	"github.com/koopa0/goen/internal/web/formtext"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("reviews: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/reviews", ac.RequireStaff(h.Reviews))
	mux.HandleFunc("POST /admin/reviews/hide", ac.RequireStaff(h.HideReview))
	mux.HandleFunc("POST /admin/reviews/show", ac.RequireStaff(h.ShowReview))
}

var notices = map[string]components.NoticeEntry{
	"ok":   components.NoticeDone(i18n.KeyAdminNoticeOK),
	"gone": components.NoticeRefused(i18n.KeyAdminNoticeGone),
}

func (h *Handler) Reviews(w http.ResponseWriter, r *http.Request) {
	filter := AllReviews
	if r.URL.Query().Get("rating") == "3" {
		filter = ThreeStarsAndBelowReviews
	}
	view, err := h.store.Reviews(r.Context(), filter, r.URL.Query().Get(keyset.Param))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read reviews", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = components.NoticeFrom(r, notices)
	web.Render(w, r, h.log, http.StatusOK, Reviews(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageReviews)}, view))
}

func (h *Handler) HideReview(w http.ResponseWriter, r *http.Request) {
	h.setReviewHidden(w, r, true)
}

func (h *Handler) ShowReview(w http.ResponseWriter, r *http.Request) {
	h.setReviewHidden(w, r, false)
}

func (h *Handler) setReviewHidden(w http.ResponseWriter, r *http.Request, hidden bool) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	switch err := h.store.SetReviewHidden(r.Context(), r.PostFormValue("review"), hidden); {
	case err == nil:
		http.Redirect(w, r, "/admin/reviews?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		http.Redirect(w, r, "/admin/reviews?gone=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set review hidden", "error", err)
		access.ServerError(w, r, h.log)
	}
}
