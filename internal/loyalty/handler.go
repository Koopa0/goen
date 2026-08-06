package loyalty

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Handler serves the points page.
type Handler struct {
	store *Store
	log   *slog.Logger
}

// NewHandler returns a Handler over store.
func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("loyalty: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

// Page serves GET /account/points.
func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	u, ok := account.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin?next=/account/points", http.StatusSeeOther)
		return
	}
	view, err := h.store.History(r.Context(), u.ID)
	if err != nil && !errors.Is(err, ErrNoAccount) {
		h.log.ErrorContext(r.Context(), "read points", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.Points(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyPointsTitle)}, view))
}

// Redeem serves POST /account/points.
func (h *Handler) Redeem(w http.ResponseWriter, r *http.Request) {
	u, ok := account.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	// The form names POINTS, never an amount of credit: a request supplying the
	// cents would be a request choosing the exchange rate.
	points, parseErr := strconv.ParseInt(r.PostFormValue("points"), 10, 64)
	if parseErr != nil {
		points = 0
	}

	switch _, err := h.store.Redeem(r.Context(), u.ID, points); {
	case err == nil:
		http.Redirect(w, r, "/account/points?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrTooSmall):
		http.Redirect(w, r, "/account/points?small=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotEnough), errors.Is(err, ErrNoAccount):
		http.Redirect(w, r, "/account/points?short=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "redeem points", "error", err)
		h.serverError(w, r)
	}
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyTryAgainBody)))
}

func noticeFor(r *http.Request) string {
	ctx := r.Context()
	switch {
	case r.URL.Query().Get("ok") == "1":
		return i18n.T(ctx, i18n.KeyPointsRedeemed)
	case r.URL.Query().Get("small") == "1":
		return i18n.T(ctx, i18n.KeyPointsBadAmount)
	case r.URL.Query().Get("short") == "1":
		return i18n.T(ctx, i18n.KeyPointsShort)
	default:
		return ""
	}
}
