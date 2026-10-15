package media

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const SweepInterval = time.Hour

const SweepBatch = 100

// Candidates is the list Sweep works from: uploads nothing points at and that
// are older than the grace window.
//
// Exported so a test can drive the window between the list and the deletes.
func (s *Store) Candidates(ctx context.Context, limit int32) ([]string, error) {
	return s.q.UnreferencedMedia(ctx, limit)
}

// Reclaim deletes one candidate, and reports whether it went. Zero means
// something points at it now — the DELETE asks again — so the upload is live.
// So does a restrict_violation: a reference the DELETE could not see, an
// attach that committed while it waited, is refused by the ON DELETE RESTRICT
// foreign key instead.
func (s *Store) Reclaim(ctx context.Context, digest string) (bool, error) {
	gone, err := s.q.DeleteMedia(ctx, digest)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == restrictViolation {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reclaim %s: %w", digest, err)
	}
	return gone > 0, nil
}

// restrictViolation is SQLSTATE 23001 (PostgreSQL docs, Appendix A). A DELETE
// from media_objects can violate only a foreign key that references it.
const restrictViolation = "23001"

func (s *Store) Sweep(ctx context.Context, log *slog.Logger) (reclaimed int, err error) {
	digests, err := s.Candidates(ctx, SweepBatch)
	if err != nil {
		return 0, err
	}
	for _, digest := range digests {
		if ctx.Err() != nil {
			return reclaimed, ctx.Err()
		}
		gone, delErr := s.Reclaim(ctx, digest)
		if delErr != nil {
			// Anything at all stops the pass: a sweeper that can delete nothing
			// must not report every hour as healthy.
			return reclaimed, delErr
		}
		if !gone {
			// The DELETE re-asked and something points at it now, attached
			// between the read and here. Not a failure: the upload is live.
			log.WarnContext(ctx, "upload was attached between the read and the delete",
				"digest", digest)
			continue
		}
		reclaimed++
	}
	return reclaimed, nil
}

// SweepForever runs once at startup, so a host redeployed more often than the
// interval still reclaims unreferenced uploads.
func (s *Store) SweepForever(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(SweepInterval)
	defer ticker.Stop()
	for {
		reclaimed, err := s.Sweep(ctx, log)
		switch {
		case err != nil && ctx.Err() == nil:
			log.ErrorContext(ctx, "sweep unreferenced media", "error", err)
		case reclaimed > 0:
			log.InfoContext(ctx, "reclaimed unreferenced uploads", "count", reclaimed)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
