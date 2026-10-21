package refunds

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/order/cancellation"
	"github.com/koopa0/goen/internal/order/notice"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/refund"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/web"
)

// beforeShipment is what the refund before shipment reads of an order. Whether
// store credit alone paid it comes from the one definition, PaidByCreditAlone.
type beforeShipment struct {
	db.BeforeShipmentRefundRow

	PaidByCredit bool
}

func readBeforeShipment(ctx context.Context, q *db.Queries, number string) (beforeShipment, error) {
	row, err := q.BeforeShipmentRefund(ctx, number)
	if err != nil {
		return beforeShipment{}, err
	}
	paid, err := q.PaidByCreditAlone(ctx, row.OrderID)
	if err != nil {
		return beforeShipment{}, fmt.Errorf("read how %s was paid: %w", number, err)
	}
	return beforeShipment{BeforeShipmentRefundRow: row, PaidByCredit: paid}, nil
}

// beforeShipmentRefundState reports whether the order page offers a refund before
// shipment, and whether one is open for Resume. Under the order lock,
// open_refund_before_shipment re-derives both for a committed order, and
// orders_check_transition re-judges a cancelCreditPaid.
func beforeShipmentRefundState(r *beforeShipment) (offered, open bool) {
	status := order.FulfillmentStatus(r.FulfillmentStatus)
	open = r.ReturnRequestID.Valid && returns.Status(r.ReturnStatus) == returns.StatusApproved
	offered = (r.Committed || creditPaidPending(r)) && !r.Shipped && !r.HasReturn &&
		(status == order.FulfillmentPending || status == order.FulfillmentPicking)
	return offered, open
}

// whyNotRefundable names the cause of a refund that is neither offered nor open.
func whyNotRefundable(r *beforeShipment) error {
	switch status := order.FulfillmentStatus(r.FulfillmentStatus); {
	case status == order.FulfillmentCancelled:
		return ErrOrderCancelled
	case r.Shipped || status == order.FulfillmentShipped ||
		status == order.FulfillmentDelivered || status == order.FulfillmentCompleted:
		return ErrShipped
	case r.HasReturn:
		return ErrHasReturn
	case !r.Committed && !r.PaidByCredit:
		return ErrNotPaid
	}
	return refund.ErrRefused
}

// creditPaidPending is a pending order store credit alone paid. The database
// commits it only when packing starts, and store_credit_guard refuses to pay
// credit back through a return on an uncommitted order, so its refund before
// shipment is the customer's own cancellation run by staff: the spend is
// reversed.
func creditPaidPending(r *beforeShipment) bool {
	return r.PaidByCredit && !r.Committed && !r.HasReturn &&
		order.FulfillmentStatus(r.FulfillmentStatus) == order.FulfillmentPending
}

// Offer is what the order page offers of the refund before shipment, offered
// or open for Resume, and whether one was ever opened: an order being refunded
// is neither picked nor shipped. It also says what the page offers of the
// refund of units a partly shipped order will not send; owed is how many of
// the order's lines a dispatch still owes.
func (s *Store) Offer(ctx context.Context, number string, owed int) (refund.Offer, error) {
	row, err := readBeforeShipment(ctx, s.q, number)
	if err != nil {
		return refund.Offer{}, fmt.Errorf("read refund before shipment of %s: %w", number, err)
	}
	var o refund.Offer
	o.Offered, o.Open = beforeShipmentRefundState(&row)
	o.CreditPaid = o.Offered && creditPaidPending(&row)
	o.Opened = row.ReturnRequestID.Valid
	offered, open, err := s.unsentOnOrder(ctx, number, owed)
	if err != nil {
		return refund.Offer{}, err
	}
	o.UnsentOffered = offered
	o.UnsentOpen, o.UnsentUnits = open != nil, open
	return o, nil
}

// Preview is a refund before shipment as confirming it would pay: the total
// and its card and store-credit split. Open is a refund already open, its
// split frozen and its reason recorded; CreditPaid is a pending order store
// credit alone paid, which confirming cancels at once.
type Preview struct {
	TotalCents, CardCents, CreditCents int64
	Open, CreditPaid                   bool
}

// RefundPreview is what confirming the refund before shipment of the order
// would pay, and moves nothing.
func (s *Store) RefundPreview(ctx context.Context, number string) (Preview, error) {
	row, err := readBeforeShipment(ctx, s.q, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return Preview{}, ErrNotFound
	}
	if err != nil {
		return Preview{}, fmt.Errorf("read refund before shipment of %s: %w", number, err)
	}
	offered, open := beforeShipmentRefundState(&row)
	if !offered && !open {
		return Preview{}, fmt.Errorf("order %s: %w", number, whyNotRefundable(&row))
	}
	return Preview{
		TotalCents: row.TotalCents, CardCents: row.CardCents, CreditCents: row.CreditCents,
		Open: open, CreditPaid: offered && creditPaidPending(&row),
	}, nil
}

// RefundBeforeShipment refunds a paid order nothing has shipped from, in full,
// and cancels it once the money has settled and the invoice is resolved. Each
// step is durable before the next, so pressing it again resumes the first one
// that did not finish. A pending order store credit alone paid is cancelled at
// once instead (cancelCreditPaid). The Checkout Sessions returned are the
// cancelled order's, for the caller to close at Stripe.
func (s *Store) RefundBeforeShipment(ctx context.Context, number, reason string) ([]string, error) {
	if !returns.ValidResolution(reason) {
		return nil, fmt.Errorf("%w: refund reason exceeds %d characters",
			ErrInvalid, returns.MaxResolutionRunes)
	}
	actorID, ok := audit.Actor(ctx)
	if !ok {
		return nil, fmt.Errorf("%w: refund before shipment", audit.ErrNoActor)
	}
	actor := uuid.NullUUID{UUID: actorID, Valid: true}

	state, err := readBeforeShipment(ctx, s.q, number)
	switch {
	case err == nil && creditPaidPending(&state):
		return s.cancelCreditPaid(ctx, number, reason, actor)
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("read refund before shipment of %s: %w", number, err)
	}

	returnID, err := s.q.OpenRefundBeforeShipment(ctx, db.OpenRefundBeforeShipmentParams{
		OrderNumber: number, Reason: strings.TrimSpace(reason),
		ActorUserID: actorID, RequestID: web.RequestID(ctx),
	})
	if err != nil {
		return nil, db.WrapRefusal(fmt.Errorf("open refund before shipment of %s: %w", number, err), refund.ErrRefused)
	}

	// Money never waits on the 加值中心: whatever ECPay answers, the refund goes
	// on, and orders_cancel_invoice_resolved holds the cancellation until the
	// correction settles. Once the order cancels its invoice is resolved, so a
	// correction error then says nothing more.
	correctionErr := s.correctInvoice(ctx, number, actorID)
	sessions, err := s.payAndCancel(ctx, number, returnID, actor)
	if err != nil {
		return nil, errors.Join(err, correctionErr)
	}
	return sessions, nil
}

// correctInvoice files the void as the staff member pressing the button. The
// refund's required note says how the buyer asked for or agreed to the
// cancellation; with its audit row and this actor it is the buyer's consent
// to the void, which the shop must keep.
func (s *Store) correctInvoice(ctx context.Context, number string, actorID uuid.UUID) error {
	if s.invoices == nil {
		return nil
	}
	ctx, cancel := invoiceShare(ctx)
	defer cancel()
	filing := issuer.WithFilingIdentity(ctx, actorID, web.RequestID(ctx))
	if err := s.invoices.CorrectForCancellation(filing, number); err != nil {
		return fmt.Errorf("correct the invoice of %s: %w", number, err)
	}
	return nil
}

// fileRefundAllowance asks the buyer to agree to an allowance for the refund when a
// void could not correct the invoice. The refund's opener files it, so a Resume
// pressed by someone else replays the same claim instead of being refused; the
// presser stands in only when the opener's account is gone.
func (s *Store) fileRefundAllowance(
	ctx context.Context, number string, returnID uuid.UUID, presser uuid.NullUUID,
) error {
	if s.invoices == nil {
		return nil
	}
	openedBy, err := s.q.RefundOpenedBy(ctx, returnID)
	if err != nil {
		return fmt.Errorf("read who opened refund %s: %w", returnID, err)
	}
	if !openedBy.Valid {
		openedBy = presser
	}
	ctx, cancel := invoiceShare(ctx)
	defer cancel()
	filing := issuer.WithFilingIdentity(ctx, openedBy.UUID, web.RequestID(ctx))
	operationID := uuid.NewSHA1(returnID, []byte("allowance"))
	if err := s.invoices.FileCancellationAllowance(filing, number, operationID); err != nil {
		return fmt.Errorf("file the allowance of %s: %w", number, err)
	}
	return nil
}

// invoiceShare bounds the ECPay calls to half of the request's remaining time:
// the refund and the cancellation need the rest.
func invoiceShare(ctx context.Context) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithTimeout(ctx, time.Until(deadline)/2)
	}
	return ctx, func() {}
}

func (s *Store) payAndCancel(
	ctx context.Context, number string, returnID uuid.UUID, actor uuid.NullUUID,
) ([]string, error) {
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
		return nil, ErrUnsettled
	}
	allowanceErr := s.fileRefundAllowance(ctx, number, returnID, actor)
	sessions, err := s.finishRefundBeforeShipment(ctx, number, returnID, actor)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("%w: %w", ErrCancellationIncomplete, err), allowanceErr)
	}
	return sessions, nil
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
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	row, err := q.LockOrderForAdvance(ctx, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %w", refund.ErrRefused, err)
	}
	if err != nil {
		return nil, fmt.Errorf("lock order %s: %w", number, err)
	}
	// Cancelled is terminal and this transaction is the only one that cancels
	// a refunded order, so an earlier press already finished everything below.
	if order.FulfillmentStatus(row.FulfillmentStatus) == order.FulfillmentCancelled {
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
		OrderNumber: number, Status: string(order.FulfillmentCancelled),
	}); err != nil {
		return db.WrapRefusal(err, refund.ErrRefused)
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
		return db.WrapRefusal(fmt.Errorf("close the lines of refund %s: %w", returnID, closeErr), refund.ErrRefused)
	}
	closed, err := q.CompleteReturn(ctx, db.CompleteReturnParams{ID: returnID})
	if err != nil {
		return db.WrapRefusal(fmt.Errorf("complete refund %s: %w", returnID, err), refund.ErrRefused)
	}
	if closed == 0 {
		return fmt.Errorf("%w: refund %s is not open for completion", refund.ErrRefused, returnID)
	}
	return nil
}

func recordStaffCancellation(
	ctx context.Context, q *db.Queries, orderID uuid.UUID, number string, returnID uuid.UUID, actor uuid.NullUUID,
) error {
	err := q.RecordOrderEvent(ctx, db.RecordOrderEventParams{
		OrderID: orderID, Kind: string(order.EventCancelled), ActorUserID: actor,
	})
	if err != nil {
		return fmt.Errorf("record order event: %w", err)
	}
	err = audit.In(ctx, q, audit.Event{
		Action: audit.ActionAdvanceOrder, Table: "orders", ID: audit.EntityID(orderID),
		After: audit.State{
			audit.FieldNumber: number, audit.FieldStatus: string(order.FulfillmentCancelled),
			audit.FieldReturnRequestID: returnID.String(),
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
	return notice.Enqueue(ctx, q, &outbox.OrderTerminal{
		OrderID: orderID, Kind: outbox.TerminalCancelledByStaff, Refunded: refundedCents > 0,
	})
}

// cancelCreditPaid does what the customer's own cancellation does
// (internal/order/buyer), as staff: the order is cancelled, its stock released, its
// credit spend reversed and its 統一發票 void queued.
func (s *Store) cancelCreditPaid(ctx context.Context, number, reason string, actor uuid.NullUUID) ([]string, error) {
	note := strings.TrimSpace(reason)
	if note == "" {
		return nil, fmt.Errorf("%w: cancelling %s needs a note", ErrInvalid, number)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin cancelling %s: %w", number, err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	orderID, err := q.LockOrderByNumber(ctx, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %w", refund.ErrRefused, err)
	}
	if err != nil {
		return nil, fmt.Errorf("lock order %s: %w", number, err)
	}
	// The page's reading of how the order was paid is stale once a cancellation
	// reversed the spend; orders_history_frozen refuses cancelling a cancelled
	// order again, and orders_paid_cancel_needs_refund one that packing or a card
	// committed.
	paidByCredit, err := q.PaidByCreditAlone(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("read how %s was paid: %w", number, err)
	}
	if !paidByCredit {
		return nil, fmt.Errorf("%w: order %s is no longer paid by store credit alone", ErrOrderChanged, number)
	}
	if advanceErr := q.AdvanceOrder(ctx, db.AdvanceOrderParams{
		OrderNumber: number, Status: string(order.FulfillmentCancelled),
	}); advanceErr != nil {
		return nil, db.WrapRefusal(fmt.Errorf("cancel %s: %w", number, advanceErr), refund.ErrRefused)
	}
	// The note says how the buyer asked for or agreed to the cancellation; with
	// the audit row it is their consent to the void, so the void's request id is
	// this request's. It is kept off the order event, which the customer's order
	// page shows.
	returnedCents, err := cancellation.Settle(ctx, q, &cancellation.Order{
		ID: orderID, Number: number, Actor: actor, Kind: outbox.TerminalCancelledByStaff,
		VoidTrigger: web.RequestID(ctx),
	})
	if err != nil {
		return nil, err
	}
	err = audit.In(ctx, q, audit.Event{
		Action: audit.ActionAdvanceOrder, Table: "orders", ID: audit.EntityID(orderID),
		After: audit.State{
			audit.FieldNumber: number, audit.FieldStatus: string(order.FulfillmentCancelled),
			audit.FieldNote: note, audit.FieldCreditReturnedCents: returnedCents,
		},
	})
	if err != nil {
		return nil, err
	}
	sessions, err := q.OpenSessionsForOrder(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("read open checkout sessions of %s: %w", number, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit cancelling %s: %w", number, err)
	}
	return sessions, nil
}
