package refunds

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/refundstate"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

type returnPayoutFacts struct {
	ID                  uuid.UUID
	RefundableCents     int64
	CardRefundCents     int64
	CreditRefundCents   int64
	CardPaidCents       int64
	CreditPaidCents     int64
	HasAccount          bool
	RefundEventRecorded bool
	PointsOutstanding   bool
}

type returnPayoutPosition struct {
	Full              refundSplit
	Outstanding       refundSplit
	MoneySettled      bool
	EventOutstanding  bool
	PointsOutstanding bool
}

// position derives both the original source split and what remains. The page
// and the retry consume this same result, so visibility cannot drift from what
// pressing retry will actually do.
func (f returnPayoutFacts) position() (returnPayoutPosition, error) {
	full, err := f.frozenSplit()
	if err != nil {
		return returnPayoutPosition{}, err
	}
	outstanding := f.outstandingSplit(full)
	// An erased owner is only a blocker while credit remains to be posted. If the
	// exact credit half already landed, a later failed card attempt may safely get
	// a new provider generation without resurrecting the account.
	if outstanding.Credit > 0 && !f.HasAccount {
		return returnPayoutPosition{}, fmt.Errorf(
			"%w: return %s still owes %d in store credit but the order has no account",
			refundstate.ErrRefused, f.ID, outstanding.Credit)
	}
	moneySettled := outstanding.Card == 0 && outstanding.Credit == 0
	return returnPayoutPosition{
		Full:         full,
		Outstanding:  outstanding,
		MoneySettled: moneySettled,
		// A single refunded event is the completed-tense word. It is recovery
		// work only after every frozen source has settled; otherwise a retry
		// would tell the customer the card money is back while it is not.
		EventOutstanding: moneySettled &&
			(f.CardPaidCents > 0 || f.CreditPaidCents > 0) && !f.RefundEventRecorded,
		PointsOutstanding: f.PointsOutstanding,
	}, nil
}

func (f returnPayoutFacts) frozenSplit() (refundSplit, error) {
	full := refundSplit{Card: f.CardRefundCents, Credit: f.CreditRefundCents}
	if full.Card < 0 || full.Credit < 0 || full.Card+full.Credit != f.RefundableCents {
		return refundSplit{}, fmt.Errorf(
			"%w: return %s froze card/credit %d/%d for a %d refund",
			refundstate.ErrRefused, f.ID, full.Card, full.Credit, f.RefundableCents)
	}
	if f.CardPaidCents != 0 && f.CardPaidCents != full.Card {
		return refundSplit{}, fmt.Errorf(
			"%w: return %s has %d settled on card against a frozen %d",
			refundstate.ErrRefused, f.ID, f.CardPaidCents, full.Card)
	}
	if f.CreditPaidCents != 0 && f.CreditPaidCents != full.Credit {
		return refundSplit{}, fmt.Errorf(
			"%w: return %s has %d posted as credit against a frozen %d",
			refundstate.ErrRefused, f.ID, f.CreditPaidCents, full.Credit)
	}
	return full, nil
}

func (f returnPayoutFacts) outstandingSplit(full refundSplit) refundSplit {
	outstanding := full
	if full.Card == 0 || f.CardPaidCents == full.Card {
		outstanding.Card = 0
	}
	if full.Credit == 0 || f.CreditPaidCents == full.Credit {
		outstanding.Credit = 0
	}
	return outstanding
}

func (s *Store) returnPayoutFactsByID(
	ctx context.Context, ids []uuid.UUID,
) (map[uuid.UUID]returnPayoutFacts, error) {
	rows, err := s.q.ReturnPayoutFacts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read return payout facts: %w", err)
	}
	facts := make(map[uuid.UUID]returnPayoutFacts, len(rows))
	for i := range rows {
		r := &rows[i]
		if _, exists := facts[r.ReturnRequestID]; exists {
			return nil, fmt.Errorf(
				"read return payout facts: return %s has multiple source rows",
				r.ReturnRequestID)
		}
		facts[r.ReturnRequestID] = returnPayoutFacts{
			ID:                  r.ReturnRequestID,
			RefundableCents:     r.RefundableCents,
			CardRefundCents:     r.CardRefundCents,
			CreditRefundCents:   r.CreditRefundCents,
			CardPaidCents:       r.CardPaidCents,
			CreditPaidCents:     r.CreditPaidCents,
			HasAccount:          r.HasAccount,
			RefundEventRecorded: r.RefundEventRecorded,
			PointsOutstanding:   r.PointsOutstanding,
		}
	}
	if len(facts) != len(ids) {
		return nil, fmt.Errorf(
			"read return payout facts: got %d rows for %d returns", len(facts), len(ids))
	}
	return facts, nil
}

func (s *Store) returnPayoutFact(
	ctx context.Context, id uuid.UUID,
) (returnPayoutFacts, error) {
	facts, err := s.returnPayoutFactsByID(ctx, []uuid.UUID{id})
	if err != nil {
		return returnPayoutFacts{}, err
	}
	return facts[id], nil
}

// FillPayouts puts each return's refund split on its queue row, rows[i] being
// ids[i]'s, and on an approved one whether its payout is still owed. A payout
// that no longer fits its sources does not fail the read: its row is marked
// blocked and its error comes back under its return, for the caller to log.
func (s *Store) FillPayouts(
	ctx context.Context, ids []uuid.UUID, rows []admin.Return,
) (map[uuid.UUID]error, error) {
	if len(ids) != len(rows) {
		return nil, fmt.Errorf("fill payouts: %d returns for %d rows", len(ids), len(rows))
	}
	payoutFacts, err := s.returnPayoutFactsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	var blocked map[uuid.UUID]error
	for i, id := range ids {
		item := &rows[i]
		if facts, ok := payoutFacts[id]; ok {
			item.CardRefundCents = facts.CardRefundCents
			item.CreditRefundCents = facts.CreditRefundCents
		}
		if payoutErr := fillReturnPayoutState(item.Status, payoutFacts[id], item); payoutErr != nil {
			if !errors.Is(payoutErr, refundstate.ErrRefused) {
				return nil, payoutErr
			}
			if blocked == nil {
				blocked = make(map[uuid.UUID]error)
			}
			blocked[id] = payoutErr
		}
	}
	return blocked, nil
}

func fillReturnPayoutState(
	status returns.Status, facts returnPayoutFacts, item *admin.Return,
) error {
	if status != returns.StatusApproved {
		return nil
	}
	position, err := facts.position()
	if errors.Is(err, refundstate.ErrRefused) {
		// A payout that no longer fits its sources is not a reason to hide
		// the queue. It is exactly the row a person must investigate.
		item.PayoutOutstanding = true
		item.PayoutBlocked = true
		return err
	}
	if err != nil {
		return err
	}
	item.PayoutOutstanding = !position.MoneySettled || position.EventOutstanding ||
		position.PointsOutstanding
	// A terminal provider generation is known not to have moved money, so the
	// claim function can safely append its successor. Only a malformed frozen or
	// settled allocation above is blocked; ordinary failed/cancelled/API-rejected
	// attempts keep the retry control visible.
	item.PayoutBlocked = false
	return nil
}

// PayApproved pays an approved return back in full, from the split its approval
// froze.
func (s *Store) PayApproved(
	ctx context.Context, returnID uuid.UUID, actor uuid.NullUUID,
) error {
	frozen, err := s.q.ReturnForDecision(ctx, returnID)
	if err != nil {
		return fmt.Errorf("read frozen approved return %s: %w", returnID, err)
	}
	frozenFacts, err := s.returnPayoutFact(ctx, returnID)
	if err != nil {
		return err
	}
	frozenPosition, err := frozenFacts.position()
	if err != nil {
		return err
	}
	if frozen.RefundableCents == 0 {
		return nil
	}
	return s.payApprovedReturn(ctx, &frozen, frozenPosition.Full, actor)
}

// Resume reads where an approved return's payout stands and pays what is still
// owed, reporting whether anything was.
func (s *Store) Resume(
	ctx context.Context, row *db.ReturnForDecisionRow, actor uuid.NullUUID,
) (bool, error) {
	position, err := s.refundPosition(ctx, row.ID)
	if err != nil {
		return false, err
	}
	return s.payOutstanding(ctx, row, position, actor)
}

func (s *Store) refundPosition(ctx context.Context, returnID uuid.UUID) (returnPayoutPosition, error) {
	facts, err := s.returnPayoutFact(ctx, returnID)
	if err != nil {
		return returnPayoutPosition{}, err
	}
	return facts.position()
}

// payOutstanding does whatever of an approved return's payout is still owed
// and reports whether anything was.
func (s *Store) payOutstanding(
	ctx context.Context,
	row *db.ReturnForDecisionRow,
	position returnPayoutPosition,
	actor uuid.NullUUID,
) (bool, error) {
	eventRepaired := false
	if position.EventOutstanding {
		if err := s.recordReturnRefundedEvent(ctx, row.ID, actor); err != nil {
			return true, err
		}
		eventRepaired = true
	}
	if !position.MoneySettled {
		// Only what is MISSING. Re-sending a card refund that already settled
		// meets refunds_settled_is_history, and re-posting the credit meets the
		// idempotency key — so a retry that resent both could never finish the
		// half that had failed.
		return true, s.payApprovedReturn(ctx, row, position.Outstanding, actor)
	}

	// Money and points are separately durable. If the money committed and the
	// clawback failed, this is useful work rather than a duplicate decision:
	// finish that last idempotent posting and report success.
	if position.PointsOutstanding {
		return true, s.reverseReturnPoints(ctx, row)
	}
	return eventRepaired, nil
}

// refundSplit is how a return is paid back: part to the card, part to the
// ledger, because an order can be funded from both at once.
type refundSplit struct {
	// Card is refunded through the provider. Zero means there is no provider
	// call to make, which is the wholly-credit-funded case.
	Card   int64
	Credit int64
}

// payApprovedReturn pays the money back on a return this caller has already
// won. It runs AFTER the decision is committed, because the claim is what
// settles which of two staff members decides, and the loser must not have paid
// anything on the way to finding out.
//
// A failure here leaves the return approved and the money not sent — visible,
// and recoverable, because the refund execution claim has already committed an
// immutable attempt keyed to its generation.
func (s *Store) payApprovedReturn(ctx context.Context, row *db.ReturnForDecisionRow,
	split refundSplit, actor uuid.NullUUID,
) error {
	payoutDone := true
	var payoutErr error
	if split.Card > 0 {
		_, cardDone, cardErr := s.payReturnCardSource(ctx, row.ID)
		payoutDone = cardDone
		payoutErr = errors.Join(payoutErr, cardErr)
	}
	if split.Credit > 0 {
		_, creditErr := s.payReturnCreditSource(ctx, row.ID, split.Credit, actor)
		if creditErr != nil {
			payoutErr = errors.Join(payoutErr, creditErr)
			payoutDone = false
		}
	}
	if payoutErr != nil {
		return fmt.Errorf("%w: %w", refundstate.ErrIncomplete, payoutErr)
	}
	// The retry path passes only the outstanding half. Therefore zero here also
	// means that source settled on an earlier attempt; if this attempt returned
	// without error and no card is still pending, the whole payout has landed.
	// Use the return's full refundable amount, not the retry remainder, or a
	// split refund whose second half resumes would claw back only that last half.
	// The timeline word is refunded: write it only once every source has settled,
	// then claw back points. A later failure of either step is recoverable
	// without duplicating the customer timeline.
	if payoutDone {
		if err := s.recordReturnRefundedEvent(ctx, row.ID, actor); err != nil {
			return fmt.Errorf("%w: %w", refundstate.ErrIncomplete, err)
		}
		if err := s.reverseReturnPoints(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) payReturnCardSource(
	ctx context.Context, returnID uuid.UUID,
) (moved, done bool, err error) {
	_, state, err := s.refundCard(ctx, returnID)
	if err != nil {
		// A known card failure proves that source did not move, but it says nothing
		// about independently frozen credit. The caller still posts that source.
		return false, false, fmt.Errorf("refund card source for return %s: %w", returnID, err)
	}
	// Stripe accepting a refund is not the same as settling it. Only a succeeded
	// outcome is money moved and permits the aggregate payout to finish.
	settled := state == refundstate.Succeeded
	return settled, settled, nil
}

func (s *Store) payReturnCreditSource(
	ctx context.Context, returnID uuid.UUID, amountCents int64, actor uuid.NullUUID,
) (bool, error) {
	if _, err := s.q.CompensateReturnWithCredit(ctx, db.CompensateReturnWithCreditParams{
		AmountCents: amountCents,
		ReturnID:    returnID,
		Actor:       actor,
	}); err != nil {
		return false, fmt.Errorf("compensate return %s with credit: %w", returnID, err)
	}
	return true, nil
}

func (s *Store) recordReturnRefundedEvent(
	ctx context.Context, returnID uuid.UUID, actor uuid.NullUUID,
) error {
	if err := s.q.RecordReturnRefundedEvent(ctx, db.RecordReturnRefundedEventParams{
		ReturnRequestID: returnID,
		ActorUserID:     actor,
	}); err != nil {
		return fmt.Errorf("record refunded event for return %s: %w", returnID, err)
	}
	return nil
}

// reverseReturnPoints finishes the third, idempotent half of a return payout.
// It deliberately receives the return's full refundable amount: a resumed
// split payout carries only the still-outstanding money in refundSplit, while
// the points calculation describes the return as a whole.
func (s *Store) reverseReturnPoints(ctx context.Context, row *db.ReturnForDecisionRow) error {
	// Erasure detaches orders.user_id but retains the award lot and its loyalty
	// account. reverse_return_points resolves those durable rows from the order,
	// so a missing live user must not strand a post-money clawback.
	if row.RefundableCents <= 0 {
		return nil
	}
	if _, err := s.q.ReverseReturnPoints(ctx, row.ID); err != nil {
		return fmt.Errorf("%w: reverse points for return %s: %w",
			refundstate.ErrIncomplete, row.ID, err)
	}
	return nil
}

// refundCard is the two-transaction dance around the provider. It answers with
// what the provider SAID: a refund Stripe accepted may be 'pending' or
// 'requires_action', and recording either as succeeded asserts money moved.
func (s *Store) refundCard(
	ctx context.Context, returnID uuid.UUID,
) (string, refundstate.State, error) {
	actorID, ok := audit.Actor(ctx)
	if !ok {
		return "", "", fmt.Errorf("%w: refund provider attempt", audit.ErrNoActor)
	}
	requestID := web.RequestID(ctx)
	if requestID == "" {
		return "", "", fmt.Errorf("%w: refund provider attempt has no request id", refundstate.ErrRefused)
	}

	claim, err := s.claimReturnRefundExecution(ctx, returnID, actorID, requestID)
	if err != nil {
		return "", "", err
	}

	intentID, err := s.refunder.PaymentIntentFor(ctx, claim.PaymentProviderRef)
	if err != nil {
		return "", "", fmt.Errorf("resolve payment intent for return %s: %w", returnID, err)
	}
	providerRef, state, err := s.refunder.Refund(
		ctx, intentID, claim.RequestKey, claim.AmountCents,
	)
	if err != nil {
		return "", "", s.recordRefundProviderError(
			ctx, returnID, claim.RefundID, actorID, requestID, err,
		)
	}
	if outcomeErr := s.recordReturnRefundOutcome(
		ctx, claim.RefundID, providerRef, actorID, requestID, state,
	); outcomeErr != nil {
		return "", "", fmt.Errorf("record refund outcome for return %s: %w", returnID, outcomeErr)
	}
	return refundProviderResult(providerRef, state)
}

func (s *Store) claimReturnRefundExecution(
	ctx context.Context, returnID, actorID uuid.UUID, requestID string,
) (db.RefundExecutionRow, error) {
	// One auto-committed database statement derives every economic value from
	// the approved return and appends the attempt audit. Only after that durable
	// attribution may this request touch Stripe.
	refundID, err := s.q.ClaimReturnRefundExecution(ctx, db.ClaimReturnRefundExecutionParams{
		ReturnRequestID: returnID,
		ActorUserID:     actorID,
		RequestID:       requestID,
	})
	if err != nil {
		return db.RefundExecutionRow{}, pgerr.WrapRefusal(
			fmt.Errorf("claim refund execution for return %s: %w", returnID, err), refundstate.ErrRefused)
	}
	claim, err := s.q.RefundExecution(ctx, refundID)
	if err != nil {
		return db.RefundExecutionRow{}, fmt.Errorf("read refund execution %s: %w", refundID, err)
	}
	return claim, nil
}

func (s *Store) recordRefundProviderError(
	ctx context.Context,
	returnID, refundID, actorID uuid.UUID,
	requestID string,
	providerErr error,
) error {
	// UNKNOWN IS NOT FAILURE: lookup, decode, transport and unrecognised provider
	// responses leave the claim pending. Only a rejection wrapped at the
	// refund-CREATE boundary proves no provider object was created.
	if !errors.Is(providerErr, ErrCreateRejected) {
		return fmt.Errorf("refund for return %s: %w", returnID, providerErr)
	}
	if _, err := s.q.RecordRefundAPIRejection(ctx, db.RecordRefundAPIRejectionParams{
		RefundID: refundID, ActorUserID: actorID, RequestID: requestID,
	}); err != nil {
		return fmt.Errorf("refund refused (%w) and could not be marked failed: %w", providerErr, err)
	}
	return fmt.Errorf("refund for return %s: %w", returnID, providerErr)
}

func (s *Store) recordReturnRefundOutcome(
	ctx context.Context,
	refundID uuid.UUID,
	providerRef string,
	actorID uuid.UUID,
	requestID string,
	state refundstate.State,
) error {
	params := db.RecordRefundPendingParams{
		RefundID: refundID, ProviderRef: providerRef,
		ActorUserID: actorID, RequestID: requestID,
	}
	switch state {
	case refundstate.Pending:
		_, err := s.q.RecordRefundPending(ctx, params)
		return err
	case refundstate.RequiresAction:
		_, err := s.q.RecordRefundRequiresAction(ctx, db.RecordRefundRequiresActionParams(params))
		return err
	case refundstate.Succeeded:
		_, err := s.q.RecordRefundSucceeded(ctx, db.RecordRefundSucceededParams(params))
		return err
	case refundstate.Failed:
		_, err := s.q.RecordRefundFailed(ctx, db.RecordRefundFailedParams(params))
		return err
	case refundstate.Cancelled:
		_, err := s.q.RecordRefundCancelled(ctx, db.RecordRefundCancelledParams(params))
		return err
	default:
		panic("refunds: unknown refund state: " + string(state))
	}
}

func refundProviderResult(providerRef string, state refundstate.State) (string, refundstate.State, error) {
	switch state {
	case refundstate.Succeeded, refundstate.Pending, refundstate.RequiresAction:
		// Stripe ACCEPTED it, so the return is approved either way: the shop took
		// the goods back, and that decision is not Stripe's to make pending.
		return providerRef, state, nil
	case refundstate.Failed, refundstate.Cancelled:
		// Terminal and no money moved. A return is decided once, so closing it
		// here would leave a settled return, an unpaid customer and no door back.
		return "", state, fmt.Errorf(
			"the provider reports the refund as %s, so no money left — the return stays open",
			state)
	default:
		panic("refunds: outcome switch accepted unknown refund state: " + string(state))
	}
}
