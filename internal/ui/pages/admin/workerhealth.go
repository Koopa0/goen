package admin

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/koopa0/goen/internal/admin/refundstate"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/money"
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
	UnreconciledEvents           []UnreconciledEvent
	UnreconciledCompletePayments []UnreconciledCompletePayment
	StrandedClaims               []StrandedClaim
	Notice                       string
	OpenRefundCount              int64
	OpenRefunds                  []OpenRefund
	UninvoicedCount              int64
	Uninvoiced                   []UninvoicedOrder
	CancelledOrderInvoiceCount   int64
	CancelledOrderInvoices       []CancelledOrderInvoice

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
		v.CancelledOrderInvoicesResolved()
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
}

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
	Topic     string
	Key       string
	Attempts  int32
	LastError string
	Since     string
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

func (c StrandedClaim) Amount() string { return money.TWD(c.AmountCents) }

// BuyerNeverAgreed is an online allowance whose consent link lapsed, which a
// resend asks again rather than replacing a request ECPay never received.
func (c StrandedClaim) BuyerNeverAgreed() bool {
	return c.LastError == invoice.CategoryBuyerUnconfirmed
}

func (c StrandedClaim) AttemptsText() string {
	return fmt.Sprintf("%d / %d", c.Attempts, c.Sends)
}

func (v *WorkerHealthView) ClaimsSettled() bool { return len(v.StrandedClaims) == 0 }
