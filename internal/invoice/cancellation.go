package invoice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/shoptime"
)

// i18n-exempt: the 作廢原因 filed with the 財政部, never shown to a visitor.
const cancellationVoidReason = "訂單取消"

// VoidDeadline is the last moment ECPay voids an invoice issued at issuedAt:
// 23:59:59 on the 13th of the month that opens the next bimonthly period. By
// then the period has been filed with the 財政部, and only an allowance
// corrects the invoice.
func VoidDeadline(issuedAt time.Time) time.Time {
	local := shoptime.In(issuedAt)
	// Periods open in odd months; time.Date carries month 13 into January.
	nextPeriod := local.Month() - (local.Month()-1)%2 + 2
	return time.Date(local.Year(), nextPeriod, 13, 23, 59, 59, 0, local.Location())
}

// voidable reports whether a void can still correct the invoice at now. ECPay
// refuses one once an allowance has relieved part of the invoice.
func voidable(issuedAt, now time.Time, hasAllowance bool) bool {
	return !hasAllowance && !now.After(VoidDeadline(issuedAt))
}

// CorrectForCancellation voids the invoice of an order being cancelled, under
// the filing identity on ctx, when ECPay still accepts a void. An issue still
// in flight is finished first: the invoice was owed when the money arrived, so
// it is issued and then voided rather than dropped. With no 加值中心 configured,
// that issue is withdrawn instead. Past the void window the invoice is left
// live for an allowance.
func (s *Store) CorrectForCancellation(ctx context.Context, orderNumber string) error {
	if err := s.finishIssueInFlight(ctx, orderNumber); err != nil {
		return err
	}
	if !s.Enabled() {
		return nil
	}
	live, err := s.q.LiveInvoice(ctx, orderNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the invoice of %s: %w", orderNumber, err)
	}
	if !voidable(live.IssuedAt, time.Now(), live.HasAllowance) {
		return nil
	}
	actorID, requestID, err := filingAuditIdentity(ctx)
	if err != nil {
		return err
	}
	operationID, err := s.q.ClaimInvoiceVoid(ctx, db.ClaimInvoiceVoidParams{
		DocumentID: live.ID, Reason: cancellationVoidReason,
		ActorUserID: actorID, RequestID: requestID,
	})
	if err != nil {
		return mapVoidClaim(live.Number, err)
	}
	_, err = s.processClaim(ctx, operationID)
	return err
}

// EnqueueVoidDue writes the void a customer's cancellation owes, in the
// cancellation's transaction.
func EnqueueVoidDue(ctx context.Context, q *db.Queries, due *outbox.InvoiceVoidDue) error {
	return outbox.Enqueue(ctx, q, outbox.TopicInvoiceVoidDue, due.OrderNumber, due)
}

// ClaimVoidDue records the system's void of the invoice of an order its
// customer cancelled; the reconciler sends it. The customer's own cancellation,
// on a form that said the invoice would be voided, is the buyer's consent to the
// void, which the shop must keep. An issue still in flight is waited for
// through the outbox's retry, so the invoice owed when the credit was spent is
// issued and then voided; with no 加值中心 configured it is withdrawn instead.
// Past the void window nothing is claimed, and /admin/health lists the live
// invoice for staff.
func (s *Store) ClaimVoidDue(ctx context.Context, due *outbox.InvoiceVoidDue) error {
	issue, err := s.q.IssueInFlight(ctx, due.OrderNumber)
	switch {
	case err == nil && !s.Enabled():
		return s.withdrawIssue(ctx, issue)
	case err == nil:
		return fmt.Errorf("%w: the invoice of %s is still being issued", ErrPending, due.OrderNumber)
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("read the issue in flight for %s: %w", due.OrderNumber, err)
	}
	if !s.Enabled() {
		return nil
	}
	live, err := s.q.LiveInvoice(ctx, due.OrderNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the invoice of %s: %w", due.OrderNumber, err)
	}
	if !voidable(live.IssuedAt, time.Now(), live.HasAllowance) {
		return nil
	}
	_, err = s.q.ClaimSystemInvoiceVoid(ctx, db.ClaimSystemInvoiceVoidParams{
		DocumentID: live.ID, Reason: cancellationVoidReason, RequestID: due.Trigger,
	})
	return voidDueClaimOutcome(live.Number, err)
}

// voidDueClaimOutcome is nil for a claim made and for an invoice no void can
// reach: one voided since it was read, and one with no issue on record, which
// /admin/health lists. Any other refusal keeps the message queued.
func voidDueClaimOutcome(documentNumber string, err error) error {
	switch constraintName(err) {
	case "invoice_void_target", "invoice_void_issue_operation":
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim the void of %s: %w", documentNumber, err)
	}
	return nil
}

func (s *Store) finishIssueInFlight(ctx context.Context, orderNumber string) error {
	operationID, err := s.q.IssueInFlight(ctx, orderNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the issue in flight for %s: %w", orderNumber, err)
	}
	if !s.Enabled() {
		return s.withdrawIssue(ctx, operationID)
	}
	_, err = s.processClaim(ctx, operationID)
	return err
}

// withdrawIssue rejects an issue no 加值中心 ever received, so a cancelled sale
// is not held open by an invoice nothing will send. One already sent may exist
// at ECPay, so it stays for staff.
func (s *Store) withdrawIssue(ctx context.Context, operationID uuid.UUID) error {
	owner := uuid.New()
	leased, err := s.q.LeaseInvoiceOperation(ctx, db.LeaseInvoiceOperationParams{
		OperationID: operationID, LeaseOwner: owner, LeaseFor: interval(operationLease),
	})
	if err != nil {
		return fmt.Errorf("lease invoice operation %s: %w", operationID, err)
	}
	if leased == uuid.Nil {
		return fmt.Errorf("%w: operation %s is not due", ErrPending, operationID)
	}
	op, err := s.operation(ctx, leased)
	if err != nil {
		return fmt.Errorf("read invoice operation %s: %w", leased, err)
	}
	if op.Sends > 0 {
		return s.retry(ctx, &op, owner, "issue_sent_before_cancellation", ErrDisabled)
	}
	return s.reject(ctx, &op, owner, "issue_withdrawn_order_cancelled", nil)
}
