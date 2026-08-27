package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// ReturnQueue is the back-office return view plus operator-only diagnostics.
// Diagnostics stay out of the rendered page but cross the Store boundary so
// the handler can record quantitative source inconsistencies.
type ReturnQueue struct {
	Rows         []pages.AdminReturn
	payoutIssues []returnPayoutIssue
}

type returnPayoutIssue struct {
	returnID uuid.UUID
	err      error
}

// Returns reads the back-office queue.
func (s *Store) Returns(ctx context.Context) (ReturnQueue, error) {
	rows, err := s.q.ReturnQueue(ctx, PageSize)
	if err != nil {
		return ReturnQueue{}, fmt.Errorf("read return queue: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	lines, err := s.q.ReturnLines(ctx, ids)
	if err != nil {
		return ReturnQueue{}, fmt.Errorf("read return lines: %w", err)
	}
	payoutFacts, err := s.returnPayoutFactsByID(ctx, ids)
	if err != nil {
		return ReturnQueue{}, err
	}
	byRequest := make(map[uuid.UUID][]pages.AdminReturnLine, len(rows))
	for i := range lines {
		l := &lines[i]
		byRequest[l.ReturnRequestID] = append(byRequest[l.ReturnRequestID], pages.AdminReturnLine{
			SKU: l.SKU, Name: l.ProductName, Label: l.VariantLabel.String,
			UnitCents: l.UnitPriceCents, Quantity: l.Quantity,
			OrderLineID: l.OrderLineID.String(),
			// The presence of the figure, never a zero: 0 received is a finding
			// and NULL is a parcel nobody has opened.
			Inspected:   l.ReceivedQuantity.Valid,
			Received:    l.ReceivedQuantity.Int32,
			Restocked:   l.RestockedQuantity.Int32,
			Note:        l.InspectionNote,
			Restockable: l.Restockable,
		})
	}

	view := ReturnQueue{}
	for i := range rows {
		r := &rows[i]
		item := pages.AdminReturn{
			ID:          r.ID.String(),
			OrderNumber: r.OrderNumber,
			Status:      r.Status,
			StatusText:  ReturnStatusLabel(ctx, r.Status),
			Reason:      r.Reason,
			Units:       r.Units,
			AmountCents: r.RefundableCents,
			CreatedAt:   r.CreatedAt.Format("2006-01-02 15:04"),
			Decided:     r.Status != "requested",
			Lines:       byRequest[r.ID],
			Window:      r.RescissionWindow,
		}
		if payoutErr := fillReturnPayoutState(r.Status, payoutFacts[r.ID], &item); payoutErr != nil {
			if !errors.Is(payoutErr, ErrRefused) {
				return ReturnQueue{}, payoutErr
			}
			view.payoutIssues = append(view.payoutIssues, returnPayoutIssue{
				returnID: r.ID,
				err:      payoutErr,
			})
		}
		view.Rows = append(view.Rows, item)
	}
	return view, nil
}

type returnPayoutFacts struct {
	ID                  uuid.UUID
	RefundableCents     int64
	CapturedCents       int64
	RefundedCents       int64
	CreditSpentCents    int64
	CreditReturnedCents int64
	HasAccount          bool
	CardSettled         bool
	CardTerminal        bool
	CreditPosted        bool
	PointsOutstanding   bool
}

type returnPayoutPosition struct {
	Full              refundSplit
	Outstanding       refundSplit
	MoneySettled      bool
	PointsOutstanding bool
	CardTerminal      bool
}

// position derives both the original source split and what remains. The page
// and the retry consume this same result, so visibility cannot drift from what
// pressing retry will actually do.
func (f returnPayoutFacts) position() (returnPayoutPosition, error) {
	capturedRemaining := f.CapturedCents - f.RefundedCents
	creditRemaining := f.CreditSpentCents - f.CreditReturnedCents

	full := refundSplit{Card: min(f.RefundableCents, capturedRemaining)}
	full.Credit = min(f.RefundableCents-full.Card, creditRemaining)
	if full.Card+full.Credit < f.RefundableCents {
		return returnPayoutPosition{}, fmt.Errorf(
			"%w: this order captured %d on the card, %d is already refunded and %d remains; "+
				"%d of store credit was spent, %d returned and %d remains. "+
				"Refunding %d does not fit across the two",
			ErrRefused, f.CapturedCents, f.RefundedCents, capturedRemaining,
			f.CreditSpentCents, f.CreditReturnedCents, creditRemaining, f.RefundableCents)
	}
	if full.Credit > 0 && !f.HasAccount {
		return returnPayoutPosition{}, fmt.Errorf(
			"%w: return %s owes %d in store credit but the order has no account",
			ErrRefused, f.ID, full.Credit)
	}

	outstanding := full
	if f.CardSettled {
		outstanding.Card = 0
	}
	if f.CreditPosted {
		outstanding.Credit = 0
	}
	return returnPayoutPosition{
		Full:              full,
		Outstanding:       outstanding,
		MoneySettled:      outstanding.Card == 0 && outstanding.Credit == 0,
		PointsOutstanding: f.PointsOutstanding,
		CardTerminal:      f.CardTerminal,
	}, nil
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
			CapturedCents:       r.CapturedCents,
			RefundedCents:       r.RefundedCents,
			CreditSpentCents:    r.CreditSpentCents,
			CreditReturnedCents: r.CreditReturnedCents,
			HasAccount:          r.HasAccount,
			CardSettled:         r.CardSettled,
			CardTerminal:        r.CardTerminal,
			CreditPosted:        r.CreditPosted,
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

func fillReturnPayoutState(
	status string, facts returnPayoutFacts, item *pages.AdminReturn,
) error {
	if status != "approved" {
		return nil
	}
	position, err := facts.position()
	if errors.Is(err, ErrRefused) {
		// A payout that no longer fits its sources is not a reason to hide
		// the queue. It is exactly the row a person must investigate.
		item.PayoutOutstanding = true
		item.PayoutBlocked = true
		return err
	}
	if err != nil {
		return err
	}
	item.PayoutOutstanding = !position.MoneySettled || position.PointsOutstanding
	item.PayoutBlocked = position.CardTerminal && position.Outstanding.Card > 0
	return nil
}

// Decide approves or rejects a return, and pays the money back when it
// approves. The refund row is committed BEFORE Stripe is called and settled
// after, so a crash between the two leaves something reconciliation can find;
// request_key is what makes the retry one refund at Stripe and one row here.
func (s *Store) Decide(ctx context.Context, id, decision, resolution string, actor uuid.NullUUID) error {
	row, retry, readErr := s.returnUnderDecision(ctx, id, decision)
	if readErr != nil {
		return readErr
	}
	if decision == "rejected" {
		return s.closeReturn(ctx, row.ID, "rejected", resolution)
	}

	// Validated BEFORE the claim, and it only reads: a claim that cannot be paid
	// should leave the return open for somebody to work out why.
	facts, factErr := s.returnPayoutFact(ctx, row.ID)
	if factErr != nil {
		return factErr
	}
	position, positionErr := facts.position()
	if positionErr != nil {
		return positionErr
	}

	if retry {
		return s.retryApprovedReturn(ctx, &row, position, resolution, actor)
	}
	if row.RefundableCents == 0 {
		return s.closeReturn(ctx, row.ID, "approved", resolution)
	}

	// THE CLAIM, and it commits before a cent moves. Two staff members deciding
	// one return at once both passed the pool read above; only one wins this,
	// and the loser must not have paid anything on the way to finding out.
	if closeErr := s.closeReturn(ctx, row.ID, "approved", resolution); closeErr != nil {
		return closeErr
	}
	return s.payApprovedReturn(ctx, &row, position.Full, resolution, actor)
}

func (s *Store) retryApprovedReturn(
	ctx context.Context,
	row *db.ReturnForDecisionRow,
	position returnPayoutPosition,
	resolution string,
	actor uuid.NullUUID,
) error {
	if !position.MoneySettled {
		// Only what is MISSING. Re-sending a card refund that already settled
		// meets refunds_settled_is_history, and re-posting the credit meets the
		// idempotency key — so a retry that resent both could never finish the
		// half that had failed.
		return s.payApprovedReturn(ctx, row, position.Outstanding, resolution, actor)
	}

	// Money and points are separately durable. If the money committed and the
	// clawback failed, this is useful work rather than a duplicate decision:
	// finish that last idempotent posting and report success.
	if position.PointsOutstanding {
		return s.reverseReturnPoints(ctx, row)
	}
	// Refused rather than reported as done: a return is decided once, and
	// pressing 同意 on one that is already approved AND paid did nothing. Saying
	// so is what tells the staff member the decision was somebody else's.
	return fmt.Errorf(
		"%w: return %s is already approved and its refund has landed", ErrRefused, row.ID)
}

// returnUnderDecision reads the return this decision is about and says whether
// it is a first decision or a RETRY of a payout that did not complete.
//
// An already-approved return being approved again is not a second decision. The
// claim moved ahead of the money, which is what stops two staff members both
// paying — and it also means a refund that failed no longer leaves the return
// open to be decided again. Pressing 同意 once more is how it resumes: the
// decision is not retaken, and refundRequestKey makes the provider call the
// same one.
func (s *Store) returnUnderDecision(
	ctx context.Context, id, decision string,
) (db.ReturnForDecisionRow, bool, error) {
	requestID, err := uuid.Parse(id)
	if err != nil {
		return db.ReturnForDecisionRow{}, false, ErrRefused
	}
	if decision != "approved" && decision != "rejected" {
		return db.ReturnForDecisionRow{}, false, ErrRefused
	}
	row, err := s.q.ReturnForDecision(ctx, requestID)
	if err != nil {
		return db.ReturnForDecisionRow{}, false, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	retry := row.Status == "approved" && decision == "approved"
	if row.Status != "requested" && !retry {
		return db.ReturnForDecisionRow{}, false,
			fmt.Errorf("%w: return %s is already %s", ErrRefused, id, row.Status)
	}
	return row, retry, nil
}

// refundSplit is how a return is paid back: part to the card, part to the
// ledger, because an order can be funded from both at once.
type refundSplit struct {
	// Card is refunded through the provider. Zero means there is no provider
	// call to make, which is the wholly-credit-funded case.
	Card int64
	// Credit is posted to the ledger as a new positive entry.
	Credit int64
}

// payApprovedReturn pays the money back on a return this caller has already
// won. It runs AFTER the decision is committed, which is the whole point: the
// claim is what settles which of two staff members decides, and it used to
// settle it after the payout, so the loser had already refunded.
//
// A failure here leaves the return approved and the money not sent — visible,
// and recoverable, because open_refund has already committed a `pending` row
// keyed on the return. The old ordering left the opposite: money sent against a
// decision that lost, on a return frozen at `rejected` with no audit row saying
// who caused it.
func (s *Store) payApprovedReturn(ctx context.Context, row *db.ReturnForDecisionRow,
	split refundSplit, resolution string, actor uuid.NullUUID,
) error {
	providerRef := ""
	moved := false
	payoutDone := true
	if split.Card > 0 {
		ref, state, err := s.refundCard(ctx, row, split.Card, resolution)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrRefundIncomplete, err)
		}
		// order_events is rendered on the customer's own order page. Record this
		// pass only when money has actually left by either source; Stripe merely
		// accepting a card refund is not the same as settling it.
		if state == RefundSucceeded {
			providerRef = ref
			moved = true
		} else {
			payoutDone = false
		}
	}
	if split.Credit > 0 {
		if _, err := s.q.CompensateReturnWithCredit(ctx, db.CompensateReturnWithCreditParams{
			UserID:      row.UserID.UUID,
			AmountCents: split.Credit,
			// i18n-exempt: a store_credit_entries.reason VALUE, not chrome —
			// written once and read forever, so it cannot follow a reader.
			Reason:   "退貨退回購物金",
			OrderID:  row.OrderID,
			ReturnID: row.ID.String(),
			Actor:    actor,
		}); err != nil {
			return fmt.Errorf("%w: compensate return %s with credit: %s",
				ErrRefundIncomplete, row.ID, err.Error())
		}
		// The ledger post is synchronous and committed: this money has left even
		// though there is no provider reference to put in the event note.
		moved = true
	}
	// Written once THIS pass moves money, which is why it cannot ride in the
	// claiming transaction. Widening this to an already-settled half would
	// duplicate this bare INSERT on every resume.
	if moved {
		if err := s.q.RecordOrderEvent(ctx, db.RecordOrderEventParams{
			OrderID: row.OrderID, Kind: "refunded", ActorUserID: actor,
			Note: text(providerRef),
		}); err != nil {
			return fmt.Errorf("record refunded event for return %s: %w", row.ID, err)
		}
	}
	// The retry path passes only the outstanding half. Therefore zero here also
	// means that source settled on an earlier attempt; if this attempt returned
	// without error and no card is still pending, the whole payout has landed.
	// Use the return's full refundable amount, not the retry remainder, or a
	// split refund whose second half resumes would claw back only that last half.
	// This follows the timeline insert: money already moved even if the separate
	// points posting fails, and its customer-visible event must not disappear.
	if payoutDone {
		if err := s.reverseReturnPoints(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

// reverseReturnPoints finishes the third, idempotent half of a return payout.
// It deliberately receives the return's full refundable amount: a resumed
// split payout carries only the still-outstanding money in refundSplit, while
// the points calculation describes the return as a whole.
func (s *Store) reverseReturnPoints(ctx context.Context, row *db.ReturnForDecisionRow) error {
	if !row.UserID.Valid || row.RefundableCents <= 0 {
		return nil
	}
	if _, err := s.q.ReverseReturnPoints(ctx, db.ReverseReturnPointsParams{
		OrderID:       row.OrderID,
		ReturnID:      row.ID,
		RefundedCents: row.RefundableCents,
	}); err != nil {
		return fmt.Errorf("%w: reverse points for return %s: %w",
			ErrRefundIncomplete, row.ID, err)
	}
	return nil
}

// refundRequestKey is goen's own idempotency key for a return's refund. Derived
// from the RETURN rather than generated, which is what makes a retry find the
// same row through open_refund and the same refund at Stripe.
func refundRequestKey(returnID uuid.UUID) string { return "return:" + returnID.String() }

// refundCard is the two-transaction dance around the provider. It answers with
// what the provider SAID: a refund Stripe accepted may be 'pending' or
// 'requires_action', and recording either as succeeded asserts money moved.
func (s *Store) refundCard(ctx context.Context, row *db.ReturnForDecisionRow,
	cents int64, resolution string,
) (string, RefundState, error) {
	requestKey := refundRequestKey(row.ID)
	if _, openErr := s.q.OpenRefund(ctx, db.OpenRefundParams{
		PaymentID:       row.PaymentID.UUID,
		RequestKey:      requestKey,
		AmountCents:     cents,
		Reason:          resolution,
		ReturnRequestID: row.ID,
	}); openErr != nil {
		return "", "", fmt.Errorf("%w: %w", ErrRefused, openErr)
	}

	// Committed. Now the provider.
	intentID, err := s.refunder.PaymentIntentFor(ctx, row.ProviderRef.String)
	if err != nil {
		return "", "", fmt.Errorf("resolve payment intent for return %s: %w", row.ID, err)
	}
	providerRef, state, err := s.refunder.Refund(ctx, intentID, requestKey, cents)
	if err != nil {
		// UNKNOWN IS NOT FAILURE: 'failed' drops the row out of refunds_guard's
		// sum, so an ambiguous transport error marked failed would let the same
		// allowance be claimed twice. Only a decision from Stripe is terminal.
		if !declinedByStripe(err) {
			return "", "", fmt.Errorf("refund for return %s: %w", row.ID, err)
		}
		if settleErr := s.q.SettleRefund(ctx, db.SettleRefundParams{
			RequestKey: requestKey, Status: string(RefundFailed),
		}); settleErr != nil {
			return "", "", fmt.Errorf("refund refused (%w) and could not be marked failed: %w",
				err, settleErr)
		}
		return "", "", fmt.Errorf("refund for return %s: %w", row.ID, err)
	}

	if err := s.q.SettleRefund(ctx, db.SettleRefundParams{
		RequestKey:  requestKey,
		ProviderRef: providerRef,
		Status:      string(state),
	}); err != nil {
		return "", "", fmt.Errorf("settle refund for return %s: %w", row.ID, err)
	}

	switch state {
	case RefundSucceeded, RefundPending, RefundRequiresAction:
		// Stripe ACCEPTED it, so the return is approved either way: the shop took
		// the goods back, and that decision is not Stripe's to make pending.
		return providerRef, state, nil
	case RefundFailed, RefundCancelled:
		// Terminal and no money moved. A return is decided once, so closing it
		// here would leave a settled return, an unpaid customer and no door back.
		return "", state, fmt.Errorf(
			"%w: the provider reports the refund as %s, so no money left — the return stays open",
			ErrRefused, state)
	default:
		panic("admin: unknown RefundState: " + string(state))
	}
}

// closeReturn stamps the decision and appends to the order's history, together.
func (s *Store) closeReturn(
	ctx context.Context,
	requestID uuid.UUID,
	status, resolution string,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin return decision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	// The row count is the decision. Decide's pre-check runs on the pool outside
	// this transaction, so the statement's own `status = 'requested'` is what
	// settles which of two staff members deciding at once wins — and this now
	// runs BEFORE the money, because settling it afterwards meant the loser had
	// already refunded.
	decided, decideErr := q.DecideReturn(ctx, db.DecideReturnParams{
		ID: requestID, Status: status, Resolution: text(resolution),
	})
	if decideErr != nil {
		return fmt.Errorf("%w: %w", ErrRefused, decideErr)
	}
	if decided == 0 {
		return fmt.Errorf("%w: return %s was decided by somebody else first",
			ErrRefused, requestID)
	}
	if err := auditIn(ctx, q, Event{
		Action: ActionDecideReturn, Table: "return_requests", ID: nullableID(requestID),
		After: map[string]any{"decision": status, "resolution": resolution},
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit return decision: %w", err)
	}
	return nil
}

// ReturnLineInspection is what a staff member found in one line of a parcel.
type ReturnLineInspection struct {
	OrderLineID uuid.UUID
	// Received is how many units actually arrived, which may be fewer than the
	// customer said they were sending.
	Received int32
	// Restocked is how many of them went back on the shelf; Note says why the
	// rest did not.
	Restocked int32
	Note      string
}

// checkInspection refuses counts return_request_lines_restocked_bounded would
// refuse anyway, so a staff member gets a sentence instead of a constraint name.
func checkInspection(lines []ReturnLineInspection) error {
	if len(lines) == 0 {
		return ErrInvalid
	}
	for _, l := range lines {
		if l.Received < 0 || l.Restocked < 0 || l.Restocked > l.Received {
			return fmt.Errorf("%w: cannot restock %d of %d received",
				ErrInvalid, l.Restocked, l.Received)
		}
	}
	return nil
}

// InspectReturn records what came back and puts the sellable units on the
// shelf, the whole parcel in ONE transaction. The restock is read back from
// what this transaction WROTE, so the set acted on is the one the database
// agreed to.
func (s *Store) InspectReturn(
	ctx context.Context, id string, lines []ReturnLineInspection, actor uuid.NullUUID,
) error {
	requestID, err := uuid.Parse(id)
	if err != nil {
		return ErrRefused
	}
	if checkErr := checkInspection(lines); checkErr != nil {
		return checkErr
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin return inspection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	for _, l := range lines {
		n, inspectErr := q.InspectReturnLine(ctx, db.InspectReturnLineParams{
			RequestID: requestID, OrderLineID: l.OrderLineID,
			Received: l.Received, Restocked: l.Restocked, Note: l.Note,
		})
		if inspectErr != nil {
			return fmt.Errorf("%w: %w", ErrRefused, inspectErr)
		}
		// Zero rows is one of three refusals the caller has to hear: not
		// approved, the line belongs elsewhere, or already inspected.
		if n == 0 {
			return fmt.Errorf("%w: line %s of return %s is not open for inspection "+
				"— it may already have been inspected, in which case a recount is a "+
				"stock adjustment", ErrRefused, l.OrderLineID, requestID)
		}
	}

	restock, err := q.ReturnRestockLines(ctx, requestID)
	if err != nil {
		return fmt.Errorf("read what this return restocks: %w", err)
	}
	for _, r := range restock {
		if err := q.RestockReturnedUnits(ctx, db.RestockReturnedUnitsParams{
			VariantID: r.VariantID, Delta: r.Quantity,
			// Per (request, line), so a resubmitted form posts one movement.
			IdempotencyKey: "return:" + requestID.String() + ":" + r.OrderLineID.String(),
			RequestID:      requestID,
			ActorUserID:    actor,
		}); err != nil {
			return fmt.Errorf("restock %s from return %s: %w", r.VariantID, requestID, err)
		}
	}

	if err := auditIn(ctx, q, Event{
		Action: ActionInspectReturn, Table: "return_requests", ID: nullableID(requestID),
		// Counts, never the note: audit_events is append-only and erase_user does
		// not reach it.
		After: map[string]any{"lines": len(lines), "restocked": len(restock)},
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit return inspection: %w", err)
	}
	return nil
}

// CompleteReturn closes an inspected return. It writes no stock: the movement
// was posted with the INSPECTION, which is when the goods went back on the
// shelf.
func (s *Store) CompleteReturn(ctx context.Context, id, resolution string, actor uuid.NullUUID) error {
	requestID, err := uuid.Parse(id)
	if err != nil {
		return ErrRefused
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin return completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	// return_requests_completed_is_inspected refuses this while any line is
	// un-inspected.
	closed, err := q.CompleteReturn(ctx, db.CompleteReturnParams{
		ID: requestID, Resolution: resolution,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}
	if closed == 0 {
		return fmt.Errorf("%w: return %s is not open for completion", ErrRefused, requestID)
	}

	if err := auditIn(ctx, q, Event{
		Action: ActionCompleteReturn, Table: "return_requests", ID: nullableID(requestID),
		After: map[string]any{"resolution": resolution},
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit return completion: %w", err)
	}
	return nil
}
