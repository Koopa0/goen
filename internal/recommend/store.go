package recommend

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

type Store struct {
	q   *db.Queries
	log *slog.Logger
}

// NewStore needs the owner's pool: `store` and `admin` hold no INSERT on what
// refresh_copurchases writes.
func NewStore(pool *pgxpool.Pool, log *slog.Logger) *Store {
	if pool == nil || log == nil {
		panic("recommend: NewStore requires a pool and a logger")
	}
	return &Store{q: db.New(pool), log: log}
}

func (s *Store) Refresh(ctx context.Context) (int32, error) {
	started := time.Now()
	pairs, err := s.q.RefreshCopurchases(ctx)
	if err != nil {
		return 0, fmt.Errorf("refresh co-purchases: %w", err)
	}
	if pairs < 0 {
		s.log.InfoContext(ctx, "co-purchase rebuild skipped: another process is running it")
		return 0, nil
	}
	s.log.InfoContext(ctx, "co-purchase projection rebuilt",
		"pairs", pairs, "took_ms", time.Since(started).Milliseconds())
	return pairs, nil
}

func (s *Store) RefreshForever(ctx context.Context) {
	if _, err := s.Refresh(ctx); err != nil && ctx.Err() == nil {
		s.log.ErrorContext(ctx, "initial co-purchase refresh", "error", err)
	}

	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.Refresh(ctx); err != nil && ctx.Err() == nil {
				s.log.ErrorContext(ctx, "co-purchase refresh", "error", err)
			}
		}
	}
}
