package refunds

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/order/notice"
	"github.com/koopa0/goen/internal/order/returns"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment/refund"
	"github.com/koopa0/goen/internal/web/requestid"
)

// The refusals of the refund of units a partly shipped order will not send
// that the form or the order's own state decides, before any money moves.
var (
	// ErrUnsentReason is a reason that is not one of returns.UnsentReasons.
	ErrUnsentReason = fmt.Errorf("%w: not a reason the shop records for an unsent unit", ErrInvalid)
	// ErrUnsentUnits names no unit, or a quantity that is not a count.
	ErrUnsentUnits = fmt.Errorf("%w: an unsent refund names at least one unit", ErrInvalid)
	// ErrUnsentDiffers is a press naming other units than the refund already
	// open on the order, which open_unsent_refund would resume regardless.
	ErrUnsentDiffers = fmt.Errorf("%w: an unsent refund naming other units is open", refund.ErrRefused)
	// ErrUnsentAllowance is a refund that settled and closed whose 折讓 could
	// not be filed; staff file it from the order page's invoice section.
	ErrUnsentAllowance = errors.New("refunds: the unsent refund settled and its allowance was not filed")
)

// UnsentRefund is a refund of unsent units as its approval freezes it: open
// already, or what confirming a press would open.
type UnsentRefund struct {
	Units                               []refund.UnsentUnit
	Reason                              returns.UnsentReason
	RefundCents, CardCents, CreditCents int64
	Open                                bool
}

// UnsentRequest is what a staff member asks of the refund of unsent units:
// how many of each order line, the reason code and the note. A resume needs
// only the units; its reason and note are recorded.
type UnsentRequest struct {
	Units  map[uuid.UUID]int32
	Reason string
	Note   string
}

// named is the units the request asks for, without the lines left at zero.
func (r *UnsentRequest) named() map[uuid.UUID]int32 {
	out := make(map[uuid.UUID]int32, len(r.Units))
	for id, n := range r.Units {
		if n != 0 {
			out[id] = n
		}
	}
	return out
}

// claims are the door's parallel arrays, in a fixed order so a repeated press
// asks the same thing.
func (r *UnsentRequest) claims() (lines []uuid.UUID, quantities []int32, err error) {
	named := r.named()
	if len(named) == 0 {
		return nil, nil, ErrUnsentUnits
	}
	lines = slices.SortedFunc(maps.Keys(named), func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	quantities = make([]int32, len(lines))
	for i, id := range lines {
		if named[id] < 0 {
			return nil, nil, ErrUnsentUnits
		}
		quantities[i] = named[id]
	}
	return lines, quantities, nil
}

func (r *UnsentRequest) reason() (returns.UnsentReason, error) {
	reason, ok := returns.ParseUnsentReason(r.Reason)
	if !ok {
		return "", ErrUnsentReason
	}
	return reason, nil
}

// unsentOffered reports whether the order page offers the refund of unsent
// units: an order that has sent a parcel, which only a paid one can, is not
// finished, and still owes units. open_unsent_refund re-derives each under
// the order lock.
func unsentOffered(status order.FulfillmentStatus, owed int) bool {
	return (status == order.FulfillmentShipped || status == order.FulfillmentDelivered) && owed > 0
}

// unsentOnOrder is the refund of unsent units as the order page offers it:
// offered while owed units remain, or open for Resume with the units it
// covers.
func (s *Store) unsentOnOrder(ctx context.Context, number string, owed int) (offered bool, open []refund.UnsentUnit, err error) {
	st, err := s.q.UnsentRefundState(ctx, number)
	if err != nil {
		return false, nil, fmt.Errorf("read the unsent refund of %s: %w", number, err)
	}
	if !st.ReturnRequestID.Valid {
		return unsentOffered(order.FulfillmentStatus(st.FulfillmentStatus), owed), nil, nil
	}
	open, err = unsentUnits(ctx, s.q, st.ReturnRequestID.UUID)
	return false, open, err
}

func unsentUnits(ctx context.Context, q *db.Queries, returnID uuid.UUID) ([]refund.UnsentUnit, error) {
	rows, err := q.UnsentRefundLines(ctx, returnID)
	if err != nil {
		return nil, fmt.Errorf("read the lines of unsent refund %s: %w", returnID, err)
	}
	units := make([]refund.UnsentUnit, len(rows))
	for i, r := range rows {
		units[i] = refund.UnsentUnit{LineID: r.OrderLineID, Name: r.ProductName, Label: r.VariantLabel, Quantity: r.Quantity}
	}
	return units, nil
}

// sameUnits reports whether the request names exactly the units of a refund.
func sameUnits(units []refund.UnsentUnit, req *UnsentRequest) bool {
	named := req.named()
	if len(named) != len(units) {
		return false
	}
	for _, u := range units {
		if named[u.LineID] != u.Quantity {
			return false
		}
	}
	return true
}

// UnsentRefundPreview is what confirming the request would do, and moves
// nothing. A first press runs the door in a transaction it rolls back, so the
// amount and the card/credit split are the ones the approval freezes. A press
// on an order whose refund is open reads that one, for Resume.
func (s *Store) UnsentRefundPreview(ctx context.Context, number string, req *UnsentRequest) (UnsentRefund, error) {
	st, err := s.q.UnsentRefundState(ctx, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return UnsentRefund{}, ErrNotFound
	}
	if err != nil {
		return UnsentRefund{}, fmt.Errorf("read the unsent refund of %s: %w", number, err)
	}
	if st.ReturnRequestID.Valid {
		return frozenUnsent(ctx, s.q, number, st.ReturnRequestID.UUID, req)
	}
	reason, err := req.reason()
	if err != nil {
		return UnsentRefund{}, err
	}
	lineIDs, quantities, err := req.claims()
	if err != nil {
		return UnsentRefund{}, err
	}
	actorID, ok := audit.Actor(ctx)
	if !ok {
		return UnsentRefund{}, fmt.Errorf("%w: unsent refund preview", audit.ErrNoActor)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return UnsentRefund{}, fmt.Errorf("begin unsent refund preview: %w", err)
	}
	// Never committed: the door's rows, its audit row and its order lock all go.
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	// The note is asked on the confirmation and judged there; the door wants
	// one, and it changes no amount.
	returnID, err := q.OpenUnsentRefund(ctx, db.OpenUnsentRefundParams{
		OrderNumber: number, LineIds: lineIDs, Quantities: quantities,
		Reason: string(reason), Note: "preview", ActorUserID: actorID, RequestID: requestid.From(ctx),
	})
	if err != nil {
		return UnsentRefund{}, db.WrapRefusal(
			fmt.Errorf("preview the unsent refund of %s: %w", number, err), refund.ErrRefused)
	}
	preview, err := frozenUnsent(ctx, q, number, returnID, req)
	preview.Open = false
	return preview, err
}

// frozenUnsent reads a refund the door opened, which must name exactly the
// units of the press.
func frozenUnsent(
	ctx context.Context, q *db.Queries, number string, returnID uuid.UUID, req *UnsentRequest,
) (UnsentRefund, error) {
	units, err := unsentUnits(ctx, q, returnID)
	if err != nil {
		return UnsentRefund{}, err
	}
	if !sameUnits(units, req) {
		return UnsentRefund{}, fmt.Errorf("order %s: %w", number, ErrUnsentDiffers)
	}
	frozen, err := q.UnsentRefundFrozen(ctx, returnID)
	if err != nil {
		return UnsentRefund{}, fmt.Errorf("read unsent refund %s: %w", returnID, err)
	}
	return UnsentRefund{
		Units: units, Reason: returns.UnsentReason(frozen.Reason),
		RefundCents: frozen.RefundCents, CardCents: frozen.CardCents, CreditCents: frozen.CreditCents,
		Open: true,
	}, nil
}

// RefundUnsent refunds units of a partly shipped order that will not be sent:
// it opens and approves the refund, pays it back card first and then credit
// (payOutstanding, the return payout), then releases or writes off the units'
// hold, completes the refund and queues the customer's letter in one
// transaction, and files the 折讓. Each step is durable before the next, so
// pressing it again with the same units resumes the first one that did not
// finish. A 折讓 that could not be filed is ErrUnsentAllowance, after
// everything else has finished.
func (s *Store) RefundUnsent(ctx context.Context, number string, req *UnsentRequest) error {
	actorID, ok := audit.Actor(ctx)
	if !ok {
		return fmt.Errorf("%w: unsent refund", audit.ErrNoActor)
	}
	actor := uuid.NullUUID{UUID: actorID, Valid: true}

	returnID, err := s.openUnsent(ctx, number, actorID, req)
	if err != nil {
		return err
	}
	row, err := s.q.ReturnForDecision(ctx, returnID)
	if err != nil {
		return fmt.Errorf("read unsent refund %s of %s: %w", returnID, number, err)
	}
	position, err := s.refundPosition(ctx, returnID)
	if err != nil {
		return err
	}
	if _, payErr := s.payOutstanding(ctx, &row, position, actor); payErr != nil {
		return payErr
	}
	// Read again: a card refund Stripe accepted as pending returns no error and
	// has still not moved the money.
	if position, err = s.refundPosition(ctx, returnID); err != nil {
		return err
	}
	if !position.MoneySettled || position.EventOutstanding || position.PointsOutstanding {
		return ErrUnsettled
	}
	if err := s.settleUnsent(ctx, row.OrderID, returnID, actorID); err != nil {
		return err
	}
	if err := s.fileUnsentAllowance(ctx, number, returnID, actorID); err != nil {
		return fmt.Errorf("%w: %w", ErrUnsentAllowance, err)
	}
	return nil
}

// openUnsent opens the refund, or finds the one this order has open. Either
// way it must cover exactly the units this press names: the door resumes the
// open one whatever a press names, and paying it for a press that asked for
// other units would refund what nobody confirmed.
func (s *Store) openUnsent(ctx context.Context, number string, actorID uuid.UUID, req *UnsentRequest) (uuid.UUID, error) {
	st, err := s.q.UnsentRefundState(ctx, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("%w: no order %s", refund.ErrRefused, number)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("read the unsent refund of %s: %w", number, err)
	}
	returnID := st.ReturnRequestID.UUID
	if !st.ReturnRequestID.Valid {
		if returnID, err = s.openNewUnsent(ctx, number, actorID, req); err != nil {
			return uuid.Nil, err
		}
	}
	units, err := unsentUnits(ctx, s.q, returnID)
	if err != nil {
		return uuid.Nil, err
	}
	if !sameUnits(units, req) {
		return uuid.Nil, fmt.Errorf("order %s: %w", number, ErrUnsentDiffers)
	}
	return returnID, nil
}

// openNewUnsent opens and approves the refund a press asks for, once its
// reason, note and units are ones the door can take.
func (s *Store) openNewUnsent(ctx context.Context, number string, actorID uuid.UUID, req *UnsentRequest) (uuid.UUID, error) {
	reason, err := req.reason()
	if err != nil {
		return uuid.Nil, err
	}
	note := strings.TrimSpace(req.Note)
	if note == "" || !returns.ValidResolution(note) {
		return uuid.Nil, fmt.Errorf("%w: an unsent refund needs a note of at most %d characters",
			ErrInvalid, returns.MaxResolutionRunes)
	}
	lineIDs, quantities, err := req.claims()
	if err != nil {
		return uuid.Nil, err
	}
	returnID, err := s.q.OpenUnsentRefund(ctx, db.OpenUnsentRefundParams{
		OrderNumber: number, LineIds: lineIDs, Quantities: quantities,
		Reason: string(reason), Note: note, ActorUserID: actorID, RequestID: requestid.From(ctx),
	})
	if err != nil {
		return uuid.Nil, db.WrapRefusal(fmt.Errorf("open the unsent refund of %s: %w", number, err), refund.ErrRefused)
	}
	return returnID, nil
}

// settleUnsent gives up the units and tells the customer, in ONE transaction:
// the letter says the money is back and the units will not come, which
// settle_unsent_refund makes true only once the payout has landed.
func (s *Store) settleUnsent(ctx context.Context, orderID, returnID, actorID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin settling unsent refund %s: %w", returnID, err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	if err := q.SettleUnsentRefund(ctx, db.SettleUnsentRefundParams{
		ReturnRequestID: returnID, ActorUserID: actorID, RequestID: requestid.From(ctx),
	}); err != nil {
		return db.WrapRefusal(fmt.Errorf("settle unsent refund %s: %w", returnID, err), refund.ErrRefused)
	}
	if err := notice.EnqueuePartRefunded(ctx, q, &outbox.OrderPartRefunded{
		OrderID: orderID, ReturnRequestID: returnID,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit settling unsent refund %s: %w", returnID, err)
	}
	return nil
}

// fileUnsentAllowance files the 折讓 for what the refund paid back, under the
// presser's filing identity; the database derives the amount. Its operation
// id comes from the refund, so a second press replays the same claim. With no
// 加值中心, or no live invoice yet, there is nothing to file now: staff file it
// from the order page once the invoice exists. A buyer asked to agree is the
// normal outcome of an online allowance, and nothing left to relieve is an
// invoice already corrected.
func (s *Store) fileUnsentAllowance(ctx context.Context, number string, returnID, actorID uuid.UUID) error {
	if s.invoices == nil || !s.invoices.Enabled() {
		return nil
	}
	ctx, cancel := invoiceShare(ctx)
	defer cancel()
	filing := issuer.WithFilingIdentity(ctx, actorID, requestid.From(ctx))
	_, err := s.invoices.FileAllowance(filing, number, uuid.NewSHA1(returnID, []byte("allowance")))
	switch {
	case err == nil, errors.Is(err, issuer.ErrAwaitingBuyer), errors.Is(err, issuer.ErrNotFound),
		errors.Is(err, issuer.ErrTooMuch):
		return nil
	default:
		return fmt.Errorf("file the allowance of %s: %w", number, err)
	}
}
