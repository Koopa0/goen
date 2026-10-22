//go:build integration

package wishlist_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/wishlist"
)

// TestStore_ItemsRemoveOwnerScope: a saved list is personal data, so one
// customer's list never shows another's products, and removing a product takes
// it off the remover's list only.
func TestStore_ItemsRemoveOwnerScope(t *testing.T) {
	ctx := t.Context()
	s := wishlist.NewStore(pool)
	alice := register(t, "wish-alice-"+uuid.NewString()+"@example.com")
	bob := register(t, "wish-bob-"+uuid.NewString()+"@example.com")
	alicesSlug, bobsSlug := ownSlug(t, "alice"), ownSlug(t, "bob")

	if err := s.Save(ctx, alice.ID, alicesSlug); err != nil {
		t.Fatalf("Save(alice): %v", err)
	}
	if err := s.Save(ctx, bob.ID, bobsSlug); err != nil {
		t.Fatalf("Save(bob): %v", err)
	}
	if got := slugs(t, s, alice.ID); len(got) != 1 || got[0] != alicesSlug {
		t.Errorf("alice's wishlist = %v, want only %s", got, alicesSlug)
	}

	if err := s.Remove(ctx, bob.ID, alicesSlug); err != nil {
		t.Fatalf("Remove(bob, alice's product): %v", err)
	}
	if got := slugs(t, s, alice.ID); len(got) != 1 || got[0] != alicesSlug {
		t.Errorf("after bob removed %s, alice's wishlist = %v, want it kept", alicesSlug, got)
	}
}

func ownSlug(t *testing.T, name string) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(),
		`SELECT p.slug FROM products p JOIN product_variants v ON v.product_id = p.id WHERE v.id = $1`,
		ownVariant(t, name)).Scan(&slug); err != nil {
		t.Fatalf("read the fixture product: %v", err)
	}
	return slug
}

func slugs(t *testing.T, s *wishlist.Store, userID string) []string {
	t.Helper()
	items, err := s.Items(t.Context(), userID)
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	out := make([]string, 0, len(items))
	for i := range items {
		out = append(out, items[i].Slug)
	}
	return out
}
