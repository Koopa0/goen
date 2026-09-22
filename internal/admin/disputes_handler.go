package admin

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

var validDisputeDispositions = map[string]bool{
	"monitoring":  true,
	"accepted":    true,
	"challenging": true,
	"closed":      true,
}

// Disputes serves GET /admin/disputes.
func (h *Handler) Disputes(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.Disputes(r.Context(), time.Now())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read dispute queue", "error", err)
		h.serverError(w, r)
		return
	}
	view := pages.AdminDisputesView{
		Rows:   rows,
		Notice: noticeFor(r),
	}
	web.Render(w, r, h.log, http.StatusOK, pages.AdminDisputes(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageDisputes)}, view))
}

// ReviewDispute serves POST /admin/disputes/{id}/review.
func (h *Handler) ReviewDispute(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	disposition := r.PostFormValue("disposition")
	if !validDisputeDispositions[disposition] {
		http.Redirect(w, r, "/admin/disputes?refused=1", http.StatusSeeOther)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Redirect(w, r, "/admin/disputes?refused=1", http.StatusSeeOther)
		return
	}
	if err := h.store.ReviewDispute(r.Context(), id, disposition); err != nil {
		if errors.Is(err, ErrInvalid) || errors.Is(err, ErrRefused) || errors.Is(err, ErrNoActor) {
			http.Redirect(w, r, "/admin/disputes?refused=1", http.StatusSeeOther)
			return
		}
		h.log.ErrorContext(r.Context(), "review dispute", "dispute", id, "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/admin/disputes?ok=1", http.StatusSeeOther)
}
