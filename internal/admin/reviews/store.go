package reviews

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

var (
	ErrNotFound = errors.New("reviews: not found")
	ErrInvalid  = errors.New("reviews: invalid input")
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("reviews: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// position is a reader's place in the review list. The query builds it as
// PageCursor, so its fields are the ordering values and nothing else.
type position struct {
	ID uuid.UUID
	At time.Time
}
