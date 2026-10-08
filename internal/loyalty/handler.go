package loyalty

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store *Store
	log   *slog.Logger
	// Rotation discards a displayed confirmation, never a ledger entry.
	confirmationKey string
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("loyalty: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log, confirmationKey: rand.Text()}
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin?next=/account/points", http.StatusSeeOther)
		return
	}
	view, err := h.store.History(r.Context(), u.ID, r.URL.Query().Get(web.KeysetParam))
	if err != nil && !errors.Is(err, ErrNoAccount) {
		h.log.ErrorContext(r.Context(), "read points", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = h.noticeFor(r, u.ID)
	if view.CanRedeem() {
		view.OperationID = uuid.NewString()
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Points(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyPointsTitle)}, view))
}

func (h *Handler) Redeem(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	// The form names POINTS: a request supplying cents would choose the rate.
	points, parseErr := strconv.ParseInt(r.PostFormValue("points"), 10, 64)
	if parseErr != nil {
		points = 0
	}
	operationID, operationErr := uuid.Parse(r.PostFormValue("operation_id"))
	if operationErr != nil || operationID == uuid.Nil {
		operationID = uuid.Nil
	}

	cents, err := h.store.Redeem(
		r.Context(), u.ID, points, operationID,
	)
	switch {
	case err == nil:
		token := encodeRedemptionConfirmation(h.confirmationKey, u.ID, redemptionConfirmation{OperationID: operationID, Points: points, CreditCents: cents, IssuedAt: time.Now().Unix()})
		http.Redirect(w, r, "/account/points?redeemed="+token, http.StatusSeeOther)
	case errors.Is(err, ErrTooSmall):
		h.renderFormRefusal(w, r, u.ID, operationID, pointsAmountReason(r))
	case errors.Is(err, ErrInvalidOperation):
		h.renderFormRefusal(w, r, u.ID, operationID, i18n.T(r.Context(), i18n.KeyPointsBadForm))
	case errors.Is(err, ErrReturnUnsettled):
		h.renderRefusal(w, r, u.ID, i18n.KeyPointsReturnUnsettled)
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

// renderRefusal answers 422 on the points page so the refusal is read where the
// form is instead of after a redirect.
func (h *Handler) renderRefusal(w http.ResponseWriter, r *http.Request, userID string, reason i18n.Key) {
	view, err := h.store.History(r.Context(), userID, "")
	if err != nil && !errors.Is(err, ErrNoAccount) {
		h.log.ErrorContext(r.Context(), "read points after refusal", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = i18n.T(r.Context(), reason)
	if view.CanRedeem() {
		view.OperationID = uuid.NewString()
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.Points(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyPointsTitle)}, view))
}

func (h *Handler) noticeFor(r *http.Request, owner string) string {
	ctx := r.Context()
	switch {
	case r.URL.Query().Get("redeemed") != "":
		result, valid := readRedemptionConfirmation(h.confirmationKey, owner, r.URL.Query().Get("redeemed"), time.Now())
		if !valid {
			return ""
		}
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyPointsRedeemed), strconv.FormatInt(result.Points, 10), money.TWD(result.CreditCents))
	case r.URL.Query().Get("short") == "1":
		return i18n.T(ctx, i18n.KeyPointsShort)
	default:
		return ""
	}
}

func pointsAmountReason(r *http.Request) string {
	return fmt.Sprintf(i18n.T(r.Context(), i18n.KeyPointsBadAmount), strconv.FormatInt(MinRedemption, 10), strconv.FormatInt(PointsPerCredit, 10))
}

func (h *Handler) renderFormRefusal(w http.ResponseWriter, r *http.Request, owner string, operation uuid.UUID, reason string) {
	view, err := h.store.History(r.Context(), owner, "")
	if err != nil && !errors.Is(err, ErrNoAccount) {
		h.log.ErrorContext(r.Context(), "read points after form refusal", "error", err)
		h.serverError(w, r)
		return
	}
	raw := r.PostFormValue("points")
	view.DraftPoints = &raw
	view.FieldError = reason
	if operation != uuid.Nil {
		view.OperationID = operation.String()
	} else {
		view.OperationID = uuid.NewString()
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.Points(layouts.Page{Title: i18n.T(r.Context(), i18n.KeyPointsTitle)}, view))
}
