//go:build integration

package wishlist_test

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/user"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p

	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		slog.Error("read seed", "error", err)
		os.Exit(1)
	}
	if _, err := pool.Exec(context.Background(), string(seed)); err != nil {
		slog.Error("load seed", "error", err)
		os.Exit(1)
	}

	code := m.Run()
	stop()
	os.Exit(code)
}

// register makes an account the way the registration form does, up to the
// mailed link: the account exists and holds the password, and its address is
// unproved. The queued follow-up is marked delivered, because a fixture has no
// worker to send it. The password is hashed at the cost production spends:
// only auth's own test binary may lower it, and these pages never read it.
func register(t *testing.T, email string) user.User {
	t.Helper()
	if err := auth.NewStore(pool).Register(t.Context(), &auth.Credentials{
		Email: email, Password: "a sufficiently long password", Name: "測試",
	}, "/account"); err != nil {
		t.Fatalf("register %s: %v", email, err)
	}
	var id uuid.UUID
	var u user.User
	if err := pool.QueryRow(t.Context(), `
		SELECT id, email, coalesce(full_name, ''), role
		FROM users WHERE lower(email) = lower($1)`, email).
		Scan(&id, &u.Email, &u.Name, &u.Role); err != nil {
		t.Fatalf("read the account registered for %s: %v", email, err)
	}
	u.ID = id.String()
	if _, err := pool.Exec(t.Context(), `
		UPDATE outbox_messages SET delivered_at = now()
		WHERE topic = $1 AND payload->>'user_id' = $2 AND delivered_at IS NULL`,
		outbox.TopicRegistration.Name(), u.ID); err != nil {
		t.Fatalf("settle the registration follow-up for %s: %v", email, err)
	}
	return u
}

// ownVariant is a published product of the calling test's own with one variant
// in stock. The suite shuffles, and orders held on a shared variant take the
// stock other tests count on.
func ownVariant(t *testing.T, name string) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	slug := name + "-" + uuid.NewString()[:8]
	var productID, variantID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, $1, $1, 'draft', NULL
		FROM brands b, categories c
		ORDER BY b.id, c.id LIMIT 1
		RETURNING id`, slug).Scan(&productID); err != nil {
		t.Fatalf("create product %s: %v", slug, err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, is_active, position)
		VALUES ($1, $2, 199900, true, 0) RETURNING id`,
		productID, strings.ToUpper(slug)).Scan(&variantID); err != nil {
		t.Fatalf("create variant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT record_inventory_movement($1, 10, 'adjustment', $2, 'admin', NULL, NULL)`,
		variantID, "fixture:"+variantID.String()); err != nil {
		t.Fatalf("stock the variant: %v", err)
	}
	// Published only now: products_active_has_variant is deferred and fires from
	// both sides.
	if _, err := pool.Exec(ctx,
		`UPDATE products SET status = 'active', published_at = now() WHERE id = $1`, productID); err != nil {
		t.Fatalf("publish %s: %v", slug, err)
	}
	return variantID
}
