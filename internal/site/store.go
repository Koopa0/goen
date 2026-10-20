package site

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
)

type Store struct {
	q *db.Queries
}

func NewStore(dbtx db.DBTX) *Store {
	if dbtx == nil {
		panic("site: NewStore requires a database handle")
	}
	return &Store{q: db.New(dbtx)}
}

func (s *Store) FAQEntries(ctx context.Context) ([]db.FAQEntriesRow, error) {
	rows, err := s.q.FAQEntries(ctx, string(i18n.FromContext(ctx)))
	if err != nil {
		return nil, fmt.Errorf("read faq: %w", err)
	}
	return rows, nil
}
