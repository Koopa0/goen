package newsletter

import (
	"context"
	"encoding/hex"
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
	"github.com/koopa0/goen/internal/pgtx"
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("newsletter: NewStore requires a database handle")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// Request means the caller must answer the visitor IDENTICALLY whatever the outcome,
// see [Outcome].
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
	defer pgtx.Rollback(ctx, tx)
	q := db.New(tx)

	_, err = q.RequestNewsletterConfirm(ctx, db.RequestNewsletterConfirmParams{
		Email:  addr,
		Digest: HashToken(token),
		Ttl:    interval(ConfirmTTL),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AlreadyActive, nil
		}
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23514" {
			return AlreadyActive, fmt.Errorf("requesting confirmation: rejected by %s: %w",
				pgErr.ConstraintName, err)
		}
		return AlreadyActive, fmt.Errorf("requesting confirmation: %w", err)
	}

	if err := outbox.Enqueue(ctx, q, outbox.TopicNewsletterConfirm, "newsletter-confirm:"+dedupeOf(token),
		&email.NewsletterConfirm{Email: addr, Token: token, Locale: i18n.FromContext(ctx).Tag()}); err != nil {
		return AlreadyActive, err
	}

	if err := tx.Commit(ctx); err != nil {
		return AlreadyActive, fmt.Errorf("committing newsletter request: %w", err)
	}
	return Requested, nil
}

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
	defer pgtx.Rollback(ctx, tx)
	q := db.New(tx)

	addr, err := q.SpendNewsletterConfirmation(ctx, HashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("spending confirmation: %w", err)
	}

	// RETURNING the live token, which is not necessarily the one just
	// generated: an address that rejoins keeps the secret it already had, so
	// every link ever mailed to it goes on working.
	leave, err = q.AddNewsletterSubscriber(ctx, db.AddNewsletterSubscriberParams{
		Email: addr, UnsubscribeToken: leave, Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return "", fmt.Errorf("adding subscriber: %w", err)
	}

	if err := outbox.Enqueue(ctx, q, outbox.TopicNewsletterWelcome, "newsletter-welcome:"+dedupeOf(leave),
		&email.NewsletterWelcome{Email: addr, UnsubscribeToken: leave, Locale: i18n.FromContext(ctx).Tag()}); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("committing newsletter confirm: %w", err)
	}
	return addr, nil
}

// Unsubscribe is idempotent and enqueues nothing: mailing "you have been
// unsubscribed" to somebody who just asked not to be emailed is the one message
// a mailing list must never send.
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

// dedupeOf hashes the token so the outbox row's key is not itself the link.
func dedupeOf(token string) string {
	return hex.EncodeToString(HashToken(token))
}

func interval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: int64(d / time.Microsecond), Valid: true}
}

// StillSubscribed is asked at delivery, because the enqueue froze the recipient
// and the queue can take hours to reach it.
func (s *Store) StillSubscribed(ctx context.Context, address string) (bool, error) {
	yes, err := s.q.StillSubscribed(ctx, address)
	if err != nil {
		return false, fmt.Errorf("check consent for a newsletter recipient: %w", err)
	}
	return yes, nil
}
