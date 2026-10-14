//go:build integration

package cart_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
)

// guestCartAt inserts a cart with every timestamp given, because an UPDATE would
// let the updated_at trigger move the clock back to now. ageDays is how long ago
// the cart was created and last updated; userID is nil for a guest.
func guestCartAt(t *testing.T, ageDays, updatedDays int, userID *uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO carts (token_hash, user_id, created_at, updated_at)
		VALUES (uuid_send(gen_random_uuid()), $1,
		        now() - make_interval(days => $2), now() - make_interval(days => $3))
		RETURNING id`, userID, ageDays, updatedDays).Scan(&id); err != nil {
		t.Fatalf("insert cart: %v", err)
	}
	return id
}

func lineAt(t *testing.T, cartID, variant uuid.UUID, addedDaysAgo int) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO cart_items (cart_id, variant_id, quantity, added_at)
		VALUES ($1, $2, 1, now() - make_interval(days => $3))`,
		cartID, variant, addedDaysAgo); err != nil {
		t.Fatalf("insert cart line: %v", err)
	}
}

func cartExists(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM carts WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count cart: %v", err)
	}
	return n == 1
}

func linesOf(t *testing.T, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM cart_items WHERE cart_id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count cart lines: %v", err)
	}
	return n
}

// The sweep runs as the store role, the one the worker's pool is, so a missing
// privilege shows here and not first in production.
func TestTheGuestCartSweepDeletesOnlyIdleGuestCarts(t *testing.T) {
	variant := variantOf(t, "koto-over-ear", true)
	var account uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email) VALUES ('sweep-'||gen_random_uuid()||'@example.com') RETURNING id`).Scan(&account); err != nil {
		t.Fatalf("create user: %v", err)
	}

	staleEmpty := guestCartAt(t, 31, 31, nil)
	staleWithLine := guestCartAt(t, 40, 40, nil)
	lineAt(t, staleWithLine, variant, 40)
	justInside := guestCartAt(t, 29, 29, nil)
	freshLine := guestCartAt(t, 40, 40, nil) // a line added yesterday keeps it
	lineAt(t, freshLine, variant, 1)
	freshUpdate := guestCartAt(t, 40, 1, nil) // a draft saved yesterday keeps it
	lineAt(t, freshUpdate, variant, 40)
	accountCart := guestCartAt(t, 400, 400, &account) // an account's cart is never swept

	n, err := cart.NewStore(storeRolePool(t)).SweepGuestCarts(t.Context())
	if err != nil {
		t.Fatalf("SweepGuestCarts: %v", err)
	}
	if n < 2 {
		t.Errorf("deleted %d carts, want at least the 2 stale ones", n)
	}

	for _, c := range []struct {
		name string
		id   uuid.UUID
		keep bool
	}{
		{"idle 31 days, no lines", staleEmpty, false},
		{"idle 40 days, one old line", staleWithLine, false},
		{"29 days old", justInside, true},
		{"old cart with a line added yesterday", freshLine, true},
		{"old cart updated yesterday", freshUpdate, true},
		{"an account's 400-day-old cart", accountCart, true},
	} {
		if got := cartExists(t, c.id); got != c.keep {
			t.Errorf("%s: cart exists = %v, want %v", c.name, got, c.keep)
		}
	}
	if got := linesOf(t, staleWithLine); got != 0 {
		t.Errorf("lines of a deleted cart = %d, want 0 (cascade)", got)
	}
	if got := linesOf(t, freshLine); got != 1 {
		t.Errorf("lines of a kept cart = %d, want 1", got)
	}
}

// More idle carts than one batch must all go in one SweepGuestCarts call.
func TestTheGuestCartSweepDrainsMoreThanOneBatch(t *testing.T) {
	const marker = "sweep-batch"
	count := 2*cart.GuestCartSweepBatch + 1
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO carts (token_hash, created_at, updated_at, checkout_draft, checkout_draft_at)
		SELECT uuid_send(gen_random_uuid()), now() - interval '90 days', now() - interval '90 days',
		       jsonb_build_object('marker', $2::text), now() - interval '90 days'
		FROM generate_series(1, $1)`, count, marker); err != nil {
		t.Fatalf("insert idle carts: %v", err)
	}

	n, err := cart.NewStore(storeRolePool(t)).SweepGuestCarts(t.Context())
	if err != nil {
		t.Fatalf("SweepGuestCarts: %v", err)
	}
	if n < int64(count) {
		t.Errorf("deleted %d carts, want at least %d", n, count)
	}
	var left int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM carts WHERE checkout_draft->>'marker' = $1`, marker).Scan(&left); err != nil {
		t.Fatalf("count leftovers: %v", err)
	}
	if left != 0 {
		t.Errorf("%d idle carts survived the sweep", left)
	}
}
