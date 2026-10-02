package feedback

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

var (
	ErrNotFound = errors.New("feedback: not found")
	ErrInvalid  = errors.New("feedback: invalid input")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("feedback: refused")
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("feedback: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// position is a reader's place in the review list. The query builds it as
// PageCursor, so its fields are the ordering values and nothing else.
type position struct {
	ID uuid.UUID
	At time.Time
}

// messagePosition adds the rank the inbox orders by: handled messages sort last.
type messagePosition struct {
	Rank bool
	ID   uuid.UUID
	At   time.Time
}
