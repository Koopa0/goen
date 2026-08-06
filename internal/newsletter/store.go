package newsletter

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

// Store records subscriptions in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// NewStore returns a Store reading and writing through pool.
//
// It holds the pool rather than a db.DBTX because two of its three writes are
// transactions: the row and the outbox message that carries its link commit
// together or not at all.
func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("newsletter: NewStore requires a database handle")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// Request asks addr to confirm that it wants the newsletter.
//
// The caller must answer the visitor IDENTICALLY whatever the outcome — see
// [Outcome]. The returned token is for the link in the email and nothing else.
//
// The confirmation row and the message that carries its link commit together.
// Enqueuing afterwards loses the link whenever the process dies in between, and
// enqueuing before would mail a link for a row that rolled back.
func (s *Store) Request(ctx context.Context, addr string) (Outcome, error) {
	addr = email.Clean(addr)
	if Validate(addr) != "" {
		return AlreadyActive, fmt.Errorf("requesting confirmation for %q: not a usable address", addr)
	}

	token, err := NewToken()
	if err != nil {
		return AlreadyActive, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AlreadyActive, fmt.Errorf("beginning newsletter request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := db.New(tx)

	_, err = q.RequestNewsletterConfirm(ctx, db.RequestNewsletterConfirmParams{
		Email:  addr,
		Digest: HashToken(token),
		Ttl:    interval(ConfirmTTL),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Already on the list. Nothing was written and nothing is sent.
			return AlreadyActive, nil
		}
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23514" {
			return AlreadyActive, fmt.Errorf("requesting confirmation: rejected by %s: %w",
				pgErr.ConstraintName, err)
		}
		return AlreadyActive, fmt.Errorf("requesting confirmation: %w", err)
	}

	if err := enqueue(ctx, q, outbox.TopicNewsletterConfirm, "newsletter-confirm:"+dedupeOf(token),
		email.NewsletterConfirm{Email: addr, Token: token, Locale: i18n.FromContext(ctx).Tag()}); err != nil {
		return AlreadyActive, err
	}

	if err := tx.Commit(ctx); err != nil {
		return AlreadyActive, fmt.Errorf("committing newsletter request: %w", err)
	}
	return Requested, nil
}

// Confirm spends a confirmation link and puts its address on the list.
//
// It returns the address so the caller can name whose subscription it is. The
// welcome message it enqueues carries the unsubscribe link, which is the only
// place that link can appear until there is a newsletter to put it at the foot
// of — a subscription somebody cannot leave is not one they consented to.
func (s *Store) Confirm(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", ErrNotFound
	}

	leave, err := NewToken()
	if err != nil {
		return "", err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("beginning newsletter confirm: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := db.New(tx)

	addr, err := q.SpendNewsletterConfirmation(ctx, HashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("spending confirmation: %w", err)
	}

	// RETURNING the live token, which is not necessarily the one just generated:
	// an address that rejoins keeps the secret it already had, so every link ever
	// mailed to it goes on working.
	leave, err = q.AddNewsletterSubscriber(ctx, db.AddNewsletterSubscriberParams{
		Email: addr, UnsubscribeToken: leave, Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return "", fmt.Errorf("adding subscriber: %w", err)
	}

	if err := enqueue(ctx, q, outbox.TopicNewsletterWelcome, "newsletter-welcome:"+dedupeOf(leave),
		email.NewsletterWelcome{Email: addr, UnsubscribeToken: leave, Locale: i18n.FromContext(ctx).Tag()}); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("committing newsletter confirm: %w", err)
	}
	return addr, nil
}

// Unsubscribe takes the address behind token off the list.
//
// Idempotent: a second click, or a link followed a year later, answers the same
// address and the same success. A token matching nothing is [ErrNotFound] — the
// one case the reader has to be told about, because their address is still on
// the list and they will be expecting it not to be.
//
// Not a transaction, because there is nothing to tell anybody: sending "you have
// been unsubscribed" to somebody who just asked not to be emailed is the one
// message a mailing list must never send.
func (s *Store) Unsubscribe(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", ErrNotFound
	}

	addr, err := s.q.UnsubscribeNewsletter(ctx, token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("unsubscribing: %w", err)
	}
	return addr, nil
}

// enqueue writes one outbox message inside the caller's transaction.
func enqueue(ctx context.Context, q *db.Queries, topic, dedupeKey string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encoding %s message: %w", topic, err)
	}
	if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{
		Topic:     topic,
		DedupeKey: dedupeKey,
		Payload:   encoded,
	}); err != nil {
		return fmt.Errorf("enqueueing %s message: %w", topic, err)
	}
	return nil
}

// dedupeOf keys a message on its token, so a retried enqueue is one message and
// each fresh request gets its own mail. Hashed, so the outbox row's key is not
// itself the link.
func dedupeOf(token string) string {
	return hex.EncodeToString(HashToken(token))
}

// interval converts a Go duration to the type the query's ::interval expects.
func interval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: int64(d / time.Microsecond), Valid: true}
}

// enqueueBulk is enqueue at [outbox.BulkPriority].
//
// A separate function rather than a priority parameter on enqueue, because every
// other caller in goen is transactional and a parameter is a number one of them
// would eventually pass wrong. Bulk is the exception and it says so by name.
func enqueueBulk(ctx context.Context, q *db.Queries, topic, dedupeKey string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encoding %s message: %w", topic, err)
	}
	if err := q.EnqueueBulkMessage(ctx, db.EnqueueBulkMessageParams{
		Topic:     topic,
		DedupeKey: dedupeKey,
		Payload:   encoded,
		Priority:  outbox.BulkPriority,
	}); err != nil {
		return fmt.Errorf("enqueueing %s message: %w", topic, err)
	}
	return nil
}
