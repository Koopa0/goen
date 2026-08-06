// Package outbox delivers messages that were written in the same transaction as
// the fact they are about.
//
// # Why an outbox at all
//
// Sending an email from a handler has two orderings and both are wrong. Send
// before the commit and a rolled-back checkout has told somebody their order is
// confirmed. Send after it and a process that dies in between has an order
// nobody was told about — the failure nobody notices, because the order looks
// fine.
//
// Writing the intent to the same transaction removes the choice: the order and
// the message commit together or neither does. Delivery becomes a separate,
// retryable problem, which is the one it actually is.
//
// # What this guarantees
//
// AT LEAST once, never exactly once. A worker can die after the provider
// accepted a message and before the row is stamped, and the retry sends a
// second copy. Handlers must therefore tolerate being run twice — which for
// email means the customer sees a duplicate, and is why the alternative
// (at most once, by stamping first) is worse: a silently missing confirmation
// costs more than a repeated one.
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

// Topics goen publishes. String constants rather than an enum type, because
// they are also the value in a database column and a reader comparing the two
// should see the same characters.
const (
	TopicOrderPlaced = "order.placed"
	// TopicPasswordReset carries a reset link.
	//
	// Through the outbox like every other message, and that matters more here
	// than elsewhere: a reset the customer never receives is a customer who
	// stays locked out, and sending from the handler loses it whenever the
	// process dies between the write and the send.
	TopicPasswordReset = "account.password_reset"
	TopicOrderPaid     = "order.paid"
	TopicOrderShipped  = "order.shipped"
	// TopicRestocked tells somebody who asked that a variant is back.
	//
	// One message per waiting address rather than one per variant, so a send
	// that fails for one mailbox is retried for that mailbox alone — a single
	// message carrying two hundred addresses would replay all two hundred.
	TopicRestocked = "catalogue.restocked"
	// TopicNewsletterConfirm carries the link that asks a mailbox whether it
	// actually wants the newsletter. It is the whole of double opt-in: until
	// this message is answered, the address is a request and not a subscriber.
	TopicNewsletterConfirm = "newsletter.confirm"
	// TopicNewsletterWelcome carries the unsubscribe link.
	//
	// Which is why it exists at all. A subscription somebody cannot leave is
	// not one they consented to, and until there is a newsletter to put the
	// link at the foot of, this is the only email that can carry it.
	TopicNewsletterWelcome = "newsletter.welcome"
	// TopicNewsletterIssue is one copy of one newsletter, to one address.
	//
	// One message per recipient rather than one per issue, the same shape as a
	// restock notice and for the same reason: a send that fails for one mailbox
	// is retried for that mailbox alone, where a single message carrying ten
	// thousand addresses would replay all ten thousand.
	//
	// Enqueued at [BulkPriority], so it waits behind every transactional message.
	TopicNewsletterIssue = "newsletter.issue"
	// TopicEmailVerify carries the link that proves an address belongs to the
	// person using it.
	//
	// One topic for two acts, because they are the same act: proving the address
	// somebody registered with, and proving a new one they want to move to.
	TopicEmailVerify = "account.email_verify"
)

// BulkPriority is where a send that can wait goes in the queue.
//
// Transactional mail is priority 0 — a receipt, a dispatch notice, a password
// reset — and a newsletter is 100. Without the distinction, one issue to ten
// thousand subscribers sits in front of every message written after it, and the
// customer waiting for a link back into their own account waits behind an email
// somebody else asked for.
const BulkPriority = 100

// PollInterval is how often the worker looks for due messages.
//
// Short enough that a confirmation email feels immediate, long enough that an
// idle shop is not running a query every second. The partial index makes the
// empty case cheap.
const PollInterval = 5 * time.Second

// HandlerBudget is the longest one message's handler may take.
//
// email.SendTimeout, mirrored rather than imported: nothing else in this package
// knows what a message DOES, and one number is a poor reason to make the queue
// depend on the mail. [TestTheLeaseCoversTheWholeBatch] holds the two equal, so
// a change to the sender's timeout that this arithmetic has not accounted for is
// a red test rather than a silent overrun.
const HandlerBudget = 30 * time.Second

// LeaseMargin is what the lease keeps back for everything that is not a handler.
//
// The claim's own round trip, the reschedule and stamp after each message, a
// machine under load, and the two clocks either end of the comparison: the lease
// is set by the DATABASE's now() and the work is paced by the worker's.
const LeaseMargin = time.Minute

// BatchSize bounds one pass.
//
// Eight, and the number comes from [Lease]: a claim is delivered SERIALLY, so a
// batch can take BatchSize × HandlerBudget before the last message is done, and
// that has to fit inside the lease the claim took. It used to be fifty, which is
// twenty-five minutes of work under a five-minute lease — so from the eleventh
// message onwards a second replica could see the rest as due and deliver them
// while this worker was still sending them. At-least-once turning into
// reliably-twice: two receipts for one order, and for a password reset a second
// live token sitting in the mailbox.
//
// It costs no throughput, because [Store.DrainAll] claims again as soon as a
// pass comes back full. What it bounds is one CLAIM, which is the only thing the
// lease has to cover.
const BatchSize = 8

// Lease is how long a claimed message stays invisible to other workers.
//
// Five minutes, and it is chosen as a RECOVERY time rather than as a work
// budget: it is how long a message waits when the worker holding it dies. Too
// short and a slow delivery is claimed a second time while the first is still
// running; too long and a customer waits that long for a reset link after a
// deploy restarts the process mid-drain.
//
// What the batch may take is fitted to it rather than the other way round — see
// [BatchSize].
const Lease = 5 * time.Minute

// Retain is how long a DELIVERED message is kept.
//
// Until now: forever. Every message goen ever sent stayed in outbox_messages,
// which is unbounded growth on the busiest write path in the schema — and it is
// also where the plaintext tokens live. A reset link, an unsubscribe link and a
// newsletter confirmation all travel in the payload, because the alternative
// (sending from the handler) loses the message when the process dies mid-send.
// That trade is only sound if the row does not outlive its own secret by years.
//
// Thirty days, which is long enough to answer "did we email them, and what did it
// say?" about anything a customer is still asking about, and short enough that
// nothing sits there for a year. The one thing it costs is the DEDUPE window: a
// producer that legitimately re-enqueued the same (topic, dedupe_key) more than
// thirty days later would send twice. None does — every key here is an order
// number, a tracking number, a spent notification row, or a token digest, and
// none of those recurs.
const Retain = 30 * 24 * time.Hour

// SweepInterval is how often that happens. Daily: retention is measured in
// weeks, so nothing is gained by looking more often, and a delete that runs once
// a day is never large.
const SweepInterval = 24 * time.Hour

// MaxAttempts is when a message stops being retried automatically.
//
// It is not deleted or marked failed: it stays pending with its attempt count,
// so [Store.Stuck] can show it to a human. A queue that silently discards what
// it could not deliver is a queue that lies about being empty.
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

// DrainAll delivers until the queue has nothing due left.
//
// One pass claims at most [BatchSize], which is small because the lease has to
// cover it. Claiming again while a pass comes back FULL is what keeps that from
// being a throughput ceiling: a newsletter to ten thousand subscribers still
// drains at the speed the mail server accepts it, in claims each of which fits
// comfortably inside its own lease.
//
// A short pass ends it. So does a pass in which every message failed — those are
// rescheduled into the future, so the next claim does not see them and the loop
// cannot spin on a provider that is down.
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

// Drain delivers one batch and reports what happened.
//
// Each message is handled in its own claim rather than one transaction over the
// batch: a message whose handler fails must not roll back the delivery of the
// seven beside it.
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
		// An unregistered topic is a deployment that publishes something it
		// cannot yet consume. Rescheduled rather than dropped: the next release
		// may know what to do with it.
		s.reschedule(ctx, m, errors.New("no handler registered for this topic"))
		return false
	}

	if err := h(ctx, m.Payload); err != nil {
		s.reschedule(ctx, m, err)
		return false
	}
	if err := s.q.MarkOutboxDelivered(ctx, m.ID); err != nil {
		// Delivered but not stamped. The retry will send it again — at least
		// once, which is the guarantee this package makes.
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
		// deleting the row or inventing a "failed" state the schema does not
		// have. Stuck() is what surfaces it.
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

// backoff is how long to wait before attempt n+1.
//
// Exponential from four seconds, capped at an hour. Capped because an
// uncapped doubling reaches days by attempt fifteen, and a provider that was
// down for ten minutes should not delay a confirmation email until Thursday.
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

// Run drains on a ticker until ctx is cancelled.
//
// The caller owns the goroutine — this blocks, so main can wg.Go it and wait at
// shutdown. A worker started and forgotten is the fire-and-forget the
// concurrency rules refuse.
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
//
// A type of this package's own rather than the generated row: a store that
// hands back db.* makes every caller depend on sqlc's output, and this one is
// read by the back office.
type StuckMessage struct {
	Topic     string
	DedupeKey string
	Attempts  int32
	LastError string
	Since     time.Time
}

// Stuck is what has failed too often, for a human.
//
// The COUNT is on /admin/health and this is the list behind it. A page that
// says "3 stuck" and cannot say which three tells an operator that something is
// wrong and nothing about what to do — which is half a health check.
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

// Decode unmarshals a payload into v. A payload that will not decode is a
// permanent failure, not a transient one — but it is still rescheduled rather
// than dropped, because the message may have been written by a version whose
// shape a later release understands.
func Decode(payload []byte, v any) error {
	if err := json.Unmarshal(payload, v); err != nil {
		return fmt.Errorf("decode outbox payload: %w", err)
	}
	return nil
}

// truncate bounds what goes in last_error. A provider that returns a page of
// HTML should not put a page of HTML in every row.
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

// SweepForever runs Sweep on a ticker until ctx is cancelled.
//
// It logs the count rather than staying silent, because "the outbox is not
// growing" is a thing an operator should be able to see happening. A sweep that
// errors is logged and retried on the next tick: nothing downstream depends on
// it, and a worker that exited would take the guarantee with it.
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
