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
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
)

// Topic names one kind of message and the payload type every message on it
// carries. Only this package makes one, so a producer cannot enqueue a type its
// consumer does not decode.
type Topic[T any] struct{ name string }

func topic[T any](name string) Topic[T] { return Topic[T]{name: name} }

func (t Topic[T]) Name() string { return t.name }

// Topics goen publishes. A topic that fans out carries one message per
// RECIPIENT, so a send that fails for one mailbox is retried for that alone.
var (
	TopicOrderPlaced       = topic[email.OrderPlaced]("order.placed")
	TopicPasswordReset     = topic[email.PasswordReset]("account.password_reset")
	TopicOrderPaid         = topic[email.OrderPaid]("order.paid")
	TopicOrderShipped      = topic[email.OrderShipped]("order.shipped")
	TopicOrderTerminal     = topic[email.OrderTerminal]("order.terminal")
	TopicRestocked         = topic[email.RestockNotice]("catalogue.restocked")
	TopicNewsletterConfirm = topic[email.NewsletterConfirm]("newsletter.confirm")
	TopicNewsletterWelcome = topic[email.NewsletterWelcome]("newsletter.welcome")
	// TopicNewsletterIssue is enqueued at [BulkPriority].
	TopicNewsletterIssue = topic[email.NewsletterIssue]("newsletter.issue")
	TopicEmailVerify     = topic[email.AddressVerify]("account.email_verify")
	TopicStaffInvitation = topic[email.StaffInvitation]("staff.invitation")
	TopicStaffEnrolment  = topic[email.StaffEnrolment]("staff.enrolment")
	// TopicPasswordResetRequest is a forgotten-password request, queued the
	// same way whether or not the address has an account. Its handler issues
	// the token and queues the TopicPasswordReset message.
	TopicPasswordResetRequest = topic[PasswordResetRequest]("account.password_reset_request")
	// TopicRegistration is a registration, queued the same way whether or not
	// the address already had an account. Its handler sends the link that
	// completes a new account, or tells an existing one's owner of the attempt.
	TopicRegistration = topic[AccountRegistration]("account.registration")
	// TopicInvoiceDue is a sale that became final. Its handler claims the
	// 統一發票 on the admin pool; the invoice reconciler issues it.
	TopicInvoiceDue = topic[InvoiceDue]("invoice.due")
	// TopicInvoiceVoidDue is an order cancelled after its 統一發票 was owed.
	// Its handler claims the void on the admin pool; the invoice reconciler
	// sends it.
	TopicInvoiceVoidDue = topic[InvoiceVoidDue]("invoice.void_due")
)

// BulkPriority is where a send that can wait goes in the queue. Transactional
// mail is 0; without the distinction one issue to ten thousand subscribers sits
// in front of every message written after it.
const BulkPriority = 100

const PollInterval = 5 * time.Second

// HandlerBudget is the longest one message's handler may take. It mirrors
// email.SendTimeout rather than importing it.
const HandlerBudget = 30 * time.Second

// SettleBudget bounds the single-row write that records a message's outcome.
// Without it the write inherits the pool's statement_timeout, which is chosen
// for a storefront request and is several times what a primary-key UPDATE needs
// — enough per message that a full batch outlives its own lease.
const SettleBudget = 5 * time.Second

// LeaseMargin is what the lease keeps back for the claim itself and for the two
// clocks: the lease is set by the database's now() and the work is paced by the
// worker's.
const LeaseMargin = time.Minute

// BatchSize bounds one pass, and it is DERIVED rather than chosen. A claim is
// delivered serially, so BatchSize × (HandlerBudget + SettleBudget) plus
// LeaseMargin must fit inside Lease, which is fixed by recovery time. At 30s,
// 5s, 1m and 5m that is the largest batch that fits.
// [TestTheLeaseCoversTheWholeBatch] is the arithmetic, and it exists because
// each term has an owner elsewhere: HandlerBudget mirrors the mail sender's
// timeout and SettleBudget answers the pool's.
const BatchSize = 6

// Lease is how long a claimed message stays invisible to other workers, chosen
// as a RECOVERY time — how long a message waits when the worker holding it dies.
const Lease = 5 * time.Minute

// Retain is how long a message is kept: after delivery for a delivered one, and
// after creation for one that never was. Mailed tokens travel in the payload, so
// the row must not outlive its own secret; the cost is that re-enqueueing one
// (topic, dedupe_key) after this window would send twice.
const Retain = 30 * 24 * time.Hour

const SweepInterval = 24 * time.Hour

// StuckAfterAttempts is the attempt count from which a message is stuck: it is
// retried daily instead of quickly, so mail queued during a long provider
// outage still goes out, and [Store.Stuck] lists it for a human until [Retain].
const StuckAfterAttempts = 8

// Handler does whatever a topic means. Returning an error reschedules the
// message; returning nil marks it delivered. Its context expires after
// [HandlerBudget]: a handler that ignores cancellation is bound by nothing and
// holds its claim past the lease, so its batch is delivered twice.
type Handler func(ctx context.Context, payload []byte) error

type Store struct {
	pool     *pgxpool.Pool
	q        *db.Queries
	handlers map[string]Handler
	log      *slog.Logger
}

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

// Handle registers what a topic does. Registration is startup-only and must
// finish before Run, Drain or DrainAll starts. An empty or duplicate topic is a
// wiring error and panics rather than silently replacing a handler.
func (s *Store) Handle(topic string, h Handler) {
	if topic == "" {
		panic("outbox: a handler needs a topic")
	}
	if h == nil {
		panic("outbox: nil handler for " + topic)
	}
	if _, exists := s.handlers[topic]; exists {
		panic("outbox: duplicate handler for " + topic)
	}
	s.handlers[topic] = h
}

// HandleJSON registers a typed JSON handler. Each delivery decodes a fresh T.
// Unknown object fields remain accepted so an older consumer can read a payload
// written by a newer producer.
func (s *Store) HandleJSON[T any](t Topic[T], h func(context.Context, *T) error) {
	if h == nil {
		panic("outbox: nil JSON handler for " + t.name)
	}
	s.Handle(t.name, func(ctx context.Context, payload []byte) error {
		var message T
		if err := json.Unmarshal(payload, &message); err != nil {
			return fmt.Errorf("decode outbox payload for %s: %w", t.name, err)
		}
		return h(ctx, &message)
	})
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
	owner := uuid.New()
	rows, err := s.q.ClaimOutbox(ctx, db.ClaimOutboxParams{
		BatchSize:  BatchSize,
		Lease:      pgtype.Interval{Microseconds: Lease.Microseconds(), Valid: true},
		LeaseOwner: owner,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("claim outbox: %w", err)
	}

	for i := range rows {
		m := &rows[i]
		if ctx.Err() != nil {
			return delivered, failed, ctx.Err()
		}
		if s.deliver(ctx, owner, m) {
			delivered++
		} else {
			failed++
		}
	}
	return delivered, failed, nil
}

func (s *Store) deliver(ctx context.Context, owner uuid.UUID, m *db.ClaimOutboxRow) bool {
	h, ok := s.handlers[m.Topic]
	if !ok {
		// Rescheduled rather than dropped: the next release may know what to do
		// with it.
		s.reschedule(ctx, owner, m, errors.New("no handler registered for this topic"))
		return false
	}

	if err := runHandler(ctx, h, m.Payload); err != nil {
		s.reschedule(ctx, owner, m, err)
		return false
	}
	// Detached: a shutdown that lands after the send must still stamp it, or the
	// next process sends the letter again once the lease expires.
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), SettleBudget)
	defer cancel()
	marked, err := s.q.MarkOutboxDelivered(settle, db.MarkOutboxDeliveredParams{ID: m.ID, LeaseOwner: owner})
	if err != nil {
		// Delivered but not stamped: the retry sends it again, which is the
		// at-least-once guarantee.
		s.log.ErrorContext(ctx, "outbox delivered but not marked",
			"message", m.ID, "topic", m.Topic, "error", err)
		return false
	}
	if marked == 0 {
		s.logLostClaim(ctx, m)
		return false
	}
	return true
}

// logLostClaim logs a settle that matched nothing: the lease ran out, a later
// claim took the message, and recording the outcome is that claim's to do.
func (s *Store) logLostClaim(ctx context.Context, m *db.ClaimOutboxRow) {
	s.log.WarnContext(ctx, "outbox claim lost before it was settled",
		"message", m.ID, "topic", m.Topic, "attempts", m.Attempts)
}

// runHandler spends at most [HandlerBudget] of the claim's lease on the handler,
// which is what [BatchSize] assumes. The budget is the handler's alone: the stamp
// and the reschedule run on their own [SettleBudget], detached from ctx's
// cancellation, so a handler that runs out of time or a shutdown that arrives
// mid-send is recorded rather than left claimed until the lease expires.
func runHandler(ctx context.Context, h Handler, payload []byte) error {
	ctx, cancel := context.WithTimeout(ctx, HandlerBudget)
	defer cancel()
	return h(ctx, payload)
}

func (s *Store) reschedule(ctx context.Context, owner uuid.UUID, m *db.ClaimOutboxRow, cause error) {
	delay := backoff(m.Attempts)
	if m.Attempts >= StuckAfterAttempts {
		// A slow retry rather than none: valid mail queued during a long provider
		// outage must still go out. [Retain] bounds how long it can keep trying.
		delay = 24 * time.Hour
		s.log.ErrorContext(ctx, "outbox message is stuck",
			"message", m.ID, "topic", m.Topic, "attempts", m.Attempts, "error", cause)
	}
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), SettleBudget)
	defer cancel()
	rescheduled, err := s.q.RescheduleOutbox(settle, db.RescheduleOutboxParams{
		ID: m.ID, LeaseOwner: owner,
		Backoff: pgtype.Interval{
			Microseconds: delay.Microseconds(), Valid: true,
		},
		LastError: truncate(cause.Error(), 500),
	})
	switch {
	case err != nil:
		s.log.ErrorContext(ctx, "outbox reschedule", "message", m.ID, "error", err)
	case rescheduled == 0:
		s.logLostClaim(ctx, m)
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

type StuckMessage struct {
	Topic         string
	DedupeKey     string
	Attempts      int32
	LastError     string
	NextAttemptAt time.Time
}

func (s *Store) Stuck(ctx context.Context, limit int32) ([]StuckMessage, error) {
	rows, err := s.q.StuckOutbox(ctx, db.StuckOutboxParams{
		MinAttempts: StuckAfterAttempts, Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("read stuck outbox: %w", err)
	}
	out := make([]StuckMessage, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, StuckMessage{
			Topic: r.Topic, DedupeKey: r.DedupeKey, Attempts: r.Attempts,
			LastError: r.LastError, NextAttemptAt: r.AvailableAt,
		})
	}
	return out, nil
}

// truncate bounds what goes in last_error, cutting on a rune boundary: a byte
// cut through a multibyte character is invalid UTF-8, which PostgreSQL refuses,
// losing the write that records the failure. The bound stays in bytes, which is
// what the column's CHECK measures.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Sweep deletes messages past [Retain], once: delivered ones by delivery time
// and undelivered ones by creation time.
func (s *Store) Sweep(ctx context.Context) (int64, error) {
	retain := pgtype.Interval{Microseconds: int64(Retain / time.Microsecond), Valid: true}
	delivered, err := s.q.SweepDeliveredMessages(ctx, retain)
	if err != nil {
		return 0, fmt.Errorf("sweep delivered messages: %w", err)
	}
	undelivered, err := s.q.SweepUndeliveredMessages(ctx, retain)
	if err != nil {
		return delivered, fmt.Errorf("sweep undelivered messages: %w", err)
	}
	for _, m := range undelivered {
		s.log.WarnContext(ctx, "outbox message expired",
			"message", m.ID, "topic", m.Topic, "attempts", m.Attempts,
			"created_at", m.CreatedAt)
	}
	if delivered > 0 || len(undelivered) > 0 {
		s.log.InfoContext(ctx, "outbox swept", "delivered", delivered,
			"undelivered", len(undelivered), "retain_days", int(Retain.Hours()/24))
	}
	return delivered + int64(len(undelivered)), nil
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
			if _, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
				log.ErrorContext(ctx, "sweep outbox", "error", err)
			}
		}
	}
}

// Enqueue writes one message in the caller's transaction, so it exists exactly
// when the fact it is about does. A key already queued for the topic is left as
// it was.
func Enqueue[T any](ctx context.Context, q *db.Queries, t Topic[T], dedupeKey string, payload *T) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode %s message: %w", t.name, err)
	}
	if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{
		Topic: t.name, DedupeKey: dedupeKey, Payload: encoded,
	}); err != nil {
		return fmt.Errorf("enqueue %s message: %w", t.name, err)
	}
	return nil
}

// EnqueueAll writes every message of one topic in a single statement, in the
// caller's transaction. keys and payloads pair by position; a key already
// queued for the topic is left as it was.
func EnqueueAll[T any](ctx context.Context, q *db.Queries, t Topic[T], priority int16, keys []string, payloads []T) error {
	if len(keys) != len(payloads) {
		return errors.New("outbox: EnqueueAll needs one payload per key")
	}
	if len(keys) == 0 {
		return nil
	}
	text := make([]string, len(payloads))
	for i := range payloads {
		encoded, err := json.Marshal(&payloads[i])
		if err != nil {
			return fmt.Errorf("encode %s message: %w", t.name, err)
		}
		text[i] = string(encoded)
	}
	if err := q.EnqueueMessages(ctx, db.EnqueueMessagesParams{
		Topic: t.name, Priority: priority, DedupeKeys: keys, Payloads: text,
	}); err != nil {
		return fmt.Errorf("enqueue %s messages: %w", t.name, err)
	}
	return nil
}
