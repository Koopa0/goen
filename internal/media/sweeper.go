package media

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// SweepInterval is how often abandoned uploads are reclaimed.
const SweepInterval = time.Hour

// SweepBatch bounds one pass.
const SweepBatch = 100

// SweepGrace is how long an upload is left alone before it counts as abandoned.
// The window itself lives in UnreferencedMedia's WHERE clause; changing this
// constant alone changes nothing.
const SweepGrace = 24 * time.Hour

// Sweep reclaims uploads nothing points at, once.
func (s *Store) Sweep(ctx context.Context, log *slog.Logger) (reclaimed int, err error) {
	digests, err := s.q.UnreferencedMedia(ctx, SweepBatch)
	if err != nil {
		return 0, err
	}
	for _, digest := range digests {
		if ctx.Err() != nil {
			return reclaimed, ctx.Err()
		}
		if delErr := s.q.DeleteMedia(ctx, digest); delErr != nil {
			// A digest attached between the read and the delete is the foreign
			// key working. Anything else stops the pass, or a sweeper that can
			// delete nothing reports every hour as healthy.
			if !foreignKeyRefusal(delErr) {
				return reclaimed, fmt.Errorf("reclaim %s: %w", digest, delErr)
			}
			log.WarnContext(ctx, "upload was attached between the read and the delete",
				"digest", digest)
			continue
		}
		reclaimed++
	}
	return reclaimed, nil
}

// foreignKeyRefusal reports whether err is the one failure [Store.Sweep]
// expects. SQLSTATE 23503 is foreign_key_violation.
func foreignKeyRefusal(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23503"
}

// SweepForever runs Sweep on a ticker until ctx is cancelled.
func (s *Store) SweepForever(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reclaimed, err := s.Sweep(ctx, log)
			switch {
			case err != nil && ctx.Err() == nil:
				log.ErrorContext(ctx, "sweep unreferenced media", "error", err)
			case reclaimed > 0:
				log.InfoContext(ctx, "reclaimed unreferenced uploads", "count", reclaimed)
			}
		}
	}
}
