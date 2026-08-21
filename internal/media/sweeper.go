package media

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// SweepInterval is how often abandoned uploads are reclaimed.
const SweepInterval = time.Hour

// SweepBatch bounds one pass.
const SweepBatch = 100

// SweepGrace is how long an upload is left alone before it counts as abandoned.
// The window itself lives in UnreferencedMedia's WHERE clause; changing this
// constant alone changes nothing.
const SweepGrace = 24 * time.Hour

// Candidates is the list Sweep works from: uploads nothing points at and that
// are older than the grace window.
//
// Exported so the window between the list and the deletes can be DRIVEN in a
// test. Sweep is two statements, and the defect it used to carry lived between
// them; a test that calls Sweep alone re-reads the list and never meets it.
func (s *Store) Candidates(ctx context.Context, limit int32) ([]string, error) {
	return s.q.UnreferencedMedia(ctx, limit)
}

// Reclaim deletes one candidate, and reports whether it went. Zero means
// something points at it now — the DELETE asks again — so the upload is live.
func (s *Store) Reclaim(ctx context.Context, digest string) (bool, error) {
	gone, err := s.q.DeleteMedia(ctx, digest)
	if err != nil {
		return false, fmt.Errorf("reclaim %s: %w", digest, err)
	}
	return gone > 0, nil
}

// Sweep reclaims uploads nothing points at, once.
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
			// The DELETE re-asked whether anything points at it and something
			// does — attached between the read and here. Not a failure; the
			// upload is live and the next pass will not offer it.
			log.WarnContext(ctx, "upload was attached between the read and the delete",
				"digest", digest)
			continue
		}
		reclaimed++
	}
	return reclaimed, nil
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
