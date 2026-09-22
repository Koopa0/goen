//go:build integration

package outbox_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/outbox"
)

func TestExpiredMailCannotLeaveBeforeTheFirstSweep(t *testing.T) {
	for _, age := range []string{"31 days", "30 days", "29 days"} {
		t.Run(age, func(t *testing.T) {
			emptyOutbox(t)
			key := enqueueReceipt(t)
			if _, err := pool.Exec(t.Context(), `UPDATE outbox_messages SET created_at = now() - $1::interval WHERE dedupe_key = $2`, age, key); err != nil {
				t.Fatal(err)
			}
			var sent capturedMail
			n := email.New(&sent, "https://goen.test", "", "")
			worker := outbox.NewStore(pool, quiet())
			worker.HandleJSON[email.OrderPaid](outbox.TopicOrderPaid, n.SendOrderPaid)
			delivered, failed, err := worker.Drain(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if age == "29 days" {
				want = 1
			}
			if delivered != want || failed != 0 || (sent.to != "") != (want == 1) {
				t.Errorf("before any sweep: delivered=%d failed=%d recipient=%q; want delivered=%d failed=0", delivered, failed, sent.to, want)
			}
			if want == 0 {
				var redacted, blocked, marked bool
				if err = pool.QueryRow(t.Context(), `SELECT payload = '{}'::jsonb, blocked_at IS NOT NULL, delivered_at IS NOT NULL FROM outbox_messages WHERE dedupe_key = $1`, key).Scan(&redacted, &blocked, &marked); err != nil {
					t.Fatal(err)
				}
				if !redacted || !blocked || marked {
					t.Errorf("expired row: redacted=%t blocked=%t delivered=%t", redacted, blocked, marked)
				}
			}
		})
	}
}

func TestABatchRechecksRetentionBeforeEachDelivery(t *testing.T) {
	emptyOutbox(t)
	for range 2 {
		enqueueReceipt(t)
	}
	var sent capturedMail
	n := email.New(&sent, "https://goen.test", "", "")
	worker := outbox.NewStore(pool, quiet())
	calls := 0
	worker.HandleJSON[email.OrderPaid](outbox.TopicOrderPaid, func(ctx context.Context, message *email.OrderPaid) error {
		calls++
		// Move the unstarted receipt across the deadline while its sibling owns
		// the worker, independently of the UPDATE's unspecified return order.
		if _, err := pool.Exec(ctx, `UPDATE outbox_messages SET created_at = now() - interval '31 days' WHERE dedupe_key <> $1`, message.OrderNumber); err != nil {
			return err
		}
		return n.SendOrderPaid(ctx, message)
	})
	delivered, failed, err := worker.Drain(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if delivered != 1 || failed != 1 || calls != 1 || sent.to == "" {
		t.Fatalf("batch crossed retention: delivered=%d failed=%d handler calls=%d recipient=%q; want 1/1/1 and the fresh receipt", delivered, failed, calls, sent.to)
	}
	var expired, deliveredRows int
	if err = pool.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE payload = '{}'::jsonb AND blocked_at IS NOT NULL AND delivered_at IS NULL), count(*) FILTER (WHERE delivered_at IS NOT NULL) FROM outbox_messages`).Scan(&expired, &deliveredRows); err != nil {
		t.Fatal(err)
	}
	if expired != 1 || deliveredRows != 1 {
		t.Errorf("terminal rows: expired=%d delivered=%d, want 1/1", expired, deliveredRows)
	}
}

func enqueueReceipt(t *testing.T) string {
	t.Helper()
	key := uuid.NewString()
	payload, err := json.Marshal(email.OrderPaid{Locale: "en", OrderNumber: key, Email: "buyer@example.com", Name: "Alex", AmountCents: 123400})
	if err != nil {
		t.Fatal(err)
	}
	enqueue(t, outbox.TopicOrderPaid, key, string(payload))
	return key
}

func TestClaimAndRedactionAgreeAtTheRetentionBoundary(t *testing.T) {
	for _, offset := range []int64{-1, 0, 1} {
		t.Run(time.Duration(offset*int64(time.Microsecond)).String(), func(t *testing.T) {
			emptyOutbox(t)
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
			if _, err = tx.Exec(t.Context(), `INSERT INTO outbox_messages (topic, dedupe_key, payload, created_at) VALUES ('boundary', $1, '{"n":1}', now() - interval '30 days' + $2 * interval '1 microsecond')`, uuid.NewString(), offset); err != nil {
				t.Fatal(err)
			}
			q := db.New(tx)
			retain := pgtype.Interval{Microseconds: outbox.Retain.Microseconds(), Valid: true}
			rows, err := q.ClaimOutbox(t.Context(), db.ClaimOutboxParams{BatchSize: outbox.BatchSize, Lease: pgtype.Interval{Microseconds: outbox.Lease.Microseconds(), Valid: true}, Retain: retain})
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if offset > 0 {
				want = 1
			}
			if len(rows) != want {
				t.Errorf("claimed %d at offset %d microseconds; want %d", len(rows), offset, want)
			}
			expired, err := q.ExpireOutboxPayloads(t.Context(), retain)
			if err != nil {
				t.Fatal(err)
			}
			if expired != int64(1-want) {
				t.Errorf("redacted %d at offset %d microseconds; want %d", expired, offset, 1-want)
			}
		})
	}
}

func TestTheSendBudgetCannotOutlivePayloadRetention(t *testing.T) {
	emptyOutbox(t)
	key := enqueueReceipt(t)
	if _, err := pool.Exec(t.Context(), `UPDATE outbox_messages SET created_at = now() - interval '30 days' + interval '10 seconds' WHERE dedupe_key = $1`, key); err != nil {
		t.Fatal(err)
	}
	worker := outbox.NewStore(pool, quiet())
	called := false
	worker.HandleJSON[email.OrderPaid](outbox.TopicOrderPaid, func(ctx context.Context, _ *email.OrderPaid) error {
		called = true
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 10*time.Second || time.Until(deadline) <= 0 {
			t.Errorf("delivery deadline = %v, present=%t; want the remaining payload lifetime", deadline, ok)
		}
		return nil
	})
	if delivered, failed, err := worker.Drain(t.Context()); err != nil || delivered != 1 || failed != 0 || !called {
		t.Fatalf("near-deadline receipt = %d/%d/%v called=%t", delivered, failed, err, called)
	}
}
