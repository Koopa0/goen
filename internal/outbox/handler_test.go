package outbox

import (
	"context"
	"strings"
	"testing"
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
