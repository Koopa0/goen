//go:build integration

package outbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/email"
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
	if err := tx.QueryRow(ctx, `
		INSERT INTO outbox_messages (topic, dedupe_key, payload)
		VALUES ('test.db-clock', $1, '{}'::jsonb)
		RETURNING id, now()`, uuid.NewString()).Scan(&id, &databaseNow); err != nil {
		t.Fatalf("enqueue in clock transaction: %v", err)
	}

	time.Sleep(250 * time.Millisecond)
	if err := db.New(tx).RescheduleOutbox(ctx, db.RescheduleOutboxParams{
		ID: id,
		Backoff: pgtype.Interval{
			Microseconds: (50 * time.Millisecond).Microseconds(), Valid: true,
		},
		LastError: "clock proof",
	}); err != nil {
		t.Fatalf("reschedule: %v", err)
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
		UPDATE outbox_messages SET attempts = $1, blocked_at = now(),
		       available_at = now() - interval '60 days', last_error = 'nothing accepted it'
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

type dummyPasswordReset struct {
	Token string `json:"token"`
	Email string `json:"email"`
}

// TestPoisonPayloadIsRedactedAndDoesNotReinvoke guards against token retention
// in unsendable messages: a poison payload with corrupt JSON must be dead-lettered
// on the first Drain, scrubbing its payload so secrets are not retained, and a
// second Drain must not re-claim the row or re-invoke SMTP.
func TestPoisonPayloadIsRedactedAndDoesNotReinvoke(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()
	s := outbox.NewStore(pool, quiet())
	key := uuid.NewString()

	// Enqueue an account.password_reset with unmarshalable JSON (token is integer instead of string).
	enqueue(t, outbox.TopicPasswordReset, key, `{"token": 999999, "email": "victim@example.com"}`)

	var smtpCalls int
	s.HandleJSON[dummyPasswordReset](outbox.TopicPasswordReset, func(_ context.Context, _ *dummyPasswordReset) error {
		smtpCalls++
		return nil
	})

	delivered, failed, err := s.Drain(ctx)
	if err != nil {
		t.Fatalf("first drain: %v", err)
	}
	if delivered != 0 || failed != 1 {
		t.Fatalf("delivered=%d failed=%d, want 0 and 1", delivered, failed)
	}
	if smtpCalls != 0 {
		t.Errorf("handler/SMTP was invoked %d times on corrupt JSON, want 0", smtpCalls)
	}

	var attempts int32
	var payload []byte
	var lastErr string
	var deliveredAt pgtype.Timestamptz
	var blockedAt pgtype.Timestamptz
	if queryErr := pool.QueryRow(ctx, `
		SELECT attempts, payload, coalesce(last_error, ''), delivered_at, blocked_at
		FROM outbox_messages WHERE dedupe_key = $1`, key).Scan(
		&attempts, &payload, &lastErr, &deliveredAt, &blockedAt); queryErr != nil {
		t.Fatalf("read message: %v", queryErr)
	}

	if attempts < 1 {
		t.Errorf("attempts = %d, want >= 1", attempts)
	}
	if string(payload) != "{}" && string(payload) != "{ }" {
		t.Errorf("payload = %s, want empty/redacted JSON to drop secrets", string(payload))
	}
	if lastErr == "" {
		t.Error("last_error is empty, want failure reason recorded")
	}
	if deliveredAt.Valid {
		t.Error("delivered_at is set on poison; it must stay NULL so /admin/health lists it")
	}
	if !blockedAt.Valid {
		t.Error("blocked_at is NULL on poison; automatic retry must stop")
	}

	// Second Drain: must not re-claim the dead-lettered message or invoke SMTP.
	delivered2, failed2, err2 := s.Drain(ctx)
	if err2 != nil {
		t.Fatalf("second drain: %v", err2)
	}
	if delivered2 != 0 || failed2 != 0 {
		t.Errorf("second drain delivered=%d failed=%d, want 0 and 0", delivered2, failed2)
	}
	if smtpCalls != 0 {
		t.Errorf("second drain invoked handler/SMTP %d times, want 0", smtpCalls)
	}

	// Verify StuckOutbox still finds it for /admin/health.
	stuck, stuckErr := s.Stuck(ctx, 10)
	if stuckErr != nil {
		t.Fatalf("Stuck: %v", stuckErr)
	}
	var found bool
	for _, m := range stuck {
		if m.DedupeKey == key {
			found = true
			if m.ID == uuid.Nil {
				t.Error("stuck message has nil ID; operator cannot act on it")
			}
			if m.Recoverable {
				t.Error("poison message is recoverable; redacted payloads must not replay")
			}
		}
	}
	if !found {
		t.Errorf("StuckOutbox did not list the dead-lettered message %s", key)
	}
}

// TestReviewSenderConfigurationFailureKeepsRecoverableMail guards that a deployment
// From misconfiguration does not redact an otherwise valid queued message and
// that operator replay can deliver the original row after configuration is fixed.
func TestReviewSenderConfigurationFailureKeepsRecoverableMail(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()
	key := uuid.NewString()
	wantPayload := `{"locale":"en","order_number":"GO-260101-000001","email":"buyer@example.com","name":"Alex","amount_cents":123400}`
	enqueue(t, outbox.TopicOrderPaid, key, wantPayload)

	badNotifier := email.New(
		email.SMTPSender{Addr: "127.0.0.1:25", From: "not an email"},
		"https://goen.test", "", "")
	worker := outbox.NewStore(pool, quiet())
	worker.HandleJSON[email.OrderPaid](outbox.TopicOrderPaid, badNotifier.SendOrderPaid)

	delivered, failed, err := worker.Drain(ctx)
	if err != nil {
		t.Fatalf("first drain: %v", err)
	}
	if delivered != 0 || failed != 1 {
		t.Fatalf("delivered=%d failed=%d, want 0 and 1", delivered, failed)
	}

	var payload []byte
	if readErr := pool.QueryRow(ctx, `
		SELECT payload FROM outbox_messages WHERE dedupe_key = $1`, key).Scan(&payload); readErr != nil {
		t.Fatalf("read message: %v", readErr)
	}
	if !jsonEqual(payload, wantPayload) {
		t.Fatalf("payload = %s, want original recoverable payload preserved", payload)
	}

	if _, execErr := pool.Exec(ctx, `
		UPDATE outbox_messages
		SET attempts = $1, available_at = now()
		WHERE dedupe_key = $2`, outbox.MaxAttempts-1, key); execErr != nil {
		t.Fatalf("prime final attempt: %v", execErr)
	}
	if _, failed, err = worker.Drain(ctx); err != nil {
		t.Fatalf("blocking drain: %v", err)
	}
	if failed != 1 {
		t.Fatalf("blocking drain failed=%d, want 1", failed)
	}

	var attempts int32
	var blockedAt pgtype.Timestamptz
	if readErr := pool.QueryRow(ctx, `
		SELECT attempts, payload, blocked_at
		FROM outbox_messages WHERE dedupe_key = $1`, key).Scan(&attempts, &payload, &blockedAt); readErr != nil {
		t.Fatalf("read blocked message: %v", readErr)
	}
	if !jsonEqual(payload, wantPayload) {
		t.Fatalf("blocked payload = %s, want %s", payload, wantPayload)
	}
	if !blockedAt.Valid {
		t.Fatal("blocked_at is NULL after exhausting attempts")
	}
	if attempts < outbox.MaxAttempts {
		t.Fatalf("attempts = %d, want >= %d", attempts, outbox.MaxAttempts)
	}

	q := db.New(pool)
	if _, replayErr := q.ReplayStuckOutboxMessage(ctx, db.ReplayStuckOutboxMessageParams{
		ID: mustMessageID(t, ctx, key),
		Retain: pgtype.Interval{
			Microseconds: int64(outbox.Retain / time.Microsecond), Valid: true,
		},
	}); replayErr != nil {
		t.Fatalf("replay: %v", replayErr)
	}

	var sent capturedMail
	goodNotifier := email.New(&sent, "https://goen.test", "", "")
	replayWorker := outbox.NewStore(pool, quiet())
	replayWorker.HandleJSON[email.OrderPaid](outbox.TopicOrderPaid, goodNotifier.SendOrderPaid)

	delivered, failed, err = replayWorker.Drain(ctx)
	if err != nil {
		t.Fatalf("replay drain: %v", err)
	}
	if delivered != 1 || failed != 0 {
		t.Fatalf("after replay delivered=%d failed=%d, want 1 and 0", delivered, failed)
	}
	if sent.to != "buyer@example.com" {
		t.Fatalf("sent to %q, want buyer@example.com", sent.to)
	}
}

type capturedMail struct {
	to string
}

func (c *capturedMail) Send(_ context.Context, m *email.Message) error {
	c.to = m.To
	return nil
}

func jsonEqual(a []byte, b string) bool {
	var left, right any
	if err := json.Unmarshal(a, &left); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &right); err != nil {
		return false
	}
	al, _ := json.Marshal(left)
	ar, _ := json.Marshal(right)
	return bytes.Equal(al, ar)
}

func mustMessageID(t *testing.T, ctx context.Context, dedupeKey string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT id FROM outbox_messages WHERE dedupe_key = $1`, dedupeKey).Scan(&id); err != nil {
		t.Fatalf("read message id: %v", err)
	}
	return id
}

// TestTheSweepBoundsRetentionOfUndeliveredStuckMessages verifies that terminal
// undelivered rows are swept after twice Retain from created_at, while fresh
// stuck messages younger than that window are kept.
func TestTheSweepBoundsRetentionOfUndeliveredStuckMessages(t *testing.T) {
	emptyOutbox(t)
	ctx := t.Context()
	s := outbox.NewStore(pool, quiet())

	enqueue(t, "sweep.stuck.aged", "stuck-aged", `{}`)
	enqueue(t, "sweep.stuck.recent", "stuck-recent", `{}`)
	enqueue(t, "sweep.delivered.old", "delivered-old", `{}`)
	enqueue(t, "sweep.pending.old", "pending-old", `{}`)

	// stuck.aged: blocked and created 61 days ago (> 2 * Retain) -> swept!
	if _, err := pool.Exec(ctx, `
		UPDATE outbox_messages
		SET attempts = $1, blocked_at = now() - interval '61 days',
		    created_at = now() - interval '61 days', available_at = now()
		WHERE dedupe_key = 'stuck-aged'`, outbox.MaxAttempts); err != nil {
		t.Fatalf("age stuck.aged: %v", err)
	}
	// stuck.recent: blocked, created 1 day ago (< 2 * Retain) -> kept!
	if _, err := pool.Exec(ctx, `
		UPDATE outbox_messages
		SET attempts = $1, blocked_at = now() - interval '1 day',
		    created_at = now() - interval '1 day', available_at = now()
		WHERE dedupe_key = 'stuck-recent'`, outbox.MaxAttempts); err != nil {
		t.Fatalf("age stuck.recent: %v", err)
	}
	// delivered.old: delivered 60 days ago (> Retain 30 days) -> swept!
	if _, err := pool.Exec(ctx, `
		UPDATE outbox_messages
		SET delivered_at = now() - interval '60 days'
		WHERE dedupe_key = 'delivered-old'`); err != nil {
		t.Fatalf("age delivered.old: %v", err)
	}
	// pending.old: never attempted, created 61 days ago -> swept after metadata window!
	if _, err := pool.Exec(ctx, `
		UPDATE outbox_messages
		SET created_at = now() - interval '61 days', available_at = now() - interval '61 days'
		WHERE dedupe_key = 'pending-old'`); err != nil {
		t.Fatalf("age pending.old: %v", err)
	}

	n, err := s.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 3 {
		t.Errorf("Sweep deleted %d rows, want 3 (stuck-aged, delivered-old, pending-old)", n)
	}

	for key, wantExists := range map[string]bool{
		"stuck-aged":    false,
		"delivered-old": false,
		"stuck-recent":  true,
		"pending-old":   false,
	} {
		var exists bool
		if scanErr := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM outbox_messages WHERE dedupe_key = $1)`,
			key).Scan(&exists); scanErr != nil {
			t.Fatalf("check %s: %v", key, scanErr)
		}
		if exists != wantExists {
			t.Errorf("message %s: exists=%v, want %v", key, exists, wantExists)
		}
	}
}
