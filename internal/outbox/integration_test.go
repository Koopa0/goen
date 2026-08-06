//go:build integration

package outbox_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

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

// enqueue writes a message directly, standing in for a business transaction.
// emptyOutbox clears the queue before a test that asserts on GLOBAL counts.
//
// Drain is global by design — that is what a worker does — so a message another
// test left pending is a message this one's counts include. The suite passed in
// file order and failed under -shuffle until each test started from empty.
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

// TestDrainDeliversAndStamps proves a handled message is marked done once.
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

	// A second drain must find nothing: a delivered message is done.
	delivered, _, err = s.Drain(ctx)
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if delivered != 0 {
		t.Errorf("the second drain delivered %d, want 0", delivered)
	}
}

// TestAFailedHandlerIsRetriedNotLost proves a failure leaves something to retry.
//
// The message stays pending with its attempt count and a reason. A queue that
// dropped what it could not deliver would be a queue that lies about being
// empty.
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

// TestBackoffPushesTheRetryIntoTheFuture proves a failing message is not retried
// every poll.
//
// Without it a permanently failing message is retried every poll, which turns
// one broken provider into a tight loop against it.
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

	// And the immediate next drain must not pick it up again.
	delivered, failed, err := s.Drain(ctx)
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if delivered+failed != 0 {
		t.Errorf("the backed-off message was claimed again immediately")
	}
}

// TestAnUnregisteredTopicIsKeptNotDropped proves an unknown topic survives.
//
// A deployment that publishes something it cannot yet consume must not lose the
// message: the next release may know what to do with it.
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

// TestTwoWorkersDoNotDeliverTheSameMessage is what SKIP LOCKED is for.
//
// Two goroutines drain at once against the same table. Without FOR UPDATE both
// send every message; without SKIP LOCKED the second waits on the first and
// does nothing useful.
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

	// DrainAll and not Drain, because twenty messages no longer fit in one
	// claim: BatchSize is eight, so that the lease a claim takes can cover the
	// serial work it implies. Two workers each draining to empty is also the
	// harder case — several contended claims rather than one — and it is what
	// Run does per tick.
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
// lease, not just a row lock.
//
// The row lock the claim takes lives only for that statement. Without a lease
// pushing available_at forward, a second worker's "due" predicate matches the
// same rows the moment the first claim returns — which is how the concurrency
// test above failed before the lease existed.
func TestAClaimedMessageIsInvisibleUntilItsLeaseExpires(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()
	key := uuid.NewString()
	enqueue(t, "test.lease", key, `{}`)

	// Claim it with a handler that does nothing, so the message is neither
	// delivered nor rescheduled — exactly the window a lease must cover.
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

// errStopHere makes a handler fail without pretending the failure means
// anything.
var errStopHere = errors.New("handler declined, for the test")

// TestOneTickDrainsABacklogRatherThanOneBatch proves the small batch did not
// buy exclusivity at the cost of throughput.
//
// BatchSize had to come down to eight, because a claim takes one lease over the
// whole batch and the batch is delivered serially — fifty messages at the send
// timeout is twenty-five minutes of work under a five-minute lease. Left there,
// eight per five-second tick would be a hard ceiling of ninety-six messages a
// minute, and a newsletter to ten thousand subscribers would take an hour and a
// half.
//
// So a pass that comes back FULL is followed by another claim. Each claim still
// fits inside its own lease, which is the property that had to hold; the queue
// still drains at the speed the handler runs.
func TestOneTickDrainsABacklogRatherThanOneBatch(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()

	// Two and a bit batches, so a single claim cannot account for them and the
	// last pass is a short one.
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

// TestDrainAllStopsWhenEveryMessageFails proves the loop cannot spin.
//
// "Claim again while the pass came back full" is a loop, and a full pass of
// FAILURES is the case it could run away on: a provider that is down fails all
// eight, and if those eight were still due the next claim would take the same
// eight again, at the speed of the database. What bounds it is the BACKOFF —
// a failed message is rescheduled into the future, so the second claim finds
// nothing.
//
// The damage without it is not a hung worker; MaxAttempts would stop it after
// eight passes. It is that those eight passes happen inside ONE tick, so a
// message that should have been retried over the next few hours has spent every
// attempt it has in a second and gone straight to Stuck() — a customer's receipt
// abandoned because the mail server was briefly unreachable.
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

// TestEnqueueIsIdempotent proves one dedupe key is one message. The dedupe key is what makes a retried business
// transaction produce one message.
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

// TestTheSweepKeepsWhatWentWrongAndDropsWhatWorked is the retention rule, and
// the second half is the one that matters.
//
// Nothing pruned outbox_messages: every message goen ever sent stayed, with its
// payload — which is where the mailed tokens live, because a reset link and an
// unsubscribe link travel in it. Unbounded growth was the visible half; a
// plaintext credential outliving its own row by years was the other.
//
// What must NOT be swept is a message that failed. A queue that forgets what it
// could not deliver reports itself empty, /admin/health stops listing it, and the
// customer nobody could email is nobody's problem any more.
func TestTheSweepKeepsWhatWentWrongAndDropsWhatWorked(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()
	s := outbox.NewStore(pool, slog.New(slog.DiscardHandler))

	// Three rows: one delivered long ago, one delivered just now, and one that
	// exhausted its attempts and is still undelivered.
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
		WHERE dedupe_key = 'stuck'`, outbox.MaxAttempts); err != nil {
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

	// And the stuck one is still what an operator is shown. Sweeping it would
	// make the queue look healthy by forgetting the failure.
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

// TestTheSweepLeavesAPendingMessageAlone proves the window is judged on
// delivered_at and not on age.
//
// available_at moves forward on every claim and every backoff, so a message
// legitimately waiting can be arbitrarily old by any other clock. Keying the
// delete on that instead would delete mail that has not been sent yet.
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
