package questions

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

var (
	ErrNotFound = errors.New("questions: not found")
	ErrInvalid  = errors.New("questions: invalid input")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("questions: refused")
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("questions: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}
