package backinstock

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
)

type Store struct {
	q *db.Queries
}

// NewStore takes the handle of the role that runs the method called:
// [Store.Request] runs as `store`, from the product page, and [Store.Sweep]
// needs the maintenance pool, because no other login may execute the function
// that deletes a request.
func NewStore(dbtx db.DBTX) *Store {
	if dbtx == nil {
		panic("backinstock: NewStore requires a database handle")
	}
	return &Store{q: db.New(dbtx)}
}

// Sweep deletes requests notified more than [NotifiedRetain] ago, a batch at a
// time until a batch comes back short, and reports how many went. A request
// still waiting for stock is never touched.
func (s *Store) Sweep(ctx context.Context) (int64, error) {
	retain := pgtype.Interval{Microseconds: int64(NotifiedRetain / time.Microsecond), Valid: true}
	var total int64
	for {
		n, err := s.q.SweepNotifiedStockNotifications(ctx, db.SweepNotifiedStockNotificationsParams{
			Retain: retain, Batch: SweepBatch,
		})
		total += n
		if err != nil {
			return total, fmt.Errorf("sweep notified restock requests: %w", err)
		}
		if n < SweepBatch {
			return total, nil
		}
	}
}

// SweepForever runs once at startup, so a host redeployed more often than the
// interval still clears notified requests.
func (s *Store) SweepForever(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(SweepInterval)
	defer t.Stop()
	for {
		n, err := s.Sweep(ctx)
		if err != nil && ctx.Err() == nil {
			log.ErrorContext(ctx, "sweep notified restock requests", "error", err)
		}
		if n > 0 {
			log.InfoContext(ctx, "deleted notified restock requests", "deleted", n,
				"retain_days", int(NotifiedRetain.Hours()/24))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
