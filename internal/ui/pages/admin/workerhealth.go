package admin

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/refundstate"
	"github.com/koopa0/goen/internal/ui/components"
)

type WorkerHealthView struct {
	OutboxPending int64
	// OutboxOldest is how long the most overdue message has been due, not old.
	OutboxOldest        time.Duration
	OutboxStuck         int64
	ExpiredHolds        int64
	CopurchaseAge       time.Duration
	CopurchaseEverBuilt bool

	ExpiredSessions   int64
	UnreferencedMedia int64
	// UnreconciledPayments is a Stripe event not automatically applied or a
	// provider-complete payment whose event outcome has not arrived.
	UnreconciledPayments int64
	Stuck                []StuckMessage
	// The two resolution subjects stay concrete: an event id and a provider ref
	// are different evidence and route to different database functions.
	UnreconciledEvents                 []UnreconciledEvent
	UnreconciledCompletePayments       []UnreconciledCompletePayment
	StrandedClaimCount                 int64
	StrandedClaimOldestSeconds         int64
	StrandedClaims                     []StrandedClaim
	Notice                             components.Result
	OpenRefundCount                    int64
	OpenRefunds                        []OpenRefund
	UninvoicedCount                    int64
	UninvoicedOldestSeconds            int64
	Uninvoiced                         []UninvoicedOrder
	CancelledOrderInvoiceCount         int64
	CancelledOrderInvoiceOldestSeconds int64
	CancelledOrderInvoices             []CancelledOrderInvoice
	Disputes                           DisputeState

	OutboxStaleAfter     time.Duration
	MaxExpiredHolds      int64
	CopurchaseStaleAfter time.Duration
	MaxExpiredSessions   int64
	MaxUnreferencedMedia int64

	Pools []PoolHealth
}

// PoolHealth is one connection pool's statistics. EmptyAcquires counts the
// acquisitions that had to wait for a connection, and AcquireWait is the time
// all acquisitions together spent waiting.
type PoolHealth struct {
	Name                         string
	Max, Acquired, Idle, Total   int32
	TotalAcquires, EmptyAcquires int64
	AcquireWait                  time.Duration
}

func (v *WorkerHealthView) OutboxHealthy() bool {
	return v.OutboxStuck == 0 &&
		(v.OutboxPending == 0 || v.OutboxOldest < v.OutboxStaleAfter)
}

func (v *WorkerHealthView) SweeperHealthy() bool { return v.ExpiredHolds <= v.MaxExpiredHolds }

func (v *WorkerHealthView) RecommendHealthy() bool {
	return v.CopurchaseEverBuilt && v.CopurchaseAge < v.CopurchaseStaleAfter
}

func (v *WorkerHealthView) HousekeepingHealthy() bool {
	return v.ExpiredSessions <= v.MaxExpiredSessions &&
		v.UnreferencedMedia <= v.MaxUnreferencedMedia
}

func (v *WorkerHealthView) HousekeepingText(ctx context.Context) string {
	if v.ExpiredSessions == 0 && v.UnreferencedMedia == 0 {
		return i18n.T(ctx, i18n.KeyHealthSweeperClear)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthSweeperBacklog),
		v.ExpiredSessions, v.UnreferencedMedia)
}

func (v *WorkerHealthView) RefundsHealthy() bool { return v.OpenRefundCount == 0 }

// PaymentsReconciled reports whether every accepted event was acted on. Any at
// all is unhealthy: each one is money at the provider against goods the shop has
// already taken back, and only a person can move it.
func (v *WorkerHealthView) PaymentsReconciled() bool { return v.UnreconciledPayments == 0 }

func (v *WorkerHealthView) AllHealthy() bool {
	return v.OutboxHealthy() && v.SweeperHealthy() &&
		v.RecommendHealthy() && v.HousekeepingHealthy() && v.RefundsHealthy() &&
		v.PaymentsReconciled() && v.ClaimsSettled() && v.PaidOrdersInvoiced() &&
		v.CancelledOrderInvoicesResolved() && v.Disputes.Healthy()
}

func (v *WorkerHealthView) StaffTaskCount() int64 {
	return v.UnreconciledPayments + v.UninvoicedCount + v.StrandedClaimCount
}

func (v *WorkerHealthView) StaffTaskText(ctx context.Context) string {
	count := v.StaffTaskCount()
	return i18n.Count(ctx, i18n.KeyAdminHPPendingTasks, count, count)
}

func (v *WorkerHealthView) FirstStaffTaskAnchor() string {
	switch {
	case len(v.UnreconciledEvents) > 0 || len(v.UnreconciledCompletePayments) > 0:
		return "#events-heading"
	case v.UninvoicedCount > 0:
		return "#uninvoiced-heading"
	case len(v.StrandedClaims) > 0:
		return "#claims-heading"
	default:
		return ""
	}
}

func (v *WorkerHealthView) PaidOrdersInvoiced() bool { return v.UninvoicedCount == 0 }

func (v *WorkerHealthView) UninvoicedText(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyAdminHPUninvoicedHint, v.UninvoicedCount, v.UninvoicedCount)
}

type UninvoicedOrder struct {
	OrderNumber string
	AmountCents int64
	Since       string
}

func (o UninvoicedOrder) Amount() string { return money.TWD(o.AmountCents) }

func (v *WorkerHealthView) CancelledOrderInvoicesResolved() bool {
	return v.CancelledOrderInvoiceCount == 0
}

func (v *WorkerHealthView) CancelledOrderInvoicesText(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyAdminHPCancelledOrderInvoicesHint, v.CancelledOrderInvoiceCount, v.CancelledOrderInvoiceCount)
}

// CancelledOrderInvoice is an issued 統一發票 its order's cancellation left live.
type CancelledOrderInvoice struct {
	OrderNumber string
	Number      string
	AmountCents int64
	IssuedOn    string
}

func (i CancelledOrderInvoice) Amount() string { return money.TWD(i.AmountCents) }

func (v *WorkerHealthView) OutboxText(ctx context.Context) string {
	switch {
	case v.OutboxStuck > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthOutboxStuck), v.OutboxStuck)
	case v.OutboxPending == 0:
		return i18n.T(ctx, i18n.KeyHealthOutboxClear)
	case v.OutboxOldest >= v.OutboxStaleAfter:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthOutboxOverdue),
			v.OutboxPending, humanDuration(ctx, v.OutboxOldest))
	default:
		if v.OutboxOldest == 0 {
			return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthOutboxNotYetDue), v.OutboxPending)
		}
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthOutboxWaiting),
			v.OutboxPending, humanDuration(ctx, v.OutboxOldest))
	}
}

func (v *WorkerHealthView) SweeperText(ctx context.Context) string {
	if v.ExpiredHolds == 0 {
		return i18n.T(ctx, i18n.KeyHealthHoldsClear)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthHoldsStuck), v.ExpiredHolds)
}

func (v *WorkerHealthView) RefundsText(ctx context.Context) string {
	if v.OpenRefundCount == 0 {
		return i18n.T(ctx, i18n.KeyHealthRefundsClear)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthRefundsStuck), v.OpenRefundCount)
}

func (v *WorkerHealthView) RecommendText(ctx context.Context) string {
	if !v.CopurchaseEverBuilt {
		return i18n.T(ctx, i18n.KeyHealthProjectionNever)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthProjectionAge),
		humanDuration(ctx, v.CopurchaseAge))
}

func humanDuration(ctx context.Context, d time.Duration) string {
	switch {
	case d < time.Minute:
		return i18n.Count(ctx, i18n.KeyAdminSeconds, int64(int(d.Seconds())), int(d.Seconds()))
	case d < time.Hour:
		return i18n.Count(ctx, i18n.KeyAdminMinutes, int64(int(d.Minutes())), int(d.Minutes()))
	case d < 24*time.Hour:
		return i18n.Count(ctx, i18n.KeyAdminHours, int64(int(d.Hours())), int(d.Hours()))
	default:
		return i18n.Count(ctx, i18n.KeyAdminDays, int64(int(d.Hours()/24)), int(d.Hours()/24))
	}
}

type UnreconciledEvent struct {
	EventID string
	Type    string
	Ref     string
	Reason  string
	Since   string
	// RefundOrderNumber and RefundCents are goen's own succeeded refund a
	// refund.failed event names; "" and 0 for every other event.
	RefundOrderNumber string
	RefundCents       int64
}

func (u UnreconciledEvent) TypeText(ctx context.Context) string {
	var key i18n.Key
	switch u.Type {
	case "checkout.session.completed":
		key = i18n.KeyAdminHPEventCompleted
	case "checkout.session.async_payment_succeeded":
		key = i18n.KeyAdminHPEventPaid
	case "checkout.session.async_payment_failed":
		key = i18n.KeyAdminHPEventFailed
	case "checkout.session.expired":
		key = i18n.KeyAdminHPEventExpired
	case "refund.failed":
		key = i18n.KeyAdminHPEventRefundFailed
	default:
		key = i18n.KeyAdminHPEventUnknown
	}
	return i18n.T(ctx, key)
}

func (u UnreconciledEvent) ReasonText(ctx context.Context) string {
	cause, _, _ := strings.Cut(u.Reason, ":")
	var key i18n.Key
	switch cause {
	case "unreadable_event":
		key = i18n.KeyAdminHPEventUnreadable
	case "unattributed_capture":
		key = i18n.KeyAdminHPEventUnattributed
	case "cancelled_order_capture":
		key = i18n.KeyAdminHPEventCancelledCapture
	case "refused_capture":
		key = i18n.KeyAdminHPEventRefusedCapture
	case "unsettled_session":
		key = i18n.KeyAdminHPEventUnsettled
	case "refund_failed":
		key = i18n.KeyAdminHPRefundFailedAtStripe
	default:
		key = i18n.KeyAdminHPReasonUnknown
	}
	return i18n.T(ctx, key)
}

func (u UnreconciledEvent) RefundAmount() string { return money.TWD(u.RefundCents) }

type UnreconciledCompletePayment struct {
	OrderNumber            string
	ProviderRef            string
	PaidAttributionAllowed bool
	Since                  string
}

// Reason explains the concrete recovery choice for this payment. Once any
// hold was released, capture is no longer one of those choices.
func (p UnreconciledCompletePayment) Reason(ctx context.Context) string {
	if !p.PaidAttributionAllowed {
		return i18n.T(ctx, i18n.KeyAdminHPCompleteStockReleased)
	}
	return i18n.T(ctx, i18n.KeyAdminHPCompleteOutcomeUnknown)
}

type StuckMessage struct {
	Topic       string
	Key         string
	Attempts    int32
	LastError   string
	NextAttempt string
}

func (m StuckMessage) AttemptsText() string { return strconv.FormatInt(int64(m.Attempts), 10) }

func (m StuckMessage) Reason(ctx context.Context) string {
	if m.LastError == "" {
		return i18n.T(ctx, i18n.KeyHealthNoReason)
	}
	return m.LastError
}

type OpenRefund struct {
	OrderNumber string
	Key         string
	Status      refundstate.State
	AmountCents int64
	ProviderRef string
	Since       string
}

func (r OpenRefund) Amount() string { return money.TWD(r.AmountCents) }

func (r OpenRefund) StatusText(ctx context.Context) string {
	switch r.Status {
	case refundstate.Pending:
		return i18n.T(ctx, i18n.KeyHealthRefundPending)
	case refundstate.RequiresAction:
		return i18n.T(ctx, i18n.KeyHealthRefundAction)
	case refundstate.Failed:
		return i18n.T(ctx, i18n.KeyHealthRefundFailed)
	case refundstate.Cancelled:
		return i18n.T(ctx, i18n.KeyHealthRefundCancelled)
	default:
		return string(r.Status)
	}
}

func (r OpenRefund) NextStep(ctx context.Context) string {
	var key i18n.Key
	switch r.Status {
	case refundstate.Pending:
		key = i18n.KeyHealthRefundPendingNext
	case refundstate.RequiresAction:
		key = i18n.KeyHealthRefundActionNext
	case refundstate.Failed:
		key = i18n.KeyHealthRefundFailedNext
	case refundstate.Cancelled:
		key = i18n.KeyHealthRefundCancelledNext
	default:
		return i18n.T(ctx, i18n.KeyHealthRefundExternalNext)
	}
	next := i18n.T(ctx, key)
	if strings.HasPrefix(r.Key, "return:") {
		return next + " " + fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthRefundRetryNext),
			i18n.T(ctx, i18n.KeyAdminRefundResume), i18n.T(ctx, i18n.KeyAdminRetRetryPayout))
	}
	return next + " " + i18n.T(ctx, i18n.KeyHealthRefundExternalNext)
}

func (r OpenRefund) Reference(ctx context.Context) string {
	if r.ProviderRef == "" {
		return i18n.T(ctx, i18n.KeyHealthNoRef)
	}
	return r.ProviderRef
}

type StrandedClaim struct {
	Operation   string
	OrderNumber string
	Kind        string
	Status      string
	AmountCents int64
	Attempts    int32
	Sends       int32
	LastError   string
	Since       string
	// CanAuthorizeResend is true only after an ambiguous Allowance send has
	// remained absent beyond the propagation window and has no live worker lease.
	CanAuthorizeResend bool
}

func (c StrandedClaim) ExplainedKindText(ctx context.Context) string {
	if key, known := InvoiceOperationLabels[c.Kind]; known {
		return i18n.T(ctx, key)
	}
	return i18n.T(ctx, i18n.KeyAdminHPInvoiceKindUnknown)
}

func (c StrandedClaim) ExplainedStatusText(ctx context.Context) string {
	if key, known := InvoiceOperationStatuses[c.Status]; known {
		return i18n.T(ctx, key)
	}
	return i18n.T(ctx, i18n.KeyAdminHPInvoiceStatusUnknown)
}

var invoiceReasonLabels = map[string]i18n.Key{
	"issue_lookup_failed":                       i18n.KeyAdminHPInvoiceLookupFailed,
	"issue_post_send_lookup_failed":             i18n.KeyAdminHPInvoiceLookupFailed,
	"allowance_lookup_failed":                   i18n.KeyAdminHPInvoiceLookupFailed,
	"allowance_known_facts_failed":              i18n.KeyAdminHPInvoiceLookupFailed,
	"void_lookup_failed":                        i18n.KeyAdminHPInvoiceLookupFailed,
	"issue_send_ambiguous":                      i18n.KeyAdminHPInvoiceAwaitingConfirmation,
	"allowance_send_ambiguous":                  i18n.KeyAdminHPInvoiceAwaitingConfirmation,
	"void_send_needs_lookup":                    i18n.KeyAdminHPInvoiceAwaitingConfirmation,
	"issue_not_yet_visible":                     i18n.KeyAdminHPInvoiceAwaitingConfirmation,
	"allowance_not_yet_visible":                 i18n.KeyAdminHPInvoiceAwaitingConfirmation,
	"allowance_awaiting_buyer":                  i18n.KeyAdminHPInvoiceAwaitingBuyer,
	"allowance_invalid_reconcile_failed":        i18n.KeyAdminHPInvoiceRecordFailed,
	"allowance_invalid_record_failed":           i18n.KeyAdminHPInvoiceRecordFailed,
	"issue_local_settlement_failed":             i18n.KeyAdminHPInvoiceRecordFailed,
	"allowance_consent_encoding_failed":         i18n.KeyAdminHPInvoiceRecordFailed,
	"allowance_local_settlement_failed":         i18n.KeyAdminHPInvoiceRecordFailed,
	"void_local_settlement_failed":              i18n.KeyAdminHPInvoiceRecordFailed,
	"allowance_invalid_reconcile_lost_lease":    i18n.KeyAdminHPInvoiceProcessingChanged,
	"allowance_invalid_record_lost_lease":       i18n.KeyAdminHPInvoiceProcessingChanged,
	"void_provider_rejected":                    i18n.KeyAdminHPInvoiceProviderRejected,
	"":                                          i18n.KeyHealthNoReason,
	"allowance_multiple_unknown_candidates":     i18n.KeyAdminHPInvoiceMultipleCandidates,
	"allowance_candidate_without_send_evidence": i18n.KeyAdminHPInvoiceUnattributed,
	"allowance_buyer_unconfirmed":               i18n.KeyAdminHPInvoiceBuyerUnconfirmed,
	"allowance_amount_still_held":               i18n.KeyAdminHPInvoiceAmountHeld,
	"allowance_success_mismatch":                i18n.KeyAdminHPInvoiceMismatch,
	"issue_lookup_mismatch":                     i18n.KeyAdminHPInvoiceMismatch,
	"allowance_lookup_mismatch":                 i18n.KeyAdminHPInvoiceMismatch,
	"void_lookup_mismatch":                      i18n.KeyAdminHPInvoiceMismatch,
	"void_success_identity_mismatch":            i18n.KeyAdminHPInvoiceMismatch,
	"allowance_known_document_mismatch":         i18n.KeyAdminHPInvoiceMismatch,
	"frozen_issue_request_invalid":              i18n.KeyAdminHPInvoiceRequestInvalid,
	"frozen_allowance_request_invalid":          i18n.KeyAdminHPInvoiceRequestInvalid,
	"frozen_void_request_invalid":               i18n.KeyAdminHPInvoiceRequestInvalid,
	"allowance_known_document_malformed":        i18n.KeyAdminHPInvoiceRecordsInvalid,
	"allowance_known_document_duplicate":        i18n.KeyAdminHPInvoiceRecordsInvalid,
	"allowance_invalid_after_current_send":      i18n.KeyAdminHPInvoiceContradiction,
	"allowance_provider_reactivated":            i18n.KeyAdminHPInvoiceContradiction,
	"allowance_invalid_reconcile_contradiction": i18n.KeyAdminHPInvoiceContradiction,
	"allowance_invalid_record_contradiction":    i18n.KeyAdminHPInvoiceContradiction,
	"allowance_known_document_status_unknown":   i18n.KeyAdminHPInvoiceUnsupported,
	"unknown_operation_kind":                    i18n.KeyAdminHPInvoiceUnsupported,
}

func (c StrandedClaim) ReasonText(ctx context.Context) string {
	if key, known := invoiceReasonLabels[c.LastError]; known {
		return i18n.T(ctx, key)
	}
	if strings.HasPrefix(c.LastError, "issue_provider_rejected_") || strings.HasPrefix(c.LastError, "allowance_provider_rejected_") {
		return i18n.T(ctx, i18n.KeyAdminHPInvoiceProviderRejected)
	}
	return i18n.T(ctx, i18n.KeyAdminHPReasonUnknown)
}

func (c StrandedClaim) Amount() string { return money.TWD(c.AmountCents) }

// BuyerNeverAgreed is an online allowance whose consent link lapsed, which a
// resend asks again rather than replacing a request ECPay never received.
func (c StrandedClaim) BuyerNeverAgreed() bool {
	return c.LastError == invoice.CategoryBuyerUnconfirmed
}

func (c StrandedClaim) AttemptsText() string {
	return fmt.Sprintf("%d / %d", c.Attempts, c.Sends)
}

func (v *WorkerHealthView) ClaimsSettled() bool { return v.StrandedClaimCount == 0 }

// Tasks is every check on this page that needs a person, as the dashboard lists
// it, so the two cannot disagree about whether something is wrong. Each links
// to its own table, and carries an age where the query behind it lists items.
func (v *WorkerHealthView) Tasks() []Task {
	var tasks []Task
	add := func(healthy bool, label i18n.Key, count int64, href string, hasAge bool, ageSeconds int64) {
		if !healthy {
			tasks = append(tasks, Task{Label: label, Count: count, Href: href, Alert: true, HasAge: hasAge, AgeSeconds: ageSeconds})
		}
	}
	add(v.PaymentsReconciled(), i18n.KeyAdminQueueTaskPayments, v.UnreconciledPayments, "/admin/health#events-heading", false, 0)
	add(v.ClaimsSettled(), i18n.KeyAdminQueueTaskClaims, v.StrandedClaimCount, "/admin/health#claims-heading", true, v.StrandedClaimOldestSeconds)
	add(v.PaidOrdersInvoiced(), i18n.KeyAdminQueueTaskUninvoiced, v.UninvoicedCount, "/admin/health#uninvoiced-heading", true, v.UninvoicedOldestSeconds)
	add(v.CancelledOrderInvoicesResolved(), i18n.KeyAdminHPCancelledOrderInvoicesHeading, v.CancelledOrderInvoiceCount, "/admin/health#cancelled-order-invoices-heading", true, v.CancelledOrderInvoiceOldestSeconds)
	add(v.RefundsHealthy(), i18n.KeyAdminHPOpenRefundsHeading, v.OpenRefundCount, "/admin/health#refunds-heading", false, 0)
	add(v.SweeperHealthy(), i18n.KeyAdminQueueTaskHolds, v.ExpiredHolds, "/admin/health", false, 0)
	return tasks
}

// DisputeState is what Stripe said about disputes awaiting the shop's answer.
// Unknown means the read failed or timed out, which is not the same as none.
type DisputeState struct {
	Configured bool
	Unknown    bool
	// OrdersUnknown means the disputes are known but the lookup of their goen
	// orders failed, which is not the same as no order.
	OrdersUnknown bool
	Items         []OpenDispute
}

func (d DisputeState) Healthy() bool {
	return !d.Configured || (!d.Unknown && !d.OrdersUnknown && len(d.Items) == 0)
}

func (d DisputeState) Text(ctx context.Context) string {
	switch {
	case d.Unknown:
		return i18n.T(ctx, i18n.KeyHealthDisputesUnknown)
	case len(d.Items) == 0:
		return i18n.T(ctx, i18n.KeyHealthDisputesClear)
	default:
		return i18n.Count(ctx, i18n.KeyHealthDisputesOpen, int64(len(d.Items)), len(d.Items))
	}
}

// OpenDispute is a card dispute the shop can still answer. OrderNumber is empty
// when no goen payment matches it.
type OpenDispute struct {
	URL         string
	OrderNumber string
	AmountCents int64
	Currency    string
	RespondBy   string
}

// Amount leaves foreign figures in Stripe's Dashboard because their minor-unit
// exponent depends on the currency.
func (d OpenDispute) Amount() string {
	if d.Currency == "twd" {
		return money.TWD(d.AmountCents)
	}
	return strings.ToUpper(d.Currency)
}
