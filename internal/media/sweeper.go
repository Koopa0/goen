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
//
// Hourly, because the cost of an unreferenced row is disk rather than a wrong
// answer — the opposite of the cart sweeper, whose delay is stock a customer
// cannot buy. There is no hurry, and an hourly pass over a partial predicate
// costs nothing.
const SweepInterval = time.Hour

// SweepBatch bounds one pass. Images are large and each delete is a page write;
// a pass that tried to reclaim a year of abandoned uploads would hold the table
// for as long as that takes.
const SweepBatch = 100

// SweepGrace is how long an upload is left alone before it counts as abandoned.
//
// It is in the QUERY (created_at < now() - 24 hours) and named here so the two
// are read together. The window exists because an upload is stored before it is
// attached: those are two requests, and a staff member who uploads an image and
// then goes to lunch must not come back to a broken form.
const SweepGrace = 24 * time.Hour

// Sweep reclaims uploads nothing points at, once.
//
// Nothing called this before. Every abandoned upload — a staff member who
// picked the wrong file, a form submitted without saving — stayed in
// media_objects for good, and the images are stored as bytes in PostgreSQL, so
// that is the whole cost of the storage decision paid for nothing.
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
			// A digest attached between the read and the delete is a foreign key
			// refusing, and THAT is the guard working: it must not take the rest
			// of the batch with it.
			//
			// Anything else is not. This branch used to log every failure at Warn
			// under the sentence above, so when the sweeper was wired on the
			// storefront pool — where `store` holds SELECT on media_objects and
			// nothing else — a permission denial was recorded, hourly and
			// forever, as the guard working. Sweep returned nil, `reclaimed`
			// stayed 0, and SweepForever logs nothing when it reclaims nothing.
			// Nothing was ever reclaimed and the log said everything was fine.
			//
			// So the expected refusal is told apart from every other one by its
			// SQLSTATE, and an unexpected failure stops the pass and is returned:
			// a sweeper that cannot delete is broken, not busy.
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
// expects: a digest that something attached between the read and the delete.
//
// Bound to the SQLSTATE rather than to the message, for the reason this
// repository binds every constraint assertion to a name: a statement meant to
// trip one rule routinely trips another first, and an error classified by what
// it looks like is an error classified wrong. 23503 is foreign_key_violation.
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
