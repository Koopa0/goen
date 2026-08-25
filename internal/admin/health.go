package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Each threshold is a MULTIPLE of its worker's interval, so a healthy gap cannot alarm.
const (
	// OutboxStaleAfter is how old the oldest undelivered message may be.
	OutboxStaleAfter = 10 * time.Minute
	// MaxExpiredHolds is a COUNT, not a duration.
	MaxExpiredHolds = 50
	// CopurchaseStaleAfter is three refresh intervals.
	CopurchaseStaleAfter = 45 * time.Minute
	// MaxExpiredSessions and MaxUnreferencedMedia are counts.
	MaxExpiredSessions   = 500
	MaxUnreferencedMedia = 200
)

// WorkerHealth reads what the background workers have and have not done.
func (s *Store) WorkerHealth(ctx context.Context, messages *outbox.Store) (pages.WorkerHealthView, error) {
	row, err := s.q.WorkerHealth(ctx, outbox.MaxAttempts)
	if err != nil {
		return pages.WorkerHealthView{}, fmt.Errorf("read worker health: %w", err)
	}
	view := pages.WorkerHealthView{
		OutboxPending:        row.OutboxPending,
		OutboxOldest:         time.Duration(row.OutboxOldestSeconds) * time.Second,
		OutboxStuck:          row.OutboxStuck,
		ExpiredHolds:         row.ExpiredHolds,
		CopurchaseAge:        time.Duration(row.CopurchaseAgeSeconds) * time.Second,
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
		return pages.WorkerHealthView{}, fmt.Errorf("read stuck messages: %w", err)
	}
	for i := range stuck {
		m := &stuck[i]
		view.Stuck = append(view.Stuck, pages.StuckMessage{
			Topic: m.Topic, Key: m.DedupeKey, Attempts: m.Attempts,
			LastError: m.LastError, Since: m.Since.Format("2006-01-02 15:04"),
		})
	}

	// Stripe events that were accepted but need a person: an unreadable known
	// object, paid money with no local attribution, or money for a cancelled
	// order. NAMED rather than merely counted, because the event and object refs
	// are what let an operator investigate or refund each one at Stripe.
	unreconciled, err := s.q.UnreconciledPayments(ctx)
	if err != nil {
		return pages.WorkerHealthView{}, fmt.Errorf("read unreconciled payments: %w", err)
	}
	for i := range unreconciled {
		u := &unreconciled[i]
		view.Unreconciled = append(view.Unreconciled, pages.UnreconciledPayment{
			EventID: u.EventID, Type: u.Type, Ref: u.ObjectRef,
			Reason: u.Reason, Since: u.ReceivedAt.Format("2006-01-02 15:04"),
		})
	}

	// 折讓 claims the provider never answered. The claim is right to survive —
	// whether ECPay filed is not knowable from here — and that leaves a row only
	// a person can settle, which is exactly why it belongs on this page beside
	// the unreconciled payments. Without it the only sign was a 折讓 button that
	// refused, on one order, with a message about checking ECPay.
	stranded, err := s.q.StrandedInvoiceClaims(ctx)
	if err != nil {
		return pages.WorkerHealthView{}, fmt.Errorf("read stranded invoice claims: %w", err)
	}
	for i := range stranded {
		c := &stranded[i]
		view.StrandedClaims = append(view.StrandedClaims, pages.StrandedClaim{
			OrderNumber: c.OrderNumber, Kind: c.Kind,
			AmountCents: c.AmountCents,
			Since:       c.IssuedAt.Format("2006-01-02 15:04"),
		})
	}

	// goen consumes no refund webhook: this list is the only unpaid-customer alarm.
	open, err := s.q.OpenRefunds(ctx, OpenRefundListLimit)
	if err != nil {
		return pages.WorkerHealthView{}, fmt.Errorf("read open refunds: %w", err)
	}
	for i := range open {
		r := &open[i]
		view.OpenRefunds = append(view.OpenRefunds, pages.OpenRefund{
			OrderNumber: r.OrderNumber,
			Key:         r.RequestKey,
			Status:      r.Status,
			AmountCents: r.AmountCents,
			ProviderRef: r.ProviderRef,
			Since:       r.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	return view, nil
}

// StuckListLimit bounds the list beside the count.
const StuckListLimit = 20

// OpenRefundListLimit bounds the refund list.
const OpenRefundListLimit = 20
