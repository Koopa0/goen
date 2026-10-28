package reviews

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/frame"
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
		panic("reviews: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *admin.Control) {
	mux.HandleFunc("GET /admin/reviews", ac.RequireStaff(h.Reviews))
	mux.HandleFunc("POST /admin/reviews/hide", ac.RequireStaff(h.HideReview))
	mux.HandleFunc("POST /admin/reviews/show", ac.RequireStaff(h.ShowReview))
}

var notices = map[string]widget.NoticeEntry{
	"hidden": widget.NoticeDone(i18n.KeyAdminNoticeReviewHidden),
	"shown":  widget.NoticeDone(i18n.KeyAdminNoticeReviewShown),
	"gone":   widget.NoticeRefused(i18n.KeyAdminNoticeGone),
}

func (h *Handler) Reviews(w http.ResponseWriter, r *http.Request) {
	filter := FilterAll
	if r.URL.Query().Get("rating") == "3" {
		filter = FilterThreeStarsAndBelow
	}
	view, err := h.store.Reviews(r.Context(), filter, r.URL.Query().Get(keyset.Param))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read reviews", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	view.Notice = widget.NoticeFrom(r, notices)
	respond.Component(w, r, h.log, http.StatusOK, Reviews(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminQueueReviews)}, view))
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
		http.Redirect(w, r, hiddenTarget(hidden), http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		http.Redirect(w, r, "/admin/reviews?gone=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set review hidden", "error", err)
		admin.ServerError(w, r, h.log)
	}
}

// hiddenTarget is where a write that took goes: the page says which change it was.
func hiddenTarget(hidden bool) string {
	if hidden {
		return "/admin/reviews?hidden=1"
	}
	return "/admin/reviews?shown=1"
}
