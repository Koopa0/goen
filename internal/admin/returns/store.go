// Package returns is the back office's returns desk: the queue, the decision
// that approves or rejects a return and has it paid back, the eligibility facts
// staff record before deciding, and the inspection and completion of what came
// back.
package returns

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/refundstate"
	returnrules "github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

var (
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("returns: refused")
	ErrInvalid = errors.New("returns: invalid input")
)

// Payouts pays an approved return back and says where each payout stands. A
// payout that did not finish wraps refundstate.ErrIncomplete; one the database
// refused wraps refundstate.ErrRefused.
type Payouts interface {
	FillPayouts(ctx context.Context, ids []uuid.UUID, rows []admin.Return) (map[uuid.UUID]error, error)
	PayApproved(ctx context.Context, returnID uuid.UUID, actor uuid.NullUUID) error
	Resume(ctx context.Context, row *db.ReturnForDecisionRow, actor uuid.NullUUID) (bool, error)
}

type Store struct {
	pool    *pgxpool.Pool
	q       *db.Queries
	payouts Payouts
}

func NewStore(pool *pgxpool.Pool, payouts Payouts) *Store {
	if pool == nil || payouts == nil {
		panic("returns: NewStore requires a pool and payouts")
	}
	return &Store{pool: pool, q: db.New(pool), payouts: payouts}
}

const maxAssessmentBasisRunes = 500

type FormRefusalError struct {
	Field string
	Kind  returnrules.RefusalKind
}

func (e *FormRefusalError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", ErrRefused.Error(), e.Kind)
}

func (e *FormRefusalError) Unwrap() error { return ErrRefused }

func formRefuse(field string, kind returnrules.RefusalKind) error {
	return &FormRefusalError{Field: field, Kind: kind}
}

// A first exception is the shop paying outside an advertised right.
// Without a recorded reason the audit cannot say why the money moved.
// Retries never call this: they resume an already-decided claim.
func requireExceptionReason(kind returnrules.DecisionKind, resolution string) error {
	if kind != returnrules.DecisionException {
		return nil
	}
	if strings.TrimSpace(resolution) != "" {
		return nil
	}
	return formRefuse("resolution", returnrules.RefuseExceptionReason)
}

// Queue is the back-office return view plus operator-only diagnostics.
// Diagnostics stay out of the rendered page but cross the Store boundary so
// the handler can record quantitative source inconsistencies.
type Queue struct {
	Bound        web.Bound
	Rows         []admin.Return
	payoutIssues []returnPayoutIssue
}

type returnPayoutIssue struct {
	returnID uuid.UUID
	err      error
}

// queuePosition is a reader's place in the queue. The query builds it as
// PageCursor, so its fields are the ordering values and nothing else.
type queuePosition struct {
	Rank     bool
	Priority bool
	At       time.Time
	ID       uuid.UUID
}

func (s *Store) Queue(ctx context.Context, after ...string) (Queue, error) {
	return s.QueueForRequest(ctx, "", after...)
}

func (s *Store) QueueForRequest(ctx context.Context, request string, after ...string) (Queue, error) {
	var requestID uuid.UUID
	if request != "" {
		id, err := uuid.Parse(request)
		if err != nil || id == uuid.Nil {
			return Queue{}, ErrInvalid
		}
		requestID = id
		request = id.String()
	}
	scope := web.ScopeURL("/admin/returns", "request", request)
	from, resumed := web.ResumeKeyset(scope, after, func(p queuePosition) bool { return p.ID != uuid.Nil })
	rows, err := s.q.ReturnQueue(ctx, db.ReturnQueueParams{HasRequest: request != "", RequestID: requestID, HasCursor: resumed, AfterRank: from.Rank, AfterPriority: from.Priority, AfterAt: from.At, AfterID: from.ID, RowLimit: web.PageLimit})
	if err != nil {
		return Queue{}, fmt.Errorf("read return queue: %w", err)
	}
	// Dropped before ids is built, not after: the extra row exists to be
	// counted, and reading its lines and payout facts would be work done for a
	// return nobody is shown.
	rows, bound := web.PageBound(scope, resumed, rows, web.PageSize, func(r *db.ReturnQueueRow) string { return r.PageCursor })
	ids := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	lines, err := s.q.ReturnLines(ctx, ids)
	if err != nil {
		return Queue{}, fmt.Errorf("read return lines: %w", err)
	}
	byRequest := make(map[uuid.UUID][]admin.ReturnLine, len(rows))
	for i := range lines {
		l := &lines[i]
		byRequest[l.ReturnRequestID] = append(byRequest[l.ReturnRequestID], admin.ReturnLine{
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
			Window:      returnrules.PolicyWindow(l.PolicyWindow),
		})
	}
	assessments, err := s.q.LatestEligibilityAssessments(ctx, ids)
	if err != nil {
		return Queue{}, fmt.Errorf("read return assessments: %w", err)
	}
	assessmentIDs := make([]uuid.UUID, 0, len(assessments))
	assessmentByRequest := make(map[uuid.UUID]db.ReturnEligibilityAssessment, len(assessments))
	for i := range assessments {
		assessmentIDs = append(assessmentIDs, assessments[i].ID)
		assessmentByRequest[assessments[i].ReturnRequestID] = assessments[i]
	}
	if overlayErr := overlayReturnAssessmentFacts(ctx, s.q, byRequest, assessmentIDs); overlayErr != nil {
		return Queue{}, overlayErr
	}

	view := buildReturnQueue(ctx, rows, byRequest, assessmentByRequest)
	blocked, err := s.payouts.FillPayouts(ctx, ids, view.Rows)
	if err != nil {
		return Queue{}, err
	}
	for _, id := range ids {
		if payoutErr, ok := blocked[id]; ok {
			view.payoutIssues = append(view.payoutIssues, returnPayoutIssue{returnID: id, err: payoutErr})
		}
	}
	// Set here rather than in the builder, which is given rows and knows
	// nothing about the read that produced them.
	view.Bound = bound
	return view, nil
}

func overlayReturnAssessmentFacts(
	ctx context.Context,
	q *db.Queries,
	byRequest map[uuid.UUID][]admin.ReturnLine,
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
			lines[j].Window = returnrules.PolicyWindow(f.PolicyWindow)
		}
		byRequest[f.ReturnRequestID] = lines
	}
	return nil
}

func buildReturnQueue(
	ctx context.Context,
	rows []db.ReturnQueueRow,
	byRequest map[uuid.UUID][]admin.ReturnLine,
	assessmentByRequest map[uuid.UUID]db.ReturnEligibilityAssessment,
) Queue {
	view := Queue{}
	for i := range rows {
		r := &rows[i]
		item := admin.Return{
			ID:          r.ID.String(),
			OrderNumber: r.OrderNumber,
			Status:      returnrules.Status(r.Status),
			StatusText:  statusText(ctx, returnrules.Status(r.Status), r.BeforeShipment),
			Reason:      r.Reason,
			Units:       r.Units,
			AmountCents: r.RefundableCents,
			CreatedAt:   shoptime.Minute(r.CreatedAt),
			Decided:     returnrules.Status(r.Status) != returnrules.StatusRequested,
			Lines:       byRequest[r.ID],
			Window:      returnrules.PolicyWindow(r.RescissionWindow),

			BeforeShipment: r.BeforeShipment,
		}
		if a, ok := assessmentByRequest[r.ID]; ok {
			item.AssessmentVersion = a.Version
			item.AssessmentBasis = a.Basis
			item.AssessedAt = shoptime.Minute(a.AssessedAt)
		}
		view.Rows = append(view.Rows, item)
	}
	return view
}

// Decide approves or rejects a return, and pays the money back when it
// approves. The refund row is committed BEFORE Stripe is called and settled
// after, so a crash between the two leaves something reconciliation can find;
// an ambiguous attempt reuses its provider key, while a known terminal attempt
// gets a fresh DB-derived key and immutable successor row.
func (s *Store) Decide(
	ctx context.Context, id, decision, resolution, assessmentVersion string,
) error {
	if !returnrules.ValidResolution(resolution) {
		return fmt.Errorf("%w: return resolution exceeds %d characters",
			ErrInvalid, returnrules.MaxResolutionRunes)
	}
	actorID, ok := audit.Actor(ctx)
	if !ok {
		return fmt.Errorf("%w: decide return", audit.ErrNoActor)
	}
	// The signed-in context keeps the return audit, provider attempt and
	// store-credit posting attributed to the same person.
	actor := uuid.NullUUID{UUID: actorID, Valid: true}

	kind, ok := returnrules.ParseDecisionKind(decision)
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
	worked, err := s.payouts.Resume(ctx, row, actor)
	if err != nil && !errors.Is(err, refundstate.ErrIncomplete) && !errors.Is(err, refundstate.ErrRefused) {
		return fmt.Errorf("%w: %w", refundstate.ErrIncomplete, err)
	}
	if err != nil || worked {
		return err
	}
	// Refused rather than reported as done: a return is decided once, and saying
	// so is what tells the staff member the decision was somebody else's.
	return fmt.Errorf(
		"%w: return %s is already approved and its refund has landed", ErrRefused, row.ID)
}

func (s *Store) decideReturnFirst(
	ctx context.Context,
	row *db.ReturnForDecisionRow,
	kind returnrules.DecisionKind,
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
	if kind == returnrules.DecisionReject {
		return nil
	}
	err := s.payouts.PayApproved(ctx, row.ID, actor)
	if err != nil && !errors.Is(err, refundstate.ErrIncomplete) {
		return fmt.Errorf("%w: %w", refundstate.ErrIncomplete, err)
	}
	return err
}

// refusedIfNoRow reports a missing row as ErrRefused and any other error as the
// failure it is: a lock that timed out is the database not answering, not a
// rule refusing the write.
func refusedIfNoRow(err error, doing string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return fmt.Errorf("%s: %w", doing, err)
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
	ctx context.Context, id string, kind returnrules.DecisionKind,
) (db.ReturnForDecisionRow, bool, error) {
	requestID, err := uuid.Parse(id)
	if err != nil {
		return db.ReturnForDecisionRow{}, false, ErrRefused
	}
	row, err := s.q.ReturnForDecision(ctx, requestID)
	if err != nil {
		return db.ReturnForDecisionRow{}, false, refusedIfNoRow(err, "read return "+id)
	}
	status := returnrules.Status(row.Status)
	retry := status == returnrules.StatusApproved && kind == returnrules.DecisionApprove
	if status != returnrules.StatusRequested && !retry {
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
		return 0, formRefuse("decision", returnrules.RefuseIncomplete)
	}
	return int32(n), nil
}

func returnDecisionAudit(
	status returnrules.Status, resolution string,
	window returnrules.PolicyWindow, entitlement returnrules.Entitlement,
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
		if _, ok := returnrules.ParseFact(fact.Unused); !ok {
			return nil, formRefuse("unused-"+fact.OrderLineID.String(), returnrules.RefuseIncomplete)
		}
		if _, ok := returnrules.ParseFact(fact.Packaging); !ok {
			return nil, formRefuse("packaging-"+fact.OrderLineID.String(), returnrules.RefuseIncomplete)
		}
		if _, ok := returnrules.ParseFact(fact.Accessories); !ok {
			return nil, formRefuse("accessories-"+fact.OrderLineID.String(), returnrules.RefuseIncomplete)
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
		return formRefuse("basis", returnrules.RefuseIncomplete)
	}
	actorID, ok := audit.Actor(ctx)
	if !ok {
		return fmt.Errorf("%w: assess return", audit.ErrNoActor)
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
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	if _, lockErr := q.LockReturnOrder(ctx, requestID); lockErr != nil {
		return refusedIfNoRow(lockErr, "lock return order for assessment")
	}
	row, err := q.ReturnForDecision(ctx, requestID)
	if err != nil {
		return refusedIfNoRow(err, "read return "+id)
	}
	if returnrules.Status(row.Status) != returnrules.StatusRequested {
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

func (s *Store) closeReturn(
	ctx context.Context,
	requestID uuid.UUID,
	kind returnrules.DecisionKind,
	resolution string,
	assessmentVersion int32,
) error {
	if kind == returnrules.DecisionReject && strings.TrimSpace(resolution) == "" {
		return formRefuse("resolution", returnrules.RefuseRejectionReason)
	}
	if err := requireExceptionReason(kind, resolution); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin return decision: %w", err)
	}
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	if _, lockErr := q.LockReturnOrder(ctx, requestID); lockErr != nil {
		return refusedIfNoRow(lockErr, "lock return order for decision")
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
		ID: requestID, Status: string(kind.Status()), Resolution: pgtype.Text{String: resolution, Valid: resolution != ""},
	})
	if decideErr != nil {
		return pgerr.WrapRefusal(decideErr, ErrRefused)
	}
	if decided == 0 {
		return fmt.Errorf("%w: return %s was decided by somebody else first",
			ErrRefused, requestID)
	}
	if err := audit.In(ctx, q, audit.Event{
		Action: audit.ActionDecideReturn, Table: "return_requests", ID: audit.EntityID(requestID),
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
) ([]returnrules.LineAssessment, error) {
	assessed := lineAssessmentsFromRows(lines)
	if assessmentVersion == 0 {
		return assessed, nil
	}
	latest, err := q.LatestEligibilityAssessment(ctx, requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, formRefuse("decision", returnrules.RefuseStale)
	}
	if err != nil {
		return nil, fmt.Errorf("lock eligibility assessment: %w", err)
	}
	if latest.Version != assessmentVersion {
		return nil, formRefuse("decision", returnrules.RefuseStale)
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
	kind returnrules.DecisionKind,
	assessmentVersion int32,
) ([]returnrules.LineAssessment, returnrules.Claim, error) {
	assessed, err := assessedLinesAtVersion(ctx, q, requestID, lines, assessmentVersion)
	if err != nil {
		return nil, returnrules.Claim{}, err
	}
	claim, err := returnrules.Evaluate(assessed, kind)
	if err != nil {
		if refused, ok := errors.AsType[*returnrules.RefusalError](err); ok {
			return nil, returnrules.Claim{}, formRefuse("decision", refused.Kind)
		}
		return nil, returnrules.Claim{}, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return assessed, claim, nil
}

func lineAssessmentsFromRows(lines []db.ReturnLinesRow) []returnrules.LineAssessment {
	out := make([]returnrules.LineAssessment, 0, len(lines))
	for i := range lines {
		window, ok := returnrules.ParsePolicyWindow(lines[i].PolicyWindow)
		if !ok || window == returnrules.WindowMixed {
			window = returnrules.WindowUndelivered
		}
		out = append(out, returnrules.LineAssessment{
			OrderLineID: lines[i].OrderLineID.String(),
			Window:      window,
			Unused:      returnrules.FactUnknown,
			Packaging:   returnrules.FactUnknown,
			Accessories: returnrules.FactUnknown,
		})
	}
	return out
}

func overlayEligibilityFacts(
	lines []returnrules.LineAssessment, facts []db.ReturnEligibilityFact,
) []returnrules.LineAssessment {
	byLine := make(map[string]db.ReturnEligibilityFact, len(facts))
	for i := range facts {
		byLine[facts[i].OrderLineID.String()] = facts[i]
	}
	for i := range lines {
		f, ok := byLine[lines[i].OrderLineID]
		if !ok {
			continue
		}
		if window, ok := returnrules.ParsePolicyWindow(f.PolicyWindow); ok && window != returnrules.WindowMixed {
			lines[i].Window = window
		}
		if unused, ok := returnrules.ParseFact(f.Unused); ok {
			lines[i].Unused = unused
		}
		if packaging, ok := returnrules.ParseFact(f.PackagingComplete); ok {
			lines[i].Packaging = packaging
		}
		if accessories, ok := returnrules.ParseFact(f.AccessoriesComplete); ok {
			lines[i].Accessories = accessories
		}
	}
	return lines
}

type LineInspection struct {
	OrderLineID uuid.UUID
	Received    int32
	Restocked   int32
	Note        string
}

// checkInspection refuses counts return_request_lines_restocked_bounded would
// refuse anyway, so a staff member gets a sentence instead of a constraint name.
func checkInspection(lines []LineInspection) error {
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

// Inspect records what came back and puts the sellable units on the
// shelf, the whole parcel in ONE transaction. The restock is read back from
// what this transaction WROTE, so the set acted on is the one the database
// agreed to.
func (s *Store) Inspect(
	ctx context.Context, id string, lines []LineInspection, actor uuid.NullUUID,
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
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	// The order before any line, as every return write takes them: each line's
	// UPDATE locks the line and then, in return_within_shipment, the order, so
	// two inspections naming the lines in different orders would each hold a
	// line the other waits for.
	if _, lockErr := q.LockReturnOrder(ctx, requestID); lockErr != nil {
		return refusedIfNoRow(lockErr, "lock return order for inspection")
	}

	for _, l := range lines {
		n, inspectErr := q.InspectReturnLine(ctx, db.InspectReturnLineParams{
			RequestID: requestID, OrderLineID: l.OrderLineID,
			Received: l.Received, Restocked: l.Restocked, Note: l.Note,
		})
		if inspectErr != nil {
			return pgerr.WrapRefusal(inspectErr, ErrRefused)
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

	if err := audit.In(ctx, q, audit.Event{
		Action: audit.ActionInspectReturn, Table: "return_requests", ID: audit.EntityID(requestID),
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

// Complete closes an inspected return. It writes no stock: the movement
// was posted with the INSPECTION, which is when the goods went back on the
// shelf.
func (s *Store) Complete(ctx context.Context, id, resolution string) error {
	if !returnrules.ValidResolution(resolution) {
		return fmt.Errorf("%w: return resolution exceeds %d characters",
			ErrInvalid, returnrules.MaxResolutionRunes)
	}
	requestID, err := uuid.Parse(id)
	if err != nil {
		return ErrRefused
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin return completion: %w", err)
	}
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	if _, lockErr := q.LockReturnOrder(ctx, requestID); lockErr != nil {
		return refusedIfNoRow(lockErr, "lock return order for completion")
	}

	// return_requests_completed_is_inspected refuses this while any line is
	// un-inspected; the transition trigger also requires the frozen payout and
	// any required point clawback to be durably exact.
	closed, err := q.CompleteReturn(ctx, db.CompleteReturnParams{
		ID: requestID, Resolution: resolution,
	})
	if err != nil {
		return pgerr.WrapRefusal(err, ErrRefused)
	}
	if closed == 0 {
		return fmt.Errorf("%w: return %s is not open for completion", ErrRefused, requestID)
	}

	if err := audit.In(ctx, q, audit.Event{
		Action: audit.ActionCompleteReturn, Table: "return_requests", ID: audit.EntityID(requestID),
		After: map[string]any{"resolution": resolution},
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit return completion: %w", err)
	}
	return nil
}
