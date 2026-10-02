package admin

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

// Each threshold is a MULTIPLE of its worker's interval, so a healthy gap cannot alarm.
const (
	OutboxStaleAfter = 10 * time.Minute
	// MaxExpiredHolds is a COUNT, not a duration.
	MaxExpiredHolds = 50
	// CopurchaseStaleAfter is three refresh intervals.
	CopurchaseStaleAfter = 45 * time.Minute
	// MaxExpiredSessions and MaxUnreferencedMedia are counts.
	MaxExpiredSessions   = 500
	MaxUnreferencedMedia = 200
	// UninvoicedAfter is how long a paid order may go without an invoice
	// operation: many passes of the outbox and of the invoice reconciler.
	UninvoicedAfter = 15 * time.Minute
)

func (s *Store) WorkerHealth(ctx context.Context, messages *outbox.Store) (admin.WorkerHealthView, error) {
	row, err := s.q.WorkerHealth(ctx, outbox.MaxAttempts)
	if err != nil {
		return admin.WorkerHealthView{}, fmt.Errorf("read worker health: %w", err)
	}
	view := admin.WorkerHealthView{
		OutboxPending:        row.OutboxPending,
		OutboxOldest:         durationFromSeconds(row.OutboxOldestSeconds),
		OutboxStuck:          row.OutboxStuck,
		ExpiredHolds:         row.ExpiredHolds,
		CopurchaseAge:        durationFromSeconds(row.CopurchaseAgeSeconds),
		CopurchaseEverBuilt:  row.CopurchaseEverBuilt,
		ExpiredSessions:      row.ExpiredSessions,
		UnreferencedMedia:    row.UnreferencedMedia,
		UnreconciledPayments: row.UnreconciledPayments,

		OutboxStaleAfter:     OutboxStaleAfter,
		MaxExpiredHolds:      MaxExpiredHolds,
		CopurchaseStaleAfter: CopurchaseStaleAfter,
		MaxExpiredSessions:   MaxExpiredSessions,
		MaxUnreferencedMedia: MaxUnreferencedMedia,
	}

	stuck, err := messages.Stuck(ctx, StuckListLimit)
	if err != nil {
		return admin.WorkerHealthView{}, fmt.Errorf("read stuck messages: %w", err)
	}
	view.Stuck = stuckMessages(stuck)

	// Stripe events that were accepted but need a person. Named rather than
	// counted: the event and object refs are what let an operator investigate or
	// refund each one at Stripe.
	unreconciled, err := s.q.UnreconciledPayments(ctx)
	if err != nil {
		return admin.WorkerHealthView{}, fmt.Errorf("read unreconciled payments: %w", err)
	}
	view.UnreconciledEvents = unreconciledEvents(unreconciled)

	// A provider-complete Session can be known before any webhook arrives, and
	// a completed-but-unpaid event is understood without being a capture. The
	// payment identity itself is then the durable alarm and has its own typed
	// resolution door; do not hide it merely because no event row is flaggable.
	complete, err := s.q.UnreconciledCompletePayments(ctx)
	if err != nil {
		return admin.WorkerHealthView{}, fmt.Errorf("read complete payments awaiting reconciliation: %w", err)
	}
	view.UnreconciledCompletePayments = unreconciledCompletePayments(complete)

	// 折讓 claims the provider never answered. Whether ECPay filed is not knowable
	// from here, so the claim survives as a row only a person can settle.
	stranded, err := s.q.StrandedInvoiceClaims(ctx)
	if err != nil {
		return admin.WorkerHealthView{}, fmt.Errorf("read stranded invoice claims: %w", err)
	}
	view.StrandedClaims = strandedClaims(stranded)

	// goen consumes no refund webhook: this is the only unpaid-customer alarm.
	// Count independently of the bounded diagnostic sample below, or 37 open
	// refunds are rendered as 20 merely because the table stops at 20 rows.
	view.OpenRefundCount, err = s.q.OpenRefundCount(ctx)
	if err != nil {
		return admin.WorkerHealthView{}, fmt.Errorf("count open refunds: %w", err)
	}
	open, err := s.q.OpenRefunds(ctx, OpenRefundListLimit)
	if err != nil {
		return admin.WorkerHealthView{}, fmt.Errorf("read open refunds: %w", err)
	}
	view.OpenRefunds = openRefunds(open)

	view.Uninvoiced, view.UninvoicedCount, err = s.UninvoicedOrders(ctx, UninvoicedAfter)
	if err != nil {
		return admin.WorkerHealthView{}, err
	}
	return view, nil
}

func (s *Store) UninvoicedOrders(
	ctx context.Context, olderThan time.Duration,
) ([]admin.UninvoicedOrder, int64, error) {
	rows, err := s.q.UninvoicedOrders(ctx, pgtype.Interval{
		Microseconds: olderThan.Microseconds(), Valid: true,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("read paid orders with no invoice operation: %w", err)
	}
	out := make([]admin.UninvoicedOrder, len(rows))
	var total int64
	for i := range rows {
		r := &rows[i]
		total = r.Total
		out[i] = admin.UninvoicedOrder{
			OrderNumber: r.OrderNumber, AmountCents: r.AmountCents,
			Since: shoptime.Minute(r.FundedAt),
		}
	}
	return out, total, nil
}

func stuckMessages(rows []outbox.StuckMessage) []admin.StuckMessage {
	out := make([]admin.StuckMessage, len(rows))
	for i := range rows {
		m := &rows[i]
		out[i] = admin.StuckMessage{
			Topic: m.Topic, Key: m.DedupeKey, Attempts: m.Attempts,
			LastError: m.LastError, Since: shoptime.Minute(m.Since),
		}
	}
	return out
}

func unreconciledEvents(rows []db.UnreconciledPaymentsRow) []admin.UnreconciledEvent {
	out := make([]admin.UnreconciledEvent, len(rows))
	for i := range rows {
		u := &rows[i]
		out[i] = admin.UnreconciledEvent{
			EventID: u.EventID, Type: u.Type, Ref: u.ObjectRef,
			Reason: u.Reason, Since: shoptime.Minute(u.ReceivedAt),
		}
	}
	return out
}

func unreconciledCompletePayments(
	rows []db.UnreconciledCompletePaymentsRow,
) []admin.UnreconciledCompletePayment {
	out := make([]admin.UnreconciledCompletePayment, len(rows))
	for i := range rows {
		p := &rows[i]
		out[i] = admin.UnreconciledCompletePayment{
			OrderNumber: p.OrderNumber, ProviderRef: p.ProviderRef,
			PaidAttributionAllowed: p.PaidAttributionAllowed,
			Since:                  shoptime.Minute(p.CreatedAt),
		}
	}
	return out
}

func strandedClaims(rows []db.StrandedInvoiceClaimsRow) []admin.StrandedClaim {
	out := make([]admin.StrandedClaim, len(rows))
	for i := range rows {
		c := &rows[i]
		out[i] = admin.StrandedClaim{
			Operation: c.OperationID.String(), OrderNumber: c.OrderNumber,
			Kind: c.Kind, Status: c.Status, AmountCents: c.AmountCents,
			Attempts: c.ReconcileAttempts, Sends: c.SendAttempts,
			LastError: c.LastError, CanAuthorizeResend: c.CanAuthorizeResend,
			Since: shoptime.Minute(c.CreatedAt),
		}
	}
	return out
}

func openRefunds(rows []db.OpenRefundsRow) []admin.OpenRefund {
	out := make([]admin.OpenRefund, len(rows))
	for i := range rows {
		r := &rows[i]
		out[i] = admin.OpenRefund{
			OrderNumber: r.OrderNumber,
			Key:         r.RequestKey,
			Status:      r.Status,
			AmountCents: r.AmountCents,
			ProviderRef: r.ProviderRef,
			Since:       shoptime.Minute(r.CreatedAt),
		}
	}
	return out
}

// AuthorizeInvoiceAllowanceResend records a staff member's independent
// confirmation that ECPay has no Allowance for an aged ambiguous send. The SQL
// door owns all eligibility checks and atomically grants exactly one retry with
// the authenticated actor and request in the append-only audit trail.
func (s *Store) AuthorizeInvoiceAllowanceResend(
	ctx context.Context, operationID uuid.UUID,
) error {
	if operationID == uuid.Nil {
		return ErrInvalid
	}
	actorID, ok := audit.Actor(ctx)
	if !ok {
		return audit.ErrNoActor
	}
	requestID := web.RequestID(ctx)
	if requestID == "" {
		return audit.ErrNoActor
	}
	authorized, err := s.q.AuthorizeInvoiceAllowanceResend(
		ctx, db.AuthorizeInvoiceAllowanceResendParams{
			OperationID: operationID, ActorUserID: actorID, RequestID: requestID,
		},
	)
	if err != nil {
		return fmt.Errorf("authorize invoice allowance resend: %w", err)
	}
	if !authorized {
		return ErrNotFound
	}
	return nil
}

// durationFromSeconds saturates PostgreSQL's much wider timestamp range at the
// largest Go duration. A direct multiplication wraps after roughly 292 years;
// on this health page that wrap turns a centuries-old queue into a negative age
// which compares as healthy.
func durationFromSeconds(seconds int64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	if seconds > math.MaxInt64/int64(time.Second) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(seconds) * time.Second
}

const StuckListLimit = 20

const OpenRefundListLimit = 20
