// Package audit is the back office's trail: the actions it records, the write
// that records one inside the work it describes, and the /admin/audit page.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/web"
)

var ErrNoActor = errors.New("admin: a back-office write reached the store with no actor")

type Event struct {
	Action Action
	// Table is what it was done to. Not a foreign key: audit rows outlive the
	// rows they describe.
	Table  string
	ID     uuid.NullUUID
	Before any
	After  any
}

// EntityID is Event.ID for a row's identifier; the zero UUID is no entity.
func EntityID(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil}
}

// Run runs a back-office write and records who did it, in ONE transaction:
// an audit row for work that rolled back is a lie, and work that commits
// without one is a gap.
func Run(ctx context.Context, pool *pgxpool.Pool, e Event, work func(context.Context, *db.Queries) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s: %w", e.Action, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := db.New(tx)

	if err := work(ctx, q); err != nil {
		return err
	}
	if err := In(ctx, q, e); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", e.Action, err)
	}
	return nil
}

// In records an action inside a transaction the caller already opened, which
// is what a write of several statements needs instead of [Run].
func In(ctx context.Context, q *db.Queries, e Event) error {
	actor, ok := Actor(ctx)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNoActor, e.Action)
	}
	before, err := encodeState(e.Before)
	if err != nil {
		return fmt.Errorf("encode before state for %s: %w", e.Action, err)
	}
	after, err := encodeState(e.After)
	if err != nil {
		return fmt.Errorf("encode after state for %s: %w", e.Action, err)
	}
	requestID := web.RequestID(ctx)
	if _, err := q.RecordAuditEvent(ctx, db.RecordAuditEventParams{
		Actor:       actor,
		Action:      string(e.Action),
		EntityTable: e.Table,
		EntityID:    e.ID,
		Before:      before,
		After:       after,
		RequestID:   pgtype.Text{String: requestID, Valid: requestID != ""},
	}); err != nil {
		return fmt.Errorf("record audit event for %s: %w", e.Action, err)
	}
	return nil
}

// ActorID is Actor for a column that may be NULL. An unparseable id is nobody
// and not a failure, so a dispatch that has physically happened is not refused
// over who recorded it.
func ActorID(ctx context.Context) uuid.NullUUID {
	id, ok := Actor(ctx)
	return uuid.NullUUID{UUID: id, Valid: ok}
}

func Actor(ctx context.Context) (uuid.UUID, bool) {
	u, ok := account.FromContext(ctx)
	if !ok {
		return uuid.UUID{}, false
	}
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return uuid.UUID{}, false
	}
	return id, true
}

// encodeState turns a before/after value into jsonb, or NULL. Invalid state is
// an audit failure: silently storing NULL would let the write commit with a
// materially incomplete trail.
func encodeState(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}
