package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ordernotice"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

// refundBeforeShipment reports whether the order page offers a refund before
// shipment, and whether one is open for Resume. open_refund_before_shipment
// re-derives both under the order lock.
func refundBeforeShipment(r *db.BeforeShipmentRefundRow) (offered, open bool) {
	status := pages.FulfillmentStatus(r.FulfillmentStatus)
	open = r.ReturnRequestID.Valid && returns.ReturnStatus(r.ReturnStatus) == returns.ReturnApproved
	offered = r.Committed && !r.Shipped && !r.HasReturn &&
		(status == pages.FulfillmentPending || status == pages.FulfillmentPicking)
	return offered, open
}

// fillRefundBeforeShipment offers the refund on the order page, and keeps the
// status form from cancelling a paid order or picking one being refunded.
func (s *Store) fillRefundBeforeShipment(ctx context.Context, view *admin.OrderView, number string) error {
	refund, err := s.q.BeforeShipmentRefund(ctx, number)
	if err != nil {
		return fmt.Errorf("read refund before shipment of %s: %w", number, err)
	}
	view.RefundOffered, view.RefundOpen = refundBeforeShipment(&refund)
	if refund.ReturnRequestID.Valid {
		view.CanShip = false
	}
	for _, n := range NextStatuses(view.Status) {
		if (n == pages.FulfillmentCancelled && view.Funded) ||
			(n == pages.FulfillmentPicking && (refund.ReturnRequestID.Valid || view.Unpaid)) ||
			// orders_finished_when_shipped: an order that still owes a parcel is
			// not finished, and Shippable is what is still outstanding.
			(n == pages.FulfillmentCompleted && len(view.Shippable) > 0) {
			continue
		}
		view.Next = append(view.Next, admin.Transition{Value: n, Label: StatusLabel(ctx, n)})
	}
	return nil
}

// RefundPreview reads what a refund before shipment of this order pays: the
// frozen split once one is open, otherwise the split its approval would freeze.
func (s *Store) RefundPreview(ctx context.Context, number string) (admin.RefundConfirmation, error) {
	row, err := s.q.BeforeShipmentRefund(ctx, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return admin.RefundConfirmation{}, ErrNotFound
	}
	if err != nil {
		return admin.RefundConfirmation{}, fmt.Errorf("read refund before shipment of %s: %w", number, err)
	}
	offered, open := refundBeforeShipment(&row)
	if !offered && !open {
		return admin.RefundConfirmation{}, fmt.Errorf(
			"%w: order %s has no refund before shipment to confirm", ErrRefused, number)
	}
	return admin.RefundConfirmation{
		OrderNumber: number, TotalCents: row.TotalCents,
		CardCents: row.CardCents, CreditCents: row.CreditCents, Resume: open,
	}, nil
}

// RefundBeforeShipment refunds a paid order nothing has shipped from, in full,
// and cancels it once the money has settled and the invoice is resolved. Each
// step is durable before the next, so pressing it again resumes the first one
// that did not finish. The Checkout Sessions returned are the cancelled order's,
// for the caller to close at Stripe.
func (s *Store) RefundBeforeShipment(ctx context.Context, number, reason string) ([]string, error) {
	if !validReturnResolution(reason) {
		return nil, fmt.Errorf("%w: refund reason exceeds %d characters",
			ErrInvalid, maxReturnResolutionRunes)
	}
	actorID, ok := actorFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("%w: refund before shipment", ErrNoActor)
	}
	actor := uuid.NullUUID{UUID: actorID, Valid: true}

	returnID, err := s.q.OpenRefundBeforeShipment(ctx, db.OpenRefundBeforeShipmentParams{
		OrderNumber: number, Reason: strings.TrimSpace(reason),
		ActorUserID: actorID, RequestID: web.RequestID(ctx),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: open refund before shipment of %s: %w", ErrRefused, number, err)
	}

	row, err := s.q.ReturnForDecision(ctx, returnID)
	if err != nil {
		return nil, fmt.Errorf("read refund %s of %s: %w", returnID, number, err)
	}
	position, err := s.refundPosition(ctx, returnID)
	if err != nil {
		return nil, err
	}
	if _, payErr := s.payOutstanding(ctx, &row, position, actor); payErr != nil {
		return nil, payErr
	}
	// Read again: a card refund Stripe accepted as pending returns no error and
	// has still not moved the money.
	if position, err = s.refundPosition(ctx, returnID); err != nil {
		return nil, err
	}
	if !position.MoneySettled || position.EventOutstanding || position.PointsOutstanding {
		return nil, ErrRefundUnsettled
	}
	return s.finishRefundBeforeShipment(ctx, number, returnID, actor)
}

func (s *Store) refundPosition(ctx context.Context, returnID uuid.UUID) (returnPayoutPosition, error) {
	facts, err := s.returnPayoutFact(ctx, returnID)
	if err != nil {
		return returnPayoutPosition{}, err
	}
	return facts.position()
}

// finishRefundBeforeShipment cancels the order, returns its held stock and
// closes the refund, in ONE transaction. orders_check_transition admits the
// cancellation only once the refund has settled and the invoice is resolved,
// and the return completes only once the order is cancelled. The stock is
// released, never restocked: nothing shipped, so the holds are still held.
func (s *Store) finishRefundBeforeShipment(
	ctx context.Context, number string, returnID uuid.UUID, actor uuid.NullUUID,
) ([]string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin refund before shipment: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	row, err := q.LockOrderForAdvance(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	// Cancelled is terminal and this transaction is the only one that cancels
	// a refunded order, so an earlier press already finished everything below.
	if pages.FulfillmentStatus(row.FulfillmentStatus) == pages.FulfillmentCancelled {
		return nil, nil
	}
	if cancelErr := cancelRefundedOrder(ctx, q, number, returnID); cancelErr != nil {
		return nil, cancelErr
	}
	if recordErr := recordStaffCancellation(ctx, q, row.ID, number, returnID, actor); recordErr != nil {
		return nil, recordErr
	}
	sessions, err := q.OpenSessionsForOrder(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("read open checkout sessions of %s: %w", number, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit refund before shipment: %w", err)
	}
	return sessions, nil
}

func cancelRefundedOrder(ctx context.Context, q *db.Queries, number string, returnID uuid.UUID) error {
	if err := q.AdvanceOrder(ctx, db.AdvanceOrderParams{
		OrderNumber: number, Status: string(pages.FulfillmentCancelled),
	}); err != nil {
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}
	held, err := q.HeldReservationsForOrder(ctx, number)
	if err != nil {
		return fmt.Errorf("read holds of %s: %w", number, err)
	}
	for _, id := range held {
		if releaseErr := q.ReleaseReservation(ctx, id); releaseErr != nil {
			return fmt.Errorf("release hold %s of %s: %w", id, number, releaseErr)
		}
	}
	if _, closeErr := q.CloseUnshippedReturnLines(ctx, returnID); closeErr != nil {
		return fmt.Errorf("%w: close the lines of refund %s: %w", ErrRefused, returnID, closeErr)
	}
	closed, err := q.CompleteReturn(ctx, db.CompleteReturnParams{ID: returnID})
	if err != nil {
		return fmt.Errorf("%w: complete refund %s: %w", ErrRefused, returnID, err)
	}
	if closed == 0 {
		return fmt.Errorf("%w: refund %s is not open for completion", ErrRefused, returnID)
	}
	return nil
}

// recordStaffCancellation leaves what any back-office cancellation leaves: the
// timeline entry, the audit row and the customer's notice.
func recordStaffCancellation(
	ctx context.Context, q *db.Queries, orderID uuid.UUID, number string, returnID uuid.UUID, actor uuid.NullUUID,
) error {
	kind, err := eventKindFor(pages.FulfillmentCancelled)
	if err != nil {
		return err
	}
	err = q.RecordOrderEvent(ctx, db.RecordOrderEventParams{
		OrderID: orderID, Kind: kind, ActorUserID: actor,
	})
	if err != nil {
		return fmt.Errorf("record order event: %w", err)
	}
	err = auditIn(ctx, q, Event{
		Action: actionAdvanceOrder, Table: "orders", ID: nullableID(orderID),
		After: map[string]any{
			"number": number, "status": string(pages.FulfillmentCancelled),
			"return_request_id": returnID.String(),
		},
	})
	if err != nil {
		return err
	}
	// The cancellation is admitted only once the refund settled, so what went
	// back is read here rather than assumed.
	refundedCents, err := q.SettledRefundsForOrder(ctx, number)
	if err != nil {
		return fmt.Errorf("read settled refunds for %s: %w", number, err)
	}
	return ordernotice.Enqueue(ctx, q, ordernotice.Message{
		OrderID: orderID, Kind: ordernotice.CancelledByStaff, Refunded: refundedCents > 0,
	})
}

// RefundBeforeShipment serves POST /admin/orders/{number}/refund. The first
// POST only renders what the refund pays; the confirmed one moves money.
func (h *Handler) RefundBeforeShipment(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !IsOrderNumber(number) {
		http.NotFound(w, r)
		return
	}
	if h.confirmRefundBeforeShipment(w, r, number) {
		return
	}
	back := "/admin/orders/" + number
	sessions, err := h.store.RefundBeforeShipment(r.Context(), number, r.PostFormValue("reason"))
	switch {
	case err == nil:
		h.closeSessions(r.Context(), number, sessions)
		http.Redirect(w, r, back+"?refunded=1", http.StatusSeeOther) //nolint:gosec // G710: validated by IsOrderNumber
	case errors.Is(err, ErrRefundUnsettled):
		http.Redirect(w, r, back+"?refundpending=1", http.StatusSeeOther) //nolint:gosec // G710: validated by IsOrderNumber
	case hasConstraint(err, "orders_cancel_invoice_resolved"):
		http.Redirect(w, r, back+"?cancelinvoice=1", http.StatusSeeOther) //nolint:gosec // G710: validated by IsOrderNumber
	case errors.Is(err, ErrRefundIncomplete):
		// Tested before ErrRefused: a payout may carry a database refusal as its
		// cause, but the refund is open and Resume is what the staff member needs.
		h.log.ErrorContext(r.Context(), "refund before shipment", "order", number, "error", err)
		http.Redirect(w, r, back+"?refundretry=1", http.StatusSeeOther) //nolint:gosec // G710: validated by IsOrderNumber
	case errors.Is(err, ErrRefused), errors.Is(err, ErrInvalid):
		h.log.WarnContext(r.Context(), "refund before shipment refused", "order", number, "error", err)
		http.Redirect(w, r, back+"?refused=1", http.StatusSeeOther) //nolint:gosec // G710: validated by IsOrderNumber
	default:
		h.log.ErrorContext(r.Context(), "refund before shipment", "order", number, "error", err)
		h.serverError(w, r)
	}
}

// confirmRefundBeforeShipment renders the refund before it moves money, and
// again when the POST did not repeat the total it was shown. It reports false
// once the caller may proceed.
func (h *Handler) confirmRefundBeforeShipment(w http.ResponseWriter, r *http.Request, number string) bool {
	view, err := h.store.RefundPreview(r.Context(), number)
	switch {
	case errors.Is(err, ErrNotFound):
		h.notFound(w, r)
		return true
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "refund before shipment not offered", "order", number, "error", err)
		http.Redirect(w, r, "/admin/orders/"+number+"?refused=1", http.StatusSeeOther) //nolint:gosec // G710: validated by IsOrderNumber
		return true
	case err != nil:
		h.log.ErrorContext(r.Context(), "read refund before shipment", "order", number, "error", err)
		h.serverError(w, r)
		return true
	}
	view.Reason = r.PostFormValue("reason")
	status := http.StatusOK
	switch {
	case r.PostFormValue("confirm") != "refund" ||
		r.PostFormValue("total") != strconv.FormatInt(view.TotalCents, 10):
	case !view.Resume && (strings.TrimSpace(view.Reason) == "" || !validReturnResolution(view.Reason)):
		// The reason becomes the provider refund's and the audit's; a resume
		// reuses the one already recorded.
		view.ReasonInvalid = true
		status = http.StatusUnprocessableEntity
	default:
		return false
	}
	web.Render(w, r, h.log, status, admin.ConfirmRefund(layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminRefundTitle)}, view))
	return true
}
