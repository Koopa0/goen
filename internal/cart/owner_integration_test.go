//go:build integration

package cart_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
)

// TestACartTokenReachesAnOwnedCartOnlyForItsOwner: adoption keeps the token the
// browser already holds, so the token alone must not reach an account's cart.
// ErrNotYourCart, not ErrNotFound, is what tells the handler the cookie is stale.
func TestACartTokenReachesAnOwnedCartOnlyForItsOwner(t *testing.T) {
	s := cart.NewStore(pool)
	user := func(label string) uuid.NullUUID {
		var id uuid.UUID
		if err := pool.QueryRow(t.Context(), `INSERT INTO users (email) VALUES ($1) RETURNING id`,
			label+"-"+uuid.NewString()+"@example.com").Scan(&id); err != nil {
			t.Fatalf("create %s: %v", label, err)
		}
		return uuid.NullUUID{UUID: id, Valid: true}
	}
	owner, stranger := user("cart-owner"), user("cart-stranger")
	nobody := uuid.NullUUID{}

	guestID, guestToken := newCartSession(t, s)
	ownedToken, err := cart.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	ownedID, err := s.Create(t.Context(), ownedToken, owner)
	if err != nil {
		t.Fatalf("create the owned cart: %v", err)
	}

	for _, tt := range []struct {
		name      string
		token     string
		requester uuid.NullUUID
		want      uuid.UUID
		wantErr   error
	}{
		{"a guest cart, signed out", guestToken, nobody, guestID, nil},
		{"a guest cart, signed in", guestToken, stranger, guestID, nil},
		{"an owned cart, its owner", ownedToken, owner, ownedID, nil},
		{"an owned cart, signed out", ownedToken, nobody, uuid.Nil, cart.ErrNotYourCart},
		{"an owned cart, another account", ownedToken, stranger, uuid.Nil, cart.ErrNotYourCart},
		{"no such cart", ownedToken + "x", owner, uuid.Nil, cart.ErrNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.ByToken(t.Context(), tt.token, tt.requester)
			if got != tt.want || !errors.Is(err, tt.wantErr) {
				t.Errorf("ByToken = %s/%v, want %s/%v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
