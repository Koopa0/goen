package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ui/pages"
)

// The thresholds at which a figure stops being normal.
//
// Each is a MULTIPLE of the interval the worker runs on, not a number somebody
// liked: a backlog older than several ticks means the ticks are not happening,
// and anything shorter would alarm on the gap between two healthy runs.
const (
	// OutboxStaleAfter is how old the oldest undelivered message may be.
	// The worker polls every 5s and retries with backoff, so ten minutes is
	// far past any legitimate retry sequence.
	OutboxStaleAfter = 10 * time.Minute
	// HoldsStaleAfter is not a duration but a COUNT: the sweeper runs on a
	// ticker and a few expired holds always exist between runs. Fifty is a
	// backlog rather than a moment.
	MaxExpiredHolds = 50
	// CopurchaseStaleAfter is three refresh intervals. One missed rebuild is a
	// slow database; three is a worker that is not running.
	CopurchaseStaleAfter = 45 * time.Minute
	// The housekeeping pruners run every six hours and every hour, so a healthy
	// system always carries some of both between ticks. These are the counts at
	// which the number stops looking like a gap and starts looking like a
	// worker that stopped.
	MaxExpiredSessions   = 500
	MaxUnreferencedMedia = 200
)

// WorkerHealth reads what the background workers have and have not done.
//
// Every figure is derived from the WORK — a count of undelivered messages, the
// age of the oldest one — rather than from a heartbeat the workers write. A
// heartbeat says "I am running"; these say "the work is being done", and those
// are different claims. A worker looping without making progress passes the
// first and fails the second.
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

	// WHICH messages, not just how many. A page that says "3 stuck" and cannot
	// name them tells an operator that something is wrong and nothing about
	// what to do.
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

	// The refunds nobody could see. `refunds` was written by the back office
	// and read by one sum, so a refund that stalled at the provider — the exact
	// state the row is committed BEFORE the call in order to record — existed
	// in the database and on no page. Nothing settles one by itself either:
	// goen consumes no refund webhook, so this list is the only thing that ever
	// says a customer has not been paid.
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

// StuckListLimit bounds the list. A page showing every stuck message in a
// backlog of ten thousand is a page nobody can read; the count beside it is
// what says how many there really are.
const StuckListLimit = 20

// OpenRefundListLimit bounds the refund list for the same reason, and is
// smaller: a shop with twenty refunds it has not settled has a problem no
// longer list would help with.
const OpenRefundListLimit = 20
