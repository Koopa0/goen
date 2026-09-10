package outbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"
)

type testPayload struct {
	Count int `json:"count"`
}

func TestHandleJSONDecodesForTypedHandler(t *testing.T) {
	t.Parallel()

	store := &Store{handlers: map[string]Handler{}}
	var got testPayload
	store.HandleJSON[testPayload]("test.typed", func(_ context.Context, payload *testPayload) error {
		got = *payload
		return nil
	})

	if err := store.handlers["test.typed"](t.Context(), []byte(`{"count":7,"new_field":true}`)); err != nil {
		t.Fatalf("handle typed payload: %v", err)
	}
	if got.Count != 7 {
		t.Errorf("decoded count = %d, want 7", got.Count)
	}
}

func TestHandleJSONNamesDecodeFailure(t *testing.T) {
	t.Parallel()

	store := &Store{handlers: map[string]Handler{}}
	store.HandleJSON[testPayload]("test.typed", func(context.Context, *testPayload) error { return nil })

	err := store.handlers["test.typed"](t.Context(), []byte(`{"count":"not a number"}`))
	if err == nil || !strings.Contains(err.Error(), "test.typed") {
		t.Errorf("decode error = %v, want it to name the topic", err)
	}
}

func TestHandleRejectsInvalidRegistration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		register func(*Store)
	}{
		{
			name: "empty topic",
			register: func(store *Store) {
				store.Handle("", func(context.Context, []byte) error { return nil })
			},
		},
		{
			name: "nil raw handler",
			register: func(store *Store) {
				store.Handle("test.raw", nil)
			},
		},
		{
			name: "nil JSON handler",
			register: func(store *Store) {
				store.HandleJSON[testPayload]("test.typed", nil)
			},
		},
		{
			name: "duplicate topic",
			register: func(store *Store) {
				store.Handle("test.raw", func(context.Context, []byte) error { return nil })
				store.Handle("test.raw", func(context.Context, []byte) error { return nil })
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &Store{handlers: map[string]Handler{}}
			defer func() {
				if recover() == nil {
					t.Error("registration did not panic")
				}
			}()
			tt.register(store)
		})
	}
}

// TestALastErrorIsCutOnARuneBoundary. last_error is a PostgreSQL text column and
// PostgreSQL refuses a byte sequence that is not valid UTF-8:
//
//	ERROR: invalid byte sequence for encoding "UTF8": 0xe5 0xae
//
// The write that would fail is the one recording the failure: attempts never
// increments, so the message retries for ever and never reaches MaxAttempts.
func TestALastErrorIsCutOnARuneBoundary(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		in    string
		limit int
	}{
		{name: "cut lands mid-rune", in: strings.Repeat("客", 40), limit: 50},
		{name: "cut lands one byte into a rune", in: "a" + strings.Repeat("客", 40), limit: 50},
		{name: "cut lands two bytes into a rune", in: "ab" + strings.Repeat("客", 40), limit: 50},
		{name: "mixed script", in: strings.Repeat("order 訂單 ", 80), limit: 500},
		{name: "the production bound", in: strings.Repeat("庫存不足", 200), limit: 500},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := truncate(tt.in, tt.limit)
			if !utf8.ValidString(got) {
				t.Errorf("truncate(%d runes, %d) produced invalid UTF-8; "+
					"PostgreSQL refuses this and the reschedule write is lost",
					utf8.RuneCountInString(tt.in), tt.limit)
			}
			if len(got) > tt.limit {
				t.Errorf("truncate returned %d bytes, over the %d-byte bound",
					len(got), tt.limit)
			}
		})
	}

	if got := truncate(strings.Repeat("客", 40), 50); len(got) == len(strings.Repeat("客", 40)) {
		t.Error("truncate returned the input unshortened")
	}
	if got := truncate("short", 50); got != "short" {
		t.Errorf("truncate(%q) = %q, want it unchanged", "short", got)
	}
}

// TestAHangingHandlerIsCutOffAtItsBudget. One claim covers [BatchSize] messages
// and stays exclusive for [Lease], so a handler that hangs until the lease
// expires hands the rest of its own batch to a second replica.
func TestAHangingHandlerIsCutOffAtItsBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// The fallback is what an unbounded handler looks like from here: it runs
		// past the lease instead of blocking the bubble for ever.
		hang := func(ctx context.Context, _ []byte) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * Lease):
				return nil
			}
		}

		start := time.Now()
		err := runHandler(t.Context(), hang, nil)

		if got := time.Since(start); got != HandlerBudget {
			t.Errorf("a hanging handler ran for %v, want %v — the claim it holds "+
				"expires after %v", got, HandlerBudget, Lease)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("runHandler(hanging handler) = %v, want a deadline error — a "+
				"message is rescheduled on what its handler returns", err)
		}
	})
}
