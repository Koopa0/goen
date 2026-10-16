//go:build integration

package cart_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/cart"
)

func TestCartIsFoundByTokenNotByID(t *testing.T) {
	s := cart.NewStore(pool)
	tok := bearer.New()
	id, err := s.Create(t.Context(), tok, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.ByToken(t.Context(), tok, uuid.NullUUID{})
	if err != nil || got != id {
		t.Fatalf("ByToken(token) = %v/%v, want %v", got, err, id)
	}
	if _, err := s.ByToken(t.Context(), tok+"x", uuid.NullUUID{}); err == nil {
		t.Error("a near-miss token found a cart")
	}
	if _, err := s.ByToken(t.Context(), "", uuid.NullUUID{}); err == nil {
		t.Error("an empty token found a cart")
	}

	var stored []byte
	if err := pool.QueryRow(t.Context(),
		`SELECT digest FROM carts WHERE id = $1`, id).Scan(&stored); err != nil {
		t.Fatalf("read digest: %v", err)
	}
	if string(stored) == tok {
		t.Error("the cart table stores the raw token; a leak would hand over live cart cookies")
	}
}
