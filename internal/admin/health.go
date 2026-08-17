package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ui/pages"
)

// The thresholds at which a figure stops being normal. Each is a MULTIPLE of the
// interval its worker runs on, so a healthy gap between two ticks cannot alarm.
const (
	// OutboxStaleAfter is how old the oldest undelivered message may be.
	OutboxStaleAfter = 10 * time.Minute
	// MaxExpiredHolds is a COUNT, not a duration: a few always exist between
	// sweeper runs.
	MaxExpiredHolds = 50
	// CopurchaseStaleAfter is three refresh intervals.
	CopurchaseStaleAfter = 45 * time.Minute
	// MaxExpiredSessions and MaxUnreferencedMedia are counts, for pruners that
	// run every six hours and every hour.
	MaxExpiredSessions   = 500
	MaxUnreferencedMedia = 200
)

// WorkerHealth reads what the background workers have and have not done.
//
// Every figure is derived from the WORK, never from a heartbeat: a worker
// looping without progress passes "I am running" and fails this.
func (s *Store) WorkerHealth(ctx context.Context, messages *outbox.Store) (pages.WorkerHealthView, error) {
	row, err := s.q.WorkerHealth(ctx, outbox.MaxAttempts)
	if err != nil {
		return pages.WorkerHealthView{}, fmt.Errorf("read worker health: %w", err)
	}
	view := pages.WorkerHealthView{
		OutboxPending:       row.OutboxPending,
		OutboxOldest:        time.Duration(row.OutboxOldestSeconds) * time.Second,
		OutboxStuck:         row.OutboxStuck,
		ExpiredHolds:        row.ExpiredHolds,
		CopurchaseAge:       time.Duration(row.CopurchaseAgeSeconds) * time.Second,
		CopurchaseEverBuilt: row.CopurchaseEverBuilt,
		ExpiredSessions:     row.ExpiredSessions,
		UnreferencedMedia:   row.UnreferencedMedia,

		OutboxStaleAfter:     OutboxStaleAfter,
		MaxExpiredHolds:      MaxExpiredHolds,
		CopurchaseStaleAfter: CopurchaseStaleAfter,
		MaxExpiredSessions:   MaxExpiredSessions,
		MaxUnreferencedMedia: MaxUnreferencedMedia,
	}

	// WHICH messages, not just how many: "3 stuck" is not something an operator
	// can act on.
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

	// goen consumes no refund webhook, so nothing settles a stalled refund by
	// itself: this list is the only thing that says a customer is unpaid.
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

// StuckListLimit bounds the list; the count beside it says how many there are.
const StuckListLimit = 20

// OpenRefundListLimit bounds the refund list.
const OpenRefundListLimit = 20
