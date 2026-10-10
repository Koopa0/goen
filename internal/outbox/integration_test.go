//go:build integration

package outbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/outbox"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	code := m.Run()
	stop()
	os.Exit(code)
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// emptyOutbox clears the queue before a test that asserts on GLOBAL counts.
// Drain is global by design, so another test's pending message is in this one's
// counts.
func emptyOutbox(t *testing.T) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `DELETE FROM outbox_messages`); err != nil {
		t.Fatalf("empty the outbox: %v", err)
	}
}

func enqueue(t *testing.T, topic, key, payload string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO outbox_messages (topic, dedupe_key, payload)
		VALUES ($1, $2, $3::jsonb)
		ON CONFLICT (topic, dedupe_key) DO NOTHING`, topic, key, payload); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
}

func TestDrainDeliversAndStamps(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()
	s := outbox.NewStore(pool, quiet())
	enqueue(t, "test.delivered", uuid.NewString(), `{"n":1}`)

	var seen [][]byte
	s.Handle("test.delivered", func(_ context.Context, p []byte) error {
		seen = append(seen, p)
		return nil
	})

	delivered, failed, err := s.Drain(ctx)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if delivered != 1 || failed != 0 {
		t.Fatalf("delivered=%d failed=%d, want 1 and 0", delivered, failed)
	}
	if len(seen) != 1 || string(seen[0]) != `{"n": 1}` && string(seen[0]) != `{"n":1}` {
		t.Errorf("handler saw %q", seen)
	}

	delivered, _, err = s.Drain(ctx)
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if delivered != 0 {
		t.Errorf("the second drain delivered %d, want 0", delivered)
	}
}

func TestAFailedHandlerIsRetriedNotLost(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()
	s := outbox.NewStore(pool, quiet())
	key := uuid.NewString()
	enqueue(t, "test.fails", key, `{}`)

	s.Handle("test.fails", func(context.Context, []byte) error {
		return errors.New("the provider said no")
	})

	delivered, failed, err := s.Drain(ctx)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if delivered != 0 || failed != 1 {
		t.Fatalf("delivered=%d failed=%d, want 0 and 1", delivered, failed)
	}

	var stamped bool
	var attempts int32
	var lastErr string
	if err := pool.QueryRow(ctx, `
		SELECT delivered_at IS NOT NULL, attempts, coalesce(last_error, '')
		FROM outbox_messages WHERE dedupe_key = $1`, key).Scan(&stamped, &attempts, &lastErr); err != nil {
		t.Fatalf("read: %v", err)
	}
	if stamped {
		t.Error("a failed message was marked delivered")
	}
	if attempts != 1 {
		t.Errorf("attempts is %d, want 1 — the count must rise on the ATTEMPT, "+
			"not on success, or it counts nothing about failures", attempts)
	}
	if lastErr == "" {
		t.Error("no reason recorded; a stuck message with no error is a mystery")
	}
}

func TestBackoffPushesTheRetryIntoTheFuture(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()
	s := outbox.NewStore(pool, quiet())
	key := uuid.NewString()
	enqueue(t, "test.backoff", key, `{}`)
	s.Handle("test.backoff", func(context.Context, []byte) error {
		return errors.New("still down")
	})

	if _, _, err := s.Drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}

	var due time.Time
	if err := pool.QueryRow(ctx,
		`SELECT available_at FROM outbox_messages WHERE dedupe_key = $1`, key).Scan(&due); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !due.After(time.Now()) {
		t.Errorf("the retry is due at %v, which is not in the future — a failing "+
			"message would be retried every poll", due)
	}

	delivered, failed, err := s.Drain(ctx)
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if delivered+failed != 0 {
		t.Errorf("the backed-off message was claimed again immediately")
	}
}

func TestRescheduleUsesTheDatabaseTransactionClock(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin clock transaction: %v", err)
	}
	cleanupCtx := context.WithoutCancel(t.Context())
	defer func() { _ = tx.Rollback(cleanupCtx) }()

	var id uuid.UUID
	var databaseNow time.Time
	owner := uuid.New()
	if err := tx.QueryRow(ctx, `
		INSERT INTO outbox_messages (topic, dedupe_key, payload, lease_owner)
		VALUES ('test.db-clock', $1, '{}'::jsonb, $2)
		RETURNING id, now()`, uuid.NewString(), owner).Scan(&id, &databaseNow); err != nil {
		t.Fatalf("enqueue in clock transaction: %v", err)
	}

	time.Sleep(250 * time.Millisecond)
	if n, err := db.New(tx).RescheduleOutbox(ctx, db.RescheduleOutboxParams{
		ID: id, LeaseOwner: owner,
		Backoff: pgtype.Interval{
			Microseconds: (50 * time.Millisecond).Microseconds(), Valid: true,
		},
		LastError: "clock proof",
	}); err != nil || n != 1 {
		t.Fatalf("reschedule: %d rows, %v", n, err)
	}

	var due time.Time
	if err := tx.QueryRow(ctx,
		`SELECT available_at FROM outbox_messages WHERE id=$1`, id).Scan(&due); err != nil {
		t.Fatalf("read reschedule time: %v", err)
	}
	want := databaseNow.Add(50 * time.Millisecond)
	if !due.Equal(want) {
		t.Fatalf("available_at=%v, want database now()+backoff=%v", due, want)
	}
	if !due.Before(time.Now()) {
		t.Fatal("fixture did not cross the process-clock boundary")
	}
}

// TestAnUnregisteredTopicIsKeptNotDropped: a deployment that publishes what it
// cannot yet consume must not lose the message.
func TestAnUnregisteredTopicIsKeptNotDropped(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()
	s := outbox.NewStore(pool, quiet())
	key := uuid.NewString()
	enqueue(t, "test.nobody.handles.this", key, `{}`)

	if _, failed, err := s.Drain(ctx); err != nil || failed != 1 {
		t.Fatalf("drain gave failed=%d err=%v, want 1 and nil", failed, err)
	}

	var stamped bool
	if err := pool.QueryRow(ctx,
		`SELECT delivered_at IS NOT NULL FROM outbox_messages WHERE dedupe_key = $1`,
		key).Scan(&stamped); err != nil {
		t.Fatalf("the message was deleted: %v", err)
	}
	if stamped {
		t.Error("a message nobody handled was marked delivered")
	}
}

func TestTwoWorkersDoNotDeliverTheSameMessage(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()

	const messages = 20
	for range messages {
		enqueue(t, "test.concurrent", uuid.NewString(), `{}`)
	}

	var mu sync.Mutex
	handled := map[string]int{}
	count := func(_ context.Context, p []byte) error {
		mu.Lock()
		defer mu.Unlock()
		handled[string(p)]++
		return nil
	}

	// Distinct payloads, so a double delivery is visible rather than merely
	// possible.
	if _, err := pool.Exec(ctx, `
		UPDATE outbox_messages SET payload = jsonb_build_object('k', dedupe_key)
		WHERE topic = 'test.concurrent'`); err != nil {
		t.Fatalf("stamp payloads: %v", err)
	}

	a := outbox.NewStore(pool, quiet())
	b := outbox.NewStore(pool, quiet())
	a.Handle("test.concurrent", count)
	b.Handle("test.concurrent", count)

	// DrainAll and not Drain: twenty messages no longer fit in one claim, and two
	// workers each draining to empty is the harder case.
	var wg sync.WaitGroup
	wg.Go(func() { _, _, _ = a.DrainAll(ctx) })
	wg.Go(func() { _, _, _ = b.DrainAll(ctx) })
	wg.Wait()

	// Counted from what the handlers SAW, not from what Drain reported: the
	// totals could add up while the same message went to both.
	if len(handled) != messages {
		t.Errorf("%d distinct messages were handled, want %d", len(handled), messages)
	}
	for payload, n := range handled {
		if n != 1 {
			t.Errorf("payload %s was delivered %d times — the claim is not holding "+
				"a lease, so a second worker sees it the instant the first "+
				"statement returns", payload, n)
		}
	}
}

// TestAClaimedMessageIsInvisibleUntilItsLeaseExpires proves the claim takes a
// lease, not just a row lock — that lock lives only for its own statement.
func TestAClaimedMessageIsInvisibleUntilItsLeaseExpires(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()
	key := uuid.NewString()
	enqueue(t, "test.lease", key, `{}`)

	claimer := outbox.NewStore(pool, quiet())
	claimer.Handle("test.lease", func(context.Context, []byte) error {
		return errStopHere
	})
	if _, failed, err := claimer.Drain(ctx); err != nil || failed != 1 {
		t.Fatalf("claim: failed=%d err=%v", failed, err)
	}

	var due time.Time
	if err := pool.QueryRow(ctx,
		`SELECT available_at FROM outbox_messages WHERE dedupe_key = $1`, key).Scan(&due); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !due.After(time.Now()) {
		t.Errorf("a claimed message is due at %v, which is now or earlier — any "+
			"other worker can take it", due)
	}
}

// TestALateWorkerCannotSettleAClaimItLost: a worker that overran its lease
// finds the message claimed again, and whatever its handler concluded, the
// later claim's state stands — a late reschedule would otherwise hand the
// message to a third worker while the second is still sending it.
func TestALateWorkerCannotSettleAClaimItLost(t *testing.T) {
	type messageState struct {
		AvailableAt time.Time
		Attempts    int32
		Delivered   bool
		LastError   string
		LeaseOwner  string
	}
	for _, tt := range []struct {
		name    string
		outcome error
	}{
		{name: "late delivery", outcome: nil},
		{name: "late failure", outcome: errStopHere},
	} {
		t.Run(tt.name, func(t *testing.T) {
			emptyOutbox(t)
			ctx := t.Context()
			key := uuid.NewString()
			enqueue(t, "test.lost-claim", key, `{}`)
			read := func() messageState {
				t.Helper()
				var s messageState
				if err := pool.QueryRow(ctx, `
					SELECT available_at, attempts, delivered_at IS NOT NULL,
					       coalesce(last_error, ''), coalesce(lease_owner::text, '')
					FROM outbox_messages WHERE dedupe_key = $1`, key).
					Scan(&s.AvailableAt, &s.Attempts, &s.Delivered, &s.LastError, &s.LeaseOwner); err != nil {
					t.Fatalf("read the message: %v", err)
				}
				return s
			}

			var claimedAgain messageState
			late := outbox.NewStore(pool, quiet())
			late.Handle("test.lost-claim", func(ctx context.Context, _ []byte) error {
				if _, err := pool.Exec(ctx,
					`UPDATE outbox_messages SET available_at = now() WHERE dedupe_key = $1`, key); err != nil {
					return fmt.Errorf("expire the lease: %w", err)
				}
				claimed, err := db.New(pool).ClaimOutbox(ctx, db.ClaimOutboxParams{
					BatchSize: 1, LeaseOwner: uuid.New(),
					Lease: pgtype.Interval{Microseconds: outbox.Lease.Microseconds(), Valid: true},
				})
				if err != nil || len(claimed) != 1 {
					return fmt.Errorf("second claim took %d messages: %w", len(claimed), err)
				}
				claimedAgain = read()
				return tt.outcome
			})

			if _, _, err := late.Drain(ctx); err != nil {
				t.Fatalf("Drain with a lost claim = %v, want nil", err)
			}
			if claimedAgain.Attempts != 2 {
				t.Fatalf("the second claim never took the message: %+v", claimedAgain)
			}
			if diff := cmp.Diff(claimedAgain, read()); diff != "" {
				t.Errorf("the %s of an expired claim changed the second claim's message (-claimed +after):\n%s", tt.name, diff)
			}
		})
	}
}

// TestTheHandlerIsGivenItsBudget: [outbox.HandlerBudget] is what the lease
// arithmetic spends on one message, so the delivery path is where it has to be
// applied. A handler nothing can cut short holds its claim until the lease
// expires and hands the tail of its own batch to a second replica.
func TestTheHandlerIsGivenItsBudget(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()
	s := outbox.NewStore(pool, quiet())
	enqueue(t, "test.budget", uuid.NewString(), `{}`)

	var budget time.Duration
	var bounded bool
	s.Handle("test.budget", func(handlerCtx context.Context, _ []byte) error {
		var deadline time.Time
		deadline, bounded = handlerCtx.Deadline()
		budget = time.Until(deadline)
		return nil
	})

	if delivered, _, err := s.Drain(ctx); err != nil || delivered != 1 {
		t.Fatalf("drain: delivered=%d err=%v, want 1 and nil", delivered, err)
	}
	if !bounded {
		t.Fatal("the handler was given a context with no deadline: nothing enforces " +
			"HandlerBudget, and a handler that hangs keeps its claim for the whole lease")
	}
	// The slack is the time between the deadline being set and the handler reading it.
	if budget > outbox.HandlerBudget || budget < outbox.HandlerBudget-time.Second {
		t.Errorf("the handler was given %v, want %v", budget, outbox.HandlerBudget)
	}
}

var errStopHere = errors.New("handler declined, for the test")

func TestOneTickDrainsABacklogRatherThanOneBatch(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()

	// Two and a bit batches, so the last pass is a short one.
	const backlog = outbox.BatchSize*2 + 3
	for range backlog {
		enqueue(t, "test.backlog", uuid.NewString(), `{}`)
	}

	s := outbox.NewStore(pool, quiet())
	var handled int
	s.Handle("test.backlog", func(context.Context, []byte) error {
		handled++
		return nil
	})

	delivered, failed, err := s.DrainAll(ctx)
	if err != nil {
		t.Fatalf("drain all: %v", err)
	}
	if delivered != backlog || failed != 0 {
		t.Errorf("DrainAll delivered %d of %d (failed %d) — one tick left %d "+
			"messages waiting five seconds for the next one", delivered, backlog,
			failed, backlog-delivered)
	}
	if handled != backlog {
		t.Errorf("the handler ran %d times, want %d", handled, backlog)
	}

	var pending int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox_messages WHERE delivered_at IS NULL`).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pending != 0 {
		t.Errorf("%d messages are still pending after DrainAll", pending)
	}
}

func TestDrainAllStopsWhenEveryMessageFails(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()

	for range outbox.BatchSize {
		enqueue(t, "test.allfail", uuid.NewString(), `{}`)
	}

	s := outbox.NewStore(pool, quiet())
	var attempts int
	s.Handle("test.allfail", func(context.Context, []byte) error {
		attempts++
		return errStopHere
	})

	done := make(chan struct {
		failed int
		err    error
	}, 1)
	go func() {
		_, failed, err := s.DrainAll(ctx)
		done <- struct {
			failed int
			err    error
		}{failed, err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("drain all: %v", got.err)
		}
		if got.failed != outbox.BatchSize {
			t.Errorf("DrainAll reported %d failures for %d messages — the failed "+
				"batch is being claimed again inside the same drain, so one tick "+
				"burns every retry a message has", got.failed, outbox.BatchSize)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("DrainAll did not return: a full pass of failures is being claimed " +
			"again and again with nothing bounding it")
	}

	if attempts != outbox.BatchSize {
		t.Errorf("the handler ran %d times for %d messages, want one run each", attempts, outbox.BatchSize)
	}
}

func TestEnqueueIsIdempotent(t *testing.T) {
	key := uuid.NewString()
	for range 3 {
		enqueue(t, "test.idempotent", key, `{}`)
	}
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM outbox_messages WHERE dedupe_key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d rows for one dedupe key, want 1", n)
	}
}

// TestTheSweepKeepsWhatWentWrongAndDropsWhatWorked: a FAILED message must not be
// swept, or the queue reports itself empty and /admin/health stops listing it.
func TestTheSweepKeepsWhatWentWrongAndDropsWhatWorked(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()
	s := outbox.NewStore(pool, slog.New(slog.DiscardHandler))

	enqueue(t, "sweep.old", "old", `{}`)
	enqueue(t, "sweep.fresh", "fresh", `{}`)
	enqueue(t, "sweep.stuck", "stuck", `{}`)
	if _, err := pool.Exec(ctx, `
		UPDATE outbox_messages SET delivered_at = now() - interval '60 days'
		WHERE dedupe_key = 'old'`); err != nil {
		t.Fatalf("age the delivered message: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE outbox_messages SET delivered_at = now() WHERE dedupe_key = 'fresh'`); err != nil {
		t.Fatalf("stamp the fresh message: %v", err)
	}
	// Stuck AND old: age alone must not be enough to sweep it.
	if _, err := pool.Exec(ctx, `
		UPDATE outbox_messages SET attempts = $1, available_at = now() - interval '60 days',
		       last_error = 'nothing accepted it'
		WHERE dedupe_key = 'stuck'`, outbox.StuckAfterAttempts); err != nil {
		t.Fatalf("wedge the stuck message: %v", err)
	}

	n, sweepErr := s.Sweep(ctx)
	if sweepErr != nil {
		t.Fatalf("Sweep: %v", sweepErr)
	}
	if n != 1 {
		t.Errorf("Sweep deleted %d rows, want 1", n)
	}

	for key, want := range map[string]bool{"old": false, "fresh": true, "stuck": true} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM outbox_messages WHERE dedupe_key = $1)`,
			key).Scan(&exists); err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		if exists != want {
			t.Errorf("%s: exists = %v, want %v", key, exists, want)
		}
	}

	stuck, err := s.Stuck(ctx, 10)
	if err != nil {
		t.Fatalf("Stuck: %v", err)
	}
	found := false
	for _, m := range stuck {
		if m.Topic == "sweep.stuck" {
			found = true
		}
	}
	if !found {
		t.Errorf("the swept queue no longer lists its stuck message: %+v", stuck)
	}
}

// TestTheSweepLeavesAPendingMessageAlone: available_at moves forward on every
// claim, so a message legitimately waiting can be arbitrarily old by that clock.
func TestTheSweepLeavesAPendingMessageAlone(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()
	s := outbox.NewStore(pool, slog.New(slog.DiscardHandler))

	enqueue(t, "sweep.pending", "pending", `{}`)
	if _, err := pool.Exec(ctx, `
		UPDATE outbox_messages
		SET available_at = now() - interval '90 days'
		WHERE dedupe_key = 'pending'`); err != nil {
		t.Fatalf("age the pending message: %v", err)
	}

	if n, err := s.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	} else if n != 0 {
		t.Errorf("Sweep deleted %d undelivered rows, want 0", n)
	}
}

// TestAMessageThatExhaustedItsAttemptsIsRetriedDaily: mail queued during a long
// provider outage must still go out once the provider is back, so running out of
// quick retries slows a message down and does not end it.
func TestAMessageThatExhaustedItsAttemptsIsRetriedDaily(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()
	s := outbox.NewStore(pool, quiet())
	key := uuid.NewString()
	enqueue(t, "test.outage", key, `{}`)

	up := false
	s.Handle("test.outage", func(context.Context, []byte) error {
		if !up {
			return errors.New("provider down")
		}
		return nil
	})

	for range outbox.StuckAfterAttempts {
		if _, err := pool.Exec(ctx,
			`UPDATE outbox_messages SET available_at = now() WHERE dedupe_key = $1`, key); err != nil {
			t.Fatalf("make it due: %v", err)
		}
		if _, _, err := s.Drain(ctx); err != nil {
			t.Fatalf("drain: %v", err)
		}
	}
	var wait time.Duration
	if err := pool.QueryRow(ctx, `
		SELECT available_at - now() FROM outbox_messages WHERE dedupe_key = $1`, key).Scan(&wait); err != nil {
		t.Fatalf("read the next slot: %v", err)
	}
	if wait < 23*time.Hour || wait > 25*time.Hour {
		t.Errorf("the next retry is %v away, want about a day", wait)
	}

	up = true
	if _, err := pool.Exec(ctx,
		`UPDATE outbox_messages SET available_at = now() WHERE dedupe_key = $1`, key); err != nil {
		t.Fatalf("reach the daily slot: %v", err)
	}
	delivered, _, err := s.Drain(ctx)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if delivered != 1 {
		t.Errorf("delivered %d on the daily slot, want 1: an exhausted message was abandoned", delivered)
	}
}

// TestTheSweepDropsAnUndeliveredMessagePastRetain: the payload of a message that
// never went out can carry a token, so it is bounded by its age like a delivered
// one; a young undelivered message and a delivered one keep their own rules.
func TestTheSweepDropsAnUndeliveredMessagePastRetain(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()
	var logs bytes.Buffer
	s := outbox.NewStore(pool, slog.New(slog.NewJSONHandler(&logs, nil)))

	for _, m := range []struct{ topic, key string }{
		{outbox.TopicOrderPlaced.Name(), "stale"},
		{outbox.TopicOrderShipped.Name(), "young"},
		{"sweep.delivered", "delivered-old"},
		{"sweep.delivered", "delivered-old-second"},
		{"sweep.delivered", "delivered-fresh"},
	} {
		enqueue(t, m.topic, m.key, `{"token":"secret"}`)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE outbox_messages SET created_at = now() - interval '31 days',
		       attempts = $1, available_at = now() + interval '1 day'
		WHERE dedupe_key = 'stale'`, outbox.StuckAfterAttempts); err != nil {
		t.Fatalf("age the stale message: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE outbox_messages SET created_at = now() - interval '60 days',
		       delivered_at = now() - interval '31 days'
		WHERE dedupe_key IN ('delivered-old', 'delivered-old-second')`); err != nil {
		t.Fatalf("age the delivered message: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE outbox_messages SET created_at = now() - interval '60 days',
		       delivered_at = now() - interval '1 day'
		WHERE dedupe_key = 'delivered-fresh'`); err != nil {
		t.Fatalf("stamp the fresh delivered message: %v", err)
	}
	var expired struct {
		ID        uuid.UUID
		Topic     string
		Attempts  int32
		CreatedAt time.Time
	}
	if err := pool.QueryRow(ctx, `
		SELECT id, topic, attempts, created_at FROM outbox_messages
		WHERE dedupe_key = 'stale'`).Scan(
		&expired.ID, &expired.Topic, &expired.Attempts, &expired.CreatedAt); err != nil {
		t.Fatalf("read the expiring message: %v", err)
	}

	n, err := s.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 3 {
		t.Errorf("Sweep deleted %d rows, want 3 (one stale undelivered, two old delivered)", n)
	}
	wantLogs := []map[string]any{
		{
			"level": "WARN", "msg": "outbox message expired",
			"message": expired.ID.String(), "topic": expired.Topic,
			"attempts": float64(expired.Attempts), "created_at": expired.CreatedAt.Format(time.RFC3339Nano),
		},
		{
			"level": "INFO", "msg": "outbox swept",
			"delivered": float64(2), "undelivered": float64(1), "retain_days": float64(30),
		},
	}
	gotLogs := make([]map[string]any, 0, len(wantLogs))
	for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode sweep log: %v", err)
		}
		delete(record, "time")
		gotLogs = append(gotLogs, record)
	}
	if diff := cmp.Diff(wantLogs, gotLogs); diff != "" {
		t.Errorf("Sweep logs (-want +got):\n%s", diff)
	}
	for key, want := range map[string]bool{
		"stale": false, "young": true, "delivered-old": false,
		"delivered-old-second": false, "delivered-fresh": true,
	} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM outbox_messages WHERE dedupe_key = $1)`,
			key).Scan(&exists); err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		if exists != want {
			t.Errorf("%s: exists = %v, want %v", key, exists, want)
		}
	}
	logs.Reset()
	if n, err := s.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("repeat Sweep = %d, %v, want 0, nil", n, err)
	}
	if logs.Len() != 0 {
		t.Errorf("repeat Sweep logged messages it did not expire: %s", &logs)
	}
}

// TestAMessageSentAsShutdownLandsIsStillStamped: SIGTERM cancels the drain's
// context between the handler's success and the stamp. The stamp must survive
// it, or the next process sends the letter again once the lease expires.
func TestAMessageSentAsShutdownLandsIsStillStamped(t *testing.T) {
	emptyOutbox(t)
	s := outbox.NewStore(pool, quiet())
	key := uuid.NewString()
	enqueue(t, "test.shutdown.sent", key, `{}`)

	ctx, shutdown := context.WithCancel(t.Context())
	defer shutdown()
	s.Handle("test.shutdown.sent", func(context.Context, []byte) error {
		shutdown()
		return nil
	})

	if _, _, err := s.Drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}

	var stamped bool
	if err := pool.QueryRow(t.Context(), `
		SELECT delivered_at IS NOT NULL FROM outbox_messages WHERE dedupe_key = $1`, key).Scan(&stamped); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !stamped {
		t.Error("a message whose handler succeeded was left unstamped by the shutdown")
	}
}

// TestAFailureAsShutdownLandsIsStillRecorded: the reschedule shares the stamp's
// context, and a failure that is not recorded leaves the message claimed until
// the lease expires with no reason on the row.
func TestAFailureAsShutdownLandsIsStillRecorded(t *testing.T) {
	emptyOutbox(t)
	s := outbox.NewStore(pool, quiet())
	key := uuid.NewString()
	enqueue(t, "test.shutdown.failed", key, `{}`)

	ctx, shutdown := context.WithCancel(t.Context())
	defer shutdown()
	s.Handle("test.shutdown.failed", func(context.Context, []byte) error {
		shutdown()
		return errors.New("the provider said no")
	})

	if _, _, err := s.Drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}

	var lastErr string
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce(last_error, '') FROM outbox_messages WHERE dedupe_key = $1`, key).Scan(&lastErr); err != nil {
		t.Fatalf("read: %v", err)
	}
	if lastErr == "" {
		t.Error("a failure that landed with the shutdown was not recorded")
	}
}
