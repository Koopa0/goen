package messages

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

var ErrNotFound = errors.New("messages: not found")

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("messages: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// messagePosition is a reader's place in the message list. The query builds it
// as PageCursor; Rank is the order's first key: handled messages sort last.
type messagePosition struct {
	Rank bool
	ID   uuid.UUID
	At   time.Time
}
