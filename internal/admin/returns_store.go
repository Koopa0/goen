package admin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

const maxAssessmentBasisRunes = 500

// FormRefusalError is a decision or assessment the advertised policy (or the
// form) refused, naming the control the queue should mark.
type FormRefusalError struct {
	Field string
	Kind  returns.RefusalKind
}

func (e *FormRefusalError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", ErrRefused.Error(), e.Kind)
}

func (e *FormRefusalError) Unwrap() error { return ErrRefused }

func formRefuse(field string, kind returns.RefusalKind) error {
	return &FormRefusalError{Field: field, Kind: kind}
}

const maxReturnResolutionRunes = 300

func validReturnResolution(s string) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= maxReturnResolutionRunes
}

// A first exception is the shop paying outside an advertised right.
// Without a recorded reason the audit cannot say why the money moved.
// Retries never call this: they resume an already-decided claim.
func requireExceptionReason(kind returns.DecisionKind, resolution string) error {
	if kind != returns.DecisionException {
		return nil
	}
	if strings.TrimSpace(resolution) != "" {
		return nil
	}
	return formRefuse("resolution", returns.RefuseExceptionReason)
}

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
			Window:      l.PolicyWindow,
		})
	}
	assessments, err := s.q.LatestEligibilityAssessments(ctx, ids)
	if err != nil {
		return ReturnQueue{}, fmt.Errorf("read return assessments: %w", err)
	}
	assessmentIDs := make([]uuid.UUID, 0, len(assessments))
	assessmentByRequest := make(map[uuid.UUID]db.ReturnEligibilityAssessment, len(assessments))
	for i := range assessments {
		assessmentIDs = append(assessmentIDs, assessments[i].ID)
		assessmentByRequest[assessments[i].ReturnRequestID] = assessments[i]
	}
	if overlayErr := overlayReturnAssessmentFacts(ctx, s.q, byRequest, assessmentIDs); overlayErr != nil {
		return ReturnQueue{}, overlayErr
	}

	view, err := buildReturnQueue(ctx, rows, byRequest, assessmentByRequest, payoutFacts)
	if err != nil {
		return ReturnQueue{}, err
	}
	return view, nil
}

func overlayReturnAssessmentFacts(
	ctx context.Context,
	q *db.Queries,
	byRequest map[uuid.UUID][]pages.AdminReturnLine,
	assessmentIDs []uuid.UUID,
) error {
	if len(assessmentIDs) == 0 {
		return nil
	}
	facts, err := q.EligibilityFactsForAssessments(ctx, assessmentIDs)
	if err != nil {
		return fmt.Errorf("read return eligibility facts: %w", err)
	}
	for i := range facts {
		f := &facts[i]
		lines := byRequest[f.ReturnRequestID]
		for j := range lines {
			if lines[j].OrderLineID != f.OrderLineID.String() {
				continue
			}
			lines[j].Unused = f.Unused
			lines[j].Packaging = f.PackagingComplete
			lines[j].Accessories = f.AccessoriesComplete
			lines[j].Window = f.PolicyWindow
		}
		byRequest[f.ReturnRequestID] = lines
	}
	return nil
}

func buildReturnQueue(
	ctx context.Context,
	rows []db.ReturnQueueRow,
	byRequest map[uuid.UUID][]pages.AdminReturnLine,
	assessmentByRequest map[uuid.UUID]db.ReturnEligibilityAssessment,
	payoutFacts map[uuid.UUID]returnPayoutFacts,
) (ReturnQueue, error) {
	view := ReturnQueue{}
	for i := range rows {
		r := &rows[i]
		item := pages.AdminReturn{
			ID:          r.ID.String(),
			OrderNumber: r.OrderNumber,
			Status:      r.Status,
			StatusText:  ReturnStatusLabel(ctx, returns.ReturnStatus(r.Status)),
			Reason:      r.Reason,
			Units:       r.Units,
			AmountCents: r.RefundableCents,
			CreatedAt:   shoptime.Minute(r.CreatedAt),
			Decided:     returns.ReturnStatus(r.Status) != returns.ReturnRequested,
			Lines:       byRequest[r.ID],
			Window:      r.RescissionWindow,
		}
		if a, ok := assessmentByRequest[r.ID]; ok {
			item.AssessmentVersion = a.Version
			item.AssessmentBasis = a.Basis
			item.AssessedAt = shoptime.Minute(a.AssessedAt)
		}
		if facts, ok := payoutFacts[r.ID]; ok {
			item.CardRefundCents = facts.CardRefundCents
			item.CreditRefundCents = facts.CreditRefundCents
		}
		if payoutErr := fillReturnPayoutState(returns.ReturnStatus(r.Status), payoutFacts[r.ID], &item); payoutErr != nil {
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
			ErrRefused, f.ID, outstanding.Credit)
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
			ErrRefused, f.ID, full.Card, full.Credit, f.RefundableCents)
	}
	if f.CardPaidCents != 0 && f.CardPaidCents != full.Card {
		return refundSplit{}, fmt.Errorf(
			"%w: return %s has %d settled on card against a frozen %d",
			ErrRefused, f.ID, f.CardPaidCents, full.Card)
	}
	if f.CreditPaidCents != 0 && f.CreditPaidCents != full.Credit {
		return refundSplit{}, fmt.Errorf(
			"%w: return %s has %d posted as credit against a frozen %d",
			ErrRefused, f.ID, f.CreditPaidCents, full.Credit)
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

func fillReturnPayoutState(
	status returns.ReturnStatus, facts returnPayoutFacts, item *pages.AdminReturn,
) error {
	if status != returns.ReturnApproved {
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
	item.PayoutOutstanding = !position.MoneySettled || position.EventOutstanding ||
		position.PointsOutstanding
	// A terminal provider generation is known not to have moved money, so the
	// claim function can safely append its successor. Only a malformed frozen or
	// settled allocation above is blocked; ordinary failed/cancelled/API-rejected
	// attempts keep the retry control visible.
	item.PayoutBlocked = false
	return nil
}

// Decide approves or rejects a return, and pays the money back when it
// approves. The refund row is committed BEFORE Stripe is called and settled
// after, so a crash between the two leaves something reconciliation can find;
// an ambiguous attempt reuses its provider key, while a known terminal attempt
// gets a fresh DB-derived key and immutable successor row.
func (s *Store) Decide(
	ctx context.Context, id, decision, resolution, assessmentVersion string, _ uuid.NullUUID,
) error {
	if !validReturnResolution(resolution) {
		return fmt.Errorf("%w: return resolution exceeds %d characters",
			ErrInvalid, maxReturnResolutionRunes)
	}
	actorID, ok := actorFrom(ctx)
	if !ok {
		return fmt.Errorf("%w: decide return", ErrNoActor)
	}
	// The signed-in context is the authority for every side effect of this
	// decision. Trusting the caller-supplied parameter instead could attribute
	// the return audit, provider attempt and store-credit posting to three
	// different people.
	actor := uuid.NullUUID{UUID: actorID, Valid: true}

	kind, ok := returns.ParseDecisionKind(decision)
	if !ok {
		return ErrRefused
	}
	version, versionErr := parseAssessmentVersion(assessmentVersion)
	if versionErr != nil {
		return versionErr
	}
	row, retry, readErr := s.returnUnderDecision(ctx, id, kind)
	if readErr != nil {
		return readErr
	}

	if retry {
		return s.retryDecideReturn(ctx, &row, actor)
	}
	return s.decideReturnFirst(ctx, &row, kind, resolution, version, actor)
}

func (s *Store) retryDecideReturn(
	ctx context.Context, row *db.ReturnForDecisionRow, actor uuid.NullUUID,
) error {
	facts, err := s.returnPayoutFact(ctx, row.ID)
	if err != nil {
		return err
	}
	position, err := facts.position()
	if err != nil {
		return err
	}
	return s.retryApprovedReturn(ctx, row, position, actor)
}

func (s *Store) decideReturnFirst(
	ctx context.Context,
	row *db.ReturnForDecisionRow,
	kind returns.DecisionKind,
	resolution string,
	version int32,
	actor uuid.NullUUID,
) error {
	resolution = strings.TrimSpace(resolution)

	// THE CLAIM, and it commits before a cent moves. Two staff members deciding
	// one return at once both passed the pool read above; only one wins this,
	// and the loser must not have paid anything on the way to finding out. The
	// transition freezes goods and the one delivery allocation under the order
	// lock, so every provider retry keeps this winning economic identity.
	if closeErr := s.closeReturn(ctx, row.ID, kind, resolution, version); closeErr != nil {
		return closeErr
	}
	if kind == returns.DecisionReject {
		return nil
	}
	return s.payFrozenApprovedReturn(ctx, row.ID, actor)
}

func (s *Store) payFrozenApprovedReturn(
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

func (s *Store) retryApprovedReturn(
	ctx context.Context,
	row *db.ReturnForDecisionRow,
	position returnPayoutPosition,
	actor uuid.NullUUID,
) error {
	eventRepaired := false
	if position.EventOutstanding {
		if err := s.recordReturnRefundedEvent(ctx, row.ID, actor); err != nil {
			return err
		}
		eventRepaired = true
	}
	if !position.MoneySettled {
		// Only what is MISSING. Re-sending a card refund that already settled
		// meets refunds_settled_is_history, and re-posting the credit meets the
		// idempotency key — so a retry that resent both could never finish the
		// half that had failed.
		return s.payApprovedReturn(ctx, row, position.Outstanding, actor)
	}

	// Money and points are separately durable. If the money committed and the
	// clawback failed, this is useful work rather than a duplicate decision:
	// finish that last idempotent posting and report success.
	if position.PointsOutstanding {
		return s.reverseReturnPoints(ctx, row)
	}
	if eventRepaired {
		return nil
	}
	// Refused rather than reported as done: a return is decided once, and saying
	// so is what tells the staff member the decision was somebody else's.
	return fmt.Errorf(
		"%w: return %s is already approved and its refund has landed", ErrRefused, row.ID)
}

// returnUnderDecision reads the return this decision is about and says whether
// it is a first decision or a RETRY of a payout that did not complete.
//
// An already-approved return being approved again is not a second decision. The
// claim moves ahead of the money, which is what stops two staff members both
// paying, so a refund that failed no longer leaves the return open to decide;
// pressing 同意 once more resumes the payout. An ambiguous open provider attempt
// reuses its durable key; a known failed/cancelled attempt gets a DB-derived
// successor generation so Stripe may execute new work without losing lineage.
func (s *Store) returnUnderDecision(
	ctx context.Context, id string, kind returns.DecisionKind,
) (db.ReturnForDecisionRow, bool, error) {
	requestID, err := uuid.Parse(id)
	if err != nil {
		return db.ReturnForDecisionRow{}, false, ErrRefused
	}
	row, err := s.q.ReturnForDecision(ctx, requestID)
	if err != nil {
		return db.ReturnForDecisionRow{}, false, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	status := returns.ReturnStatus(row.Status)
	retry := status == returns.ReturnApproved && kind == returns.DecisionApprove
	if status != returns.ReturnRequested && !retry {
		return db.ReturnForDecisionRow{}, false,
			fmt.Errorf("%w: return %s is already %s", ErrRefused, id, row.Status)
	}
	return row, retry, nil
}

func parseAssessmentVersion(raw string) (int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n < 0 {
		return 0, formRefuse("decision", returns.RefuseIncomplete)
	}
	return int32(n), nil
}

func returnDecisionAudit(
	status returns.ReturnStatus, resolution string,
	window returns.PolicyWindow, entitlement returns.Entitlement,
	assessmentVersion int32,
) map[string]any {
	after := map[string]any{
		"decision":      string(status),
		"resolution":    resolution,
		"policy_window": string(window),
	}
	if entitlement != "" {
		after["entitlement"] = string(entitlement)
	}
	if assessmentVersion > 0 {
		after["assessment_version"] = assessmentVersion
	}
	return after
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
		return fmt.Errorf("%w: %w", ErrRefundIncomplete, payoutErr)
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
			return fmt.Errorf("%w: %w", ErrRefundIncomplete, err)
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
	settled := state == RefundSucceeded
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
	// The ledger post is synchronous and committed, even without a provider ref.
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
			ErrRefundIncomplete, row.ID, err)
	}
	return nil
}

// refundCard is the two-transaction dance around the provider. It answers with
// what the provider SAID: a refund Stripe accepted may be 'pending' or
// 'requires_action', and recording either as succeeded asserts money moved.
func (s *Store) refundCard(
	ctx context.Context, returnID uuid.UUID,
) (string, RefundState, error) {
	actorID, ok := actorFrom(ctx)
	if !ok {
		return "", "", fmt.Errorf("%w: refund provider attempt", ErrNoActor)
	}
	requestID := web.RequestID(ctx)
	if requestID == "" {
		return "", "", fmt.Errorf("%w: refund provider attempt has no request id", ErrRefused)
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
		return db.RefundExecutionRow{}, fmt.Errorf(
			"%w: claim refund execution for return %s: %w", ErrRefused, returnID, err)
	}
	claim, err := s.q.RefundExecution(ctx, refundID)
	if err != nil {
		return db.RefundExecutionRow{}, fmt.Errorf(
			"%w: read refund execution %s: %w", ErrRefused, refundID, err)
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
	if !errors.Is(providerErr, ErrRefundCreateRejected) {
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
	state RefundState,
) error {
	params := db.RecordRefundPendingParams{
		RefundID: refundID, ProviderRef: providerRef,
		ActorUserID: actorID, RequestID: requestID,
	}
	switch state {
	case RefundPending:
		_, err := s.q.RecordRefundPending(ctx, params)
		return err
	case RefundRequiresAction:
		_, err := s.q.RecordRefundRequiresAction(ctx, db.RecordRefundRequiresActionParams(params))
		return err
	case RefundSucceeded:
		_, err := s.q.RecordRefundSucceeded(ctx, db.RecordRefundSucceededParams(params))
		return err
	case RefundFailed:
		_, err := s.q.RecordRefundFailed(ctx, db.RecordRefundFailedParams(params))
		return err
	case RefundCancelled:
		_, err := s.q.RecordRefundCancelled(ctx, db.RecordRefundCancelledParams(params))
		return err
	default:
		panic("admin: unknown RefundState: " + string(state))
	}
}

func refundProviderResult(providerRef string, state RefundState) (string, RefundState, error) {
	switch state {
	case RefundSucceeded, RefundPending, RefundRequiresAction:
		// Stripe ACCEPTED it, so the return is approved either way: the shop took
		// the goods back, and that decision is not Stripe's to make pending.
		return providerRef, state, nil
	case RefundFailed, RefundCancelled:
		// Terminal and no money moved. A return is decided once, so closing it
		// here would leave a settled return, an unpaid customer and no door back.
		return "", state, fmt.Errorf(
			"the provider reports the refund as %s, so no money left — the return stays open",
			state)
	default:
		panic("admin: outcome switch accepted unknown RefundState: " + string(state))
	}
}

// LineEligibility is one line's three observed facts from the assess form.
type LineEligibility struct {
	OrderLineID uuid.UUID
	Unused      string
	Packaging   string
	Accessories string
}

func validAssessmentBasis(s string) bool {
	s = strings.TrimSpace(s)
	return utf8.ValidString(s) && utf8.RuneCountInString(s) > 0 &&
		utf8.RuneCountInString(s) <= maxAssessmentBasisRunes
}

func lineEligibilityByID(facts []LineEligibility) (map[uuid.UUID]LineEligibility, error) {
	byLine := make(map[uuid.UUID]LineEligibility, len(facts))
	for _, fact := range facts {
		if _, ok := returns.ParseFact(fact.Unused); !ok {
			return nil, formRefuse("unused-"+fact.OrderLineID.String(), returns.RefuseIncomplete)
		}
		if _, ok := returns.ParseFact(fact.Packaging); !ok {
			return nil, formRefuse("packaging-"+fact.OrderLineID.String(), returns.RefuseIncomplete)
		}
		if _, ok := returns.ParseFact(fact.Accessories); !ok {
			return nil, formRefuse("accessories-"+fact.OrderLineID.String(), returns.RefuseIncomplete)
		}
		byLine[fact.OrderLineID] = fact
	}
	return byLine, nil
}

func insertEligibilityFacts(
	ctx context.Context,
	q *db.Queries,
	assessment db.ReturnEligibilityAssessment,
	requestID uuid.UUID,
	lines []db.ReturnLinesRow,
	byLine map[uuid.UUID]LineEligibility,
) error {
	for i := range lines {
		line := &lines[i]
		fact := byLine[line.OrderLineID]
		unused, packaging, accessories := "unknown", "unknown", "unknown"
		if fact.OrderLineID == line.OrderLineID {
			unused, packaging, accessories = fact.Unused, fact.Packaging, fact.Accessories
		}
		if err := q.InsertEligibilityFact(ctx, db.InsertEligibilityFactParams{
			AssessmentID:        assessment.ID,
			OrderID:             line.OrderID,
			ReturnRequestID:     requestID,
			OrderLineID:         line.OrderLineID,
			Unused:              unused,
			PackagingComplete:   packaging,
			AccessoriesComplete: accessories,
			RequestedAt:         line.RequestedAt,
			DeliveredAt:         line.DeliveredAt,
			PolicyWindow:        line.PolicyWindow,
		}); err != nil {
			return fmt.Errorf("insert eligibility fact: %w", err)
		}
	}
	return nil
}

// Assess records a pre-decision eligibility observation. It does not pay
// and does not restock: those stay behind Decide and InspectReturn.
func (s *Store) Assess(ctx context.Context, id, basis string, facts []LineEligibility) error {
	basis = strings.TrimSpace(basis)
	if !validAssessmentBasis(basis) {
		return formRefuse("basis", returns.RefuseIncomplete)
	}
	actorID, ok := actorFrom(ctx)
	if !ok {
		return fmt.Errorf("%w: assess return", ErrNoActor)
	}
	requestID, err := uuid.Parse(id)
	if err != nil {
		return ErrRefused
	}
	byLine, err := lineEligibilityByID(facts)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin return assessment: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)
	if _, lockErr := q.LockReturnOrder(ctx, requestID); lockErr != nil {
		return fmt.Errorf("%w: lock return order for assessment: %w", ErrRefused, lockErr)
	}
	row, err := q.ReturnForDecision(ctx, requestID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}
	if returns.ReturnStatus(row.Status) != returns.ReturnRequested {
		return fmt.Errorf("%w: return %s is already %s", ErrRefused, id, row.Status)
	}
	lines, err := q.ReturnLines(ctx, []uuid.UUID{requestID})
	if err != nil {
		return fmt.Errorf("read return lines for assessment: %w", err)
	}
	if len(lines) == 0 {
		return ErrRefused
	}
	version, err := q.NextEligibilityVersion(ctx, requestID)
	if err != nil {
		return fmt.Errorf("next eligibility version: %w", err)
	}
	assessment, err := q.InsertEligibilityAssessment(ctx, db.InsertEligibilityAssessmentParams{
		OrderID:         row.OrderID,
		ReturnRequestID: requestID,
		Version:         version,
		AssessedBy:      actorID,
		Basis:           basis,
	})
	if err != nil {
		return fmt.Errorf("insert eligibility assessment: %w", err)
	}
	if err := insertEligibilityFacts(ctx, q, assessment, requestID, lines, byLine); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit return assessment: %w", err)
	}
	return nil
}

// closeReturn stamps the decision and appends to the order's history, together.
func (s *Store) closeReturn(
	ctx context.Context,
	requestID uuid.UUID,
	kind returns.DecisionKind,
	resolution string,
	assessmentVersion int32,
) error {
	if err := requireExceptionReason(kind, resolution); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin return decision: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)
	if _, lockErr := q.LockReturnOrder(ctx, requestID); lockErr != nil {
		return fmt.Errorf("%w: lock return order for decision: %w", ErrRefused, lockErr)
	}

	lines, err := q.ReturnLines(ctx, []uuid.UUID{requestID})
	if err != nil {
		return fmt.Errorf("read return lines for decision: %w", err)
	}
	_, claim, policyErr := decisionClaim(ctx, q, requestID, lines, kind, assessmentVersion)
	if policyErr != nil {
		return policyErr
	}

	// The row count is the decision. Decide's pre-check runs on the pool outside
	// this transaction, so the statement's own `status = 'requested'` is what
	// settles which of two staff members deciding at once wins — and it runs
	// BEFORE any money moves.
	decided, decideErr := q.DecideReturn(ctx, db.DecideReturnParams{
		ID: requestID, Status: string(kind.Status()), Resolution: text(resolution),
	})
	if decideErr != nil {
		return fmt.Errorf("%w: %w", ErrRefused, decideErr)
	}
	if decided == 0 {
		return fmt.Errorf("%w: return %s was decided by somebody else first",
			ErrRefused, requestID)
	}
	if err := auditIn(ctx, q, Event{
		Action: actionDecideReturn, Table: "return_requests", ID: nullableID(requestID),
		After: returnDecisionAudit(kind.Status(), resolution, claim.Window, claim.Entitlement, assessmentVersion),
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit return decision: %w", err)
	}
	return nil
}

func assessedLinesAtVersion(
	ctx context.Context,
	q *db.Queries,
	requestID uuid.UUID,
	lines []db.ReturnLinesRow,
	assessmentVersion int32,
) ([]returns.LineAssessment, error) {
	assessed := lineAssessmentsFromRows(lines)
	if assessmentVersion == 0 {
		return assessed, nil
	}
	latest, err := q.LatestEligibilityAssessment(ctx, requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, formRefuse("decision", returns.RefuseStale)
	}
	if err != nil {
		return nil, fmt.Errorf("lock eligibility assessment: %w", err)
	}
	if latest.Version != assessmentVersion {
		return nil, formRefuse("decision", returns.RefuseStale)
	}
	facts, factErr := q.EligibilityFacts(ctx, latest.ID)
	if factErr != nil {
		return nil, fmt.Errorf("read eligibility facts: %w", factErr)
	}
	return overlayEligibilityFacts(assessed, facts), nil
}

func decisionClaim(
	ctx context.Context,
	q *db.Queries,
	requestID uuid.UUID,
	lines []db.ReturnLinesRow,
	kind returns.DecisionKind,
	assessmentVersion int32,
) ([]returns.LineAssessment, returns.Claim, error) {
	assessed, err := assessedLinesAtVersion(ctx, q, requestID, lines, assessmentVersion)
	if err != nil {
		return nil, returns.Claim{}, err
	}
	claim, err := returns.Evaluate(assessed, kind)
	if err != nil {
		if refused, ok := errors.AsType[*returns.RefusalError](err); ok {
			return nil, returns.Claim{}, formRefuse("decision", refused.Kind)
		}
		return nil, returns.Claim{}, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return assessed, claim, nil
}

func lineAssessmentsFromRows(lines []db.ReturnLinesRow) []returns.LineAssessment {
	out := make([]returns.LineAssessment, 0, len(lines))
	for i := range lines {
		window, ok := returns.ParsePolicyWindow(lines[i].PolicyWindow)
		if !ok || window == returns.WindowMixed {
			window = returns.WindowUndelivered
		}
		out = append(out, returns.LineAssessment{
			OrderLineID: lines[i].OrderLineID.String(),
			Window:      window,
			Unused:      returns.FactUnknown,
			Packaging:   returns.FactUnknown,
			Accessories: returns.FactUnknown,
		})
	}
	return out
}

func overlayEligibilityFacts(
	lines []returns.LineAssessment, facts []db.ReturnEligibilityFact,
) []returns.LineAssessment {
	byLine := make(map[string]db.ReturnEligibilityFact, len(facts))
	for i := range facts {
		byLine[facts[i].OrderLineID.String()] = facts[i]
	}
	for i := range lines {
		f, ok := byLine[lines[i].OrderLineID]
		if !ok {
			continue
		}
		if window, ok := returns.ParsePolicyWindow(f.PolicyWindow); ok && window != returns.WindowMixed {
			lines[i].Window = window
		}
		if unused, ok := returns.ParseFact(f.Unused); ok {
			lines[i].Unused = unused
		}
		if packaging, ok := returns.ParseFact(f.PackagingComplete); ok {
			lines[i].Packaging = packaging
		}
		if accessories, ok := returns.ParseFact(f.AccessoriesComplete); ok {
			lines[i].Accessories = accessories
		}
	}
	return lines
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
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
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
		Action: actionInspectReturn, Table: "return_requests", ID: nullableID(requestID),
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
	if !validReturnResolution(resolution) {
		return fmt.Errorf("%w: return resolution exceeds %d characters",
			ErrInvalid, maxReturnResolutionRunes)
	}
	requestID, err := uuid.Parse(id)
	if err != nil {
		return ErrRefused
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin return completion: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)
	if _, lockErr := q.LockReturnOrder(ctx, requestID); lockErr != nil {
		return fmt.Errorf("%w: lock return order for completion: %w", ErrRefused, lockErr)
	}

	// return_requests_completed_is_inspected refuses this while any line is
	// un-inspected; the transition trigger also requires the frozen payout and
	// any required point clawback to be durably exact.
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
		Action: actionCompleteReturn, Table: "return_requests", ID: nullableID(requestID),
		After: map[string]any{"resolution": resolution},
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit return completion: %w", err)
	}
	return nil
}
