//go:build integration

package site

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
)

// pool is the suite's database: the migrated schema and the development
// catalogue, which the chrome's tests read as the shop.
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

// TestStore_BannerDismissal: dismissing one banner does not silence the next.
func TestStore_BannerDismissal(t *testing.T) {
	ctx := t.Context()
	s := NewStore(pool)

	if _, err := pool.Exec(ctx, `DELETE FROM promo_banners`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	first := seedBanner(t, "第一檔")

	shown, err := s.Banner(ctx, "")
	if err != nil {
		t.Fatalf("banner: %v", err)
	}
	if !shown.Shown() || shown.Message != "第一檔" {
		t.Fatalf("the running banner did not show: %+v", shown)
	}

	dismissed := dismissDigest(first)
	hidden, hiddenErr := s.Banner(ctx, dismissed)
	if hiddenErr != nil {
		t.Fatalf("banner: %v", hiddenErr)
	}
	if hidden.Shown() {
		t.Error("a dismissed banner is still showing")
	}

	if _, err := pool.Exec(ctx, `UPDATE promo_banners SET is_active = false`); err != nil {
		t.Fatalf("retire: %v", err)
	}
	seedBanner(t, "第二檔")

	next, nextErr := s.Banner(ctx, dismissed)
	if nextErr != nil {
		t.Fatalf("banner: %v", nextErr)
	}
	if !next.Shown() {
		t.Error("the NEXT promotion is hidden by a dismissal of the previous one")
	}
	if next.Message != "第二檔" {
		t.Errorf("showing %q, want 第二檔", next.Message)
	}
}

// TestStore_BannerWindow: a banner outside its window, or switched off, does
// not show.
func TestStore_BannerWindow(t *testing.T) {
	ctx := t.Context()
	s := NewStore(pool)

	tests := []struct {
		name  string
		setup string
		want  bool
	}{
		{"running", `UPDATE promo_banners SET is_active = true, starts_at = NULL, ends_at = NULL`, true},
		{"switched off", `UPDATE promo_banners SET is_active = false`, false},
		{"ended", `UPDATE promo_banners SET is_active = true,
			starts_at = now() - interval '30 days', ends_at = now() - interval '1 day'`, false},
		{"not started", `UPDATE promo_banners SET is_active = true,
			starts_at = now() + interval '1 day', ends_at = NULL`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `DELETE FROM promo_banners`); err != nil {
				t.Fatalf("clear: %v", err)
			}
			seedBanner(t, "檔期測試")
			if _, err := pool.Exec(ctx, tt.setup); err != nil {
				t.Fatalf("setup: %v", err)
			}
			banner, err := s.Banner(ctx, "")
			if err != nil {
				t.Fatalf("banner: %v", err)
			}
			if banner.Shown() != tt.want {
				t.Errorf("shown=%v, want %v", banner.Shown(), tt.want)
			}
		})
	}
}

// TestStore_BannerOffSiteCTA: an off-site call to action is dropped, not
// rendered.
func TestStore_BannerOffSiteCTA(t *testing.T) {
	ctx := t.Context()
	s := NewStore(pool)

	for _, href := range []string{
		"https://evil.example", "//evil.example/x", "///evil.example/x",
		"javascript:alert(1)", `/\evil.example`,
	} {
		if _, err := pool.Exec(ctx, `DELETE FROM promo_banners`); err != nil {
			t.Fatalf("clear: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO promo_banners (message, cta_label, cta_href)
			VALUES ('測試', '點我', $1)`, href); err != nil {
			t.Fatalf("insert %q: %v", href, err)
		}
		banner, err := s.Banner(ctx, "")
		if err != nil {
			t.Fatalf("banner: %v", err)
		}
		if banner.HasCTA() {
			t.Errorf("an off-site CTA %q was rendered as %q", href, banner.CTAHref)
		}
		if !banner.Shown() {
			t.Errorf("a bad CTA hid the whole banner")
		}
	}

	if _, err := pool.Exec(ctx, `DELETE FROM promo_banners`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO promo_banners (message, cta_label, cta_href)
		VALUES ('測試', '看優惠', '/deals')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	banner, err := s.Banner(ctx, "")
	if err != nil {
		t.Fatalf("banner: %v", err)
	}
	if !banner.HasCTA() || banner.CTAHref != "/deals" {
		t.Errorf("a same-site CTA came back as %q", banner.CTAHref)
	}
}

func seedBanner(t *testing.T, message string) string {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO promo_banners (message, message_short)
		VALUES ($1, $1) RETURNING id`, message).Scan(&id); err != nil {
		t.Fatalf("seed banner: %v", err)
	}
	return id.String()
}

// TestStore_NavPanelProducts: a department's header panel shows at most three
// of its own products, newest first, and only ones that can be bought: the
// header is on every page, and a sold-out product there is a click that ends at
// a disabled button. The newest product of the department is sold out for the
// length of the test, so the rule is exercised by a row it actually removes.
func TestStore_NavPanelProducts(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	const dept = "tech"
	var newest string
	if err := pool.QueryRow(ctx, `
		WITH RECURSIVE tree AS (
		    SELECT id FROM categories WHERE slug = $1
		    UNION ALL
		    SELECT k.id FROM categories k JOIN tree t ON k.parent_id = t.id
		)
		SELECT p.slug FROM products p
		WHERE p.category_id IN (SELECT id FROM tree) AND p.status = 'active'
		ORDER BY p.published_at DESC, p.id DESC LIMIT 1`, dept).Scan(&newest); err != nil {
		t.Fatalf("fixture: the newest %s product: %v", dept, err)
	}
	// Remembered in Go rather than a temporary table: a pool hands each
	// statement whichever connection is free, and a temp table lives on one.
	var ids []uuid.UUID
	var stock []int32
	if err := pool.QueryRow(ctx, `
		SELECT array_agg(v.id), array_agg(v.stock_quantity) FROM product_variants v
		JOIN products p ON p.id = v.product_id WHERE p.slug = $1`, newest).Scan(&ids, &stock); err != nil {
		t.Fatalf("fixture: remember the stock: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.WithoutCancel(ctx), `
			UPDATE product_variants v SET stock_quantity = s.stock
			FROM unnest($1::uuid[], $2::integer[]) AS s(id, stock) WHERE s.id = v.id`, ids, stock); err != nil {
			t.Errorf("restore the stock: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, `
		UPDATE product_variants SET stock_quantity = safety_stock WHERE id = ANY($1::uuid[])`, ids); err != nil {
		t.Fatalf("fixture: sell out %s: %v", newest, err)
	}

	nav, err := NewStore(pool).Nav(ctx)
	if err != nil {
		t.Fatalf("read the header: %v", err)
	}
	shown := 0
	for i := range nav {
		d := &nav[i]
		rows, err := pool.Query(ctx, `
			WITH RECURSIVE tree AS (
			    SELECT id FROM categories WHERE slug = $1
			    UNION ALL
			    SELECT k.id FROM categories k JOIN tree t ON k.parent_id = t.id
			)
			SELECT p.slug FROM products p
			WHERE p.category_id IN (SELECT id FROM tree) AND p.status = 'active'
			  AND EXISTS (
			      SELECT 1 FROM product_variants v
			      WHERE v.product_id = p.id AND v.is_active AND v.stock_quantity > v.safety_stock)
			ORDER BY p.published_at DESC, p.id DESC LIMIT 3`, d.Slug)
		if err != nil {
			t.Fatalf("read %s's products: %v", d.Slug, err)
		}
		want, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("read %s's products: %v", d.Slug, err)
		}
		got := make([]string, 0, len(d.Picks))
		for _, p := range d.Picks {
			got = append(got, p.Slug)
			if p.Slug == newest {
				t.Errorf("%s's panel offers %s, which is sold out", d.Slug, newest)
			}
			if !strings.HasPrefix(p.Price, "NT$") {
				t.Errorf("%s's panel prices %s as %q", d.Slug, p.Slug, p.Price)
			}
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s's panel shows %v, want %v", d.Slug, got, want)
		}
		shown += len(got)
	}
	if shown == 0 {
		t.Fatal("no department showed a product, so this compared nothing")
	}
}

// TestStore_NavCounts: the nav counts each department's active products.
func TestStore_NavCounts(t *testing.T) {
	ctx := t.Context()
	var big, small, sub uuid.UUID
	for _, c := range []struct {
		id     *uuid.UUID
		slug   string
		parent *uuid.UUID
	}{
		{&big, "navcount-big", nil},
		{&small, "navcount-small", nil},
		{&sub, "navcount-big-sub", &big},
	} {
		var parent any
		if c.parent != nil {
			parent = *c.parent
		}
		if err := pool.QueryRow(ctx, `
			INSERT INTO categories (slug, name, parent_id, position)
			VALUES ($1, $1, $2, (SELECT coalesce(max(position), -1) + 1 FROM categories WHERE parent_id IS NOT DISTINCT FROM $2))
			RETURNING id`, c.slug, parent).Scan(c.id); err != nil {
			t.Fatalf("insert category %s: %v", c.slug, err)
		}
	}
	slugs := []string{"navcount-a", "navcount-b", "navcount-c", "navcount-d"}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		// Archived first: an active product may not lose its last variant.
		for _, q := range []string{
			`UPDATE products SET status = 'archived' WHERE slug = ANY($1)`,
			`DELETE FROM product_variants WHERE product_id IN (SELECT id FROM products WHERE slug = ANY($1))`,
			`DELETE FROM products WHERE slug = ANY($1)`,
		} {
			if _, err := pool.Exec(clean, q, slugs); err != nil {
				t.Errorf("clean up: %v", err)
			}
		}
		if _, err := pool.Exec(clean, `DELETE FROM categories WHERE id IN ($1, $2, $3)`, sub, big, small); err != nil {
			t.Errorf("clean up categories: %v", err)
		}
	})
	for _, p := range []struct {
		slug, status string
		category     uuid.UUID
	}{
		{slugs[0], "active", big},
		{slugs[1], "active", sub},
		{slugs[2], "draft", big},
		{slugs[3], "active", small},
	} {
		if _, err := pool.Exec(ctx, `
			WITH p AS (
			    INSERT INTO products (category_id, slug, name, status, published_at)
			    VALUES ($1, $2, $2, $3, now())
			    RETURNING id
			)
			INSERT INTO product_variants (product_id, sku, price_cents, stock_quantity, safety_stock, position)
			SELECT p.id, upper($2), 1000, 5, 0, 0 FROM p`,
			p.category, p.slug, p.status); err != nil {
			t.Fatalf("insert product %s: %v", p.slug, err)
		}
	}

	items, err := NewStore(pool).Nav(i18n.WithLocale(ctx, i18n.ZhHant))
	if err != nil {
		t.Fatalf("nav: %v", err)
	}
	got := map[string]int{}
	for _, n := range items {
		got[n.Slug] = n.ProductCount
	}
	if got["navcount-big"] != 2 || got["navcount-small"] != 1 {
		t.Errorf("department counts = big %d, small %d; want 2 (one in a sub-category, the draft left out) and 1",
			got["navcount-big"], got["navcount-small"])
	}
}
