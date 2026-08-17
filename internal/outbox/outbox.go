// Package outbox delivers messages that were written in the same transaction as
// the fact they are about.
//
// It guarantees AT LEAST once, never exactly once: a worker can die after the
// provider accepted a message and before the row is stamped, so every handler
// must tolerate being run twice.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

// Topics goen publishes. A topic that fans out carries one message per
// RECIPIENT, so a send that fails for one mailbox is retried for that alone.
const (
	TopicOrderPlaced       = "order.placed"
	TopicPasswordReset     = "account.password_reset"
	TopicOrderPaid         = "order.paid"
	TopicOrderShipped      = "order.shipped"
	TopicRestocked         = "catalogue.restocked"
	TopicNewsletterConfirm = "newsletter.confirm"
	TopicNewsletterWelcome = "newsletter.welcome"
	// TopicNewsletterIssue is enqueued at [BulkPriority].
	TopicNewsletterIssue = "newsletter.issue"
	TopicEmailVerify     = "account.email_verify"
)

// BulkPriority is where a send that can wait goes in the queue. Transactional
// mail is 0; without the distinction one issue to ten thousand subscribers sits
// in front of every message written after it.
const BulkPriority = 100

// PollInterval is how often the worker looks for due messages.
const PollInterval = 5 * time.Second

// HandlerBudget is the longest one message's handler may take. It mirrors
// email.SendTimeout rather than importing it, and
// [TestTheHandlerBudgetIsTheSendersOwnTimeout] holds the two in step.
const HandlerBudget = 30 * time.Second

// LeaseMargin is what the lease keeps back for everything that is not a handler,
// including the two clocks: the lease is set by the database's now() and the
// work is paced by the worker's.
const LeaseMargin = time.Minute

// BatchSize bounds one pass. It follows from [Lease]: a claim is delivered
// SERIALLY, so BatchSize × HandlerBudget must fit inside the lease the claim
// took, or a second replica delivers the tail of the batch again.
const BatchSize = 8

// Lease is how long a claimed message stays invisible to other workers, chosen
// as a RECOVERY time — how long a message waits when the worker holding it dies.
const Lease = 5 * time.Minute

// Retain is how long a DELIVERED message is kept. Mailed tokens travel in the
// payload, so the row must not outlive its own secret; the cost is that
// re-enqueueing one (topic, dedupe_key) after this window would send twice.
const Retain = 30 * 24 * time.Hour

// SweepInterval is how often that happens.
const SweepInterval = 24 * time.Hour

// MaxAttempts is when a message stops being retried automatically. It stays
// pending rather than being deleted, so [Store.Stuck] can show it to a human.
const MaxAttempts = 8

// Handler does whatever a topic means. Returning an error reschedules the
// message; returning nil marks it delivered.
type Handler func(ctx context.Context, payload []byte) error

// Store drains the outbox.
type Store struct {
	pool     *pgxpool.Pool
	q        *db.Queries
	handlers map[string]Handler
	log      *slog.Logger
}

// NewStore returns a Store over pool.
func NewStore(pool *pgxpool.Pool, log *slog.Logger) *Store {
	if pool == nil || log == nil {
		panic("outbox: NewStore requires a pool and a logger")
	}
	return &Store{
		pool: pool, q: db.New(pool),
		handlers: map[string]Handler{},
		log:      log,
	}
}

// Handle registers what a topic does.
func (s *Store) Handle(topic string, h Handler) {
	if h == nil {
		panic("outbox: nil handler for " + topic)
	}
	s.handlers[topic] = h
}

// DrainAll delivers until the queue has nothing due left. A full pass of
// failures cannot spin the loop, because each one is rescheduled forward.
func (s *Store) DrainAll(ctx context.Context) (delivered, failed int, err error) {
	for {
		gotDelivered, gotFailed, drainErr := s.Drain(ctx)
		delivered += gotDelivered
		failed += gotFailed
		if drainErr != nil {
			return delivered, failed, drainErr
		}
		if gotDelivered+gotFailed < BatchSize {
			return delivered, failed, nil
		}
	}
}

// Drain delivers one batch and reports what happened. Each message is stamped on
// its own, so a handler that fails does not roll back the deliveries beside it.
func (s *Store) Drain(ctx context.Context) (delivered, failed int, err error) {
	rows, err := s.q.ClaimOutbox(ctx, db.ClaimOutboxParams{
		BatchSize: BatchSize,
		Lease:     pgtype.Interval{Microseconds: Lease.Microseconds(), Valid: true},
	})
	if err != nil {
		return 0, 0, fmt.Errorf("claim outbox: %w", err)
	}

	for i := range rows {
		m := &rows[i]
		if ctx.Err() != nil {
			return delivered, failed, ctx.Err()
		}
		if s.deliver(ctx, m) {
			delivered++
		} else {
			failed++
		}
	}
	return delivered, failed, nil
}

// deliver runs one message's handler and records the outcome.
func (s *Store) deliver(ctx context.Context, m *db.ClaimOutboxRow) bool {
	h, ok := s.handlers[m.Topic]
	if !ok {
		// Rescheduled rather than dropped: the next release may know what to do
		// with it.
		s.reschedule(ctx, m, errors.New("no handler registered for this topic"))
		return false
	}

	if err := h(ctx, m.Payload); err != nil {
		s.reschedule(ctx, m, err)
		return false
	}
	if err := s.q.MarkOutboxDelivered(ctx, m.ID); err != nil {
		// Delivered but not stamped: the retry sends it again, which is the
		// at-least-once guarantee.
		s.log.ErrorContext(ctx, "outbox delivered but not marked",
			"message", m.ID, "topic", m.Topic, "error", err)
		return false
	}
	return true
}

// reschedule pushes a failed message into the future.
func (s *Store) reschedule(ctx context.Context, m *db.ClaimOutboxRow, cause error) {
	next := time.Now().Add(backoff(m.Attempts))
	if m.Attempts >= MaxAttempts {
		// Far enough out that the automatic retry effectively stops, without
		// inventing a "failed" state the schema does not have.
		next = time.Now().Add(24 * time.Hour)
		s.log.ErrorContext(ctx, "outbox message is stuck",
			"message", m.ID, "topic", m.Topic, "attempts", m.Attempts, "error", cause)
	}
	if err := s.q.RescheduleOutbox(ctx, db.RescheduleOutboxParams{
		ID: m.ID, AvailableAt: next, LastError: truncate(cause.Error(), 500),
	}); err != nil {
		s.log.ErrorContext(ctx, "outbox reschedule", "message", m.ID, "error", err)
	}
}

// backoff is how long to wait before attempt n+1: exponential from four seconds,
// capped at an hour because uncapped doubling reaches days by attempt fifteen.
func backoff(attempts int32) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 10 {
		attempts = 10
	}
	d := time.Duration(math.Pow(2, float64(attempts))) * 2 * time.Second
	return min(d, time.Hour)
}

// Run drains on a ticker until ctx is cancelled. It blocks, so the caller owns
// the goroutine and can wait for it at shutdown.
func (s *Store) Run(ctx context.Context) {
	t := time.NewTicker(PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			delivered, failed, err := s.DrainAll(ctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				s.log.ErrorContext(ctx, "outbox drain", "error", err)
				continue
			}
			if delivered > 0 || failed > 0 {
				s.log.InfoContext(ctx, "outbox drained",
					"delivered", delivered, "failed", failed)
			}
		}
	}
}

// StuckMessage is one that has exhausted its attempts.
type StuckMessage struct {
	Topic     string
	DedupeKey string
	Attempts  int32
	LastError string
	Since     time.Time
}

// Stuck is what has failed too often, for a human. /admin/health shows the count
// and this is the list behind it.
func (s *Store) Stuck(ctx context.Context, limit int32) ([]StuckMessage, error) {
	rows, err := s.q.StuckOutbox(ctx, db.StuckOutboxParams{
		MinAttempts: MaxAttempts, Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("read stuck outbox: %w", err)
	}
	out := make([]StuckMessage, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, StuckMessage{
			Topic: r.Topic, DedupeKey: r.DedupeKey, Attempts: r.Attempts,
			LastError: r.LastError, Since: r.AvailableAt,
		})
	}
	return out, nil
}

// Decode unmarshals a payload into v.
func Decode(payload []byte, v any) error {
	if err := json.Unmarshal(payload, v); err != nil {
		return fmt.Errorf("decode outbox payload: %w", err)
	}
	return nil
}

// truncate bounds what goes in last_error.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Sweep deletes delivered messages past [Retain], once. It returns how many.
func (s *Store) Sweep(ctx context.Context) (int64, error) {
	n, err := s.q.SweepDeliveredMessages(ctx, pgtype.Interval{
		Microseconds: int64(Retain / time.Microsecond), Valid: true,
	})
	if err != nil {
		return 0, fmt.Errorf("sweep delivered messages: %w", err)
	}
	return n, nil
}

// SweepForever runs Sweep on a ticker until ctx is cancelled. A sweep that errors
// is logged and retried on the next tick rather than ending the worker.
func (s *Store) SweepForever(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			switch n, err := s.Sweep(ctx); {
			case err != nil && ctx.Err() == nil:
				log.ErrorContext(ctx, "sweep outbox", "error", err)
			case err == nil && n > 0:
				log.InfoContext(ctx, "outbox swept", "deleted", n,
					"retain_days", int(Retain.Hours()/24))
			}
		}
	}
}
