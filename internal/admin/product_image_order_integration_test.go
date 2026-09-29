//go:build integration

package admin_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/product"
)

// imageOrderProduct is an active, buyable product with n images, oldest first.
func imageOrderProduct(t *testing.T, n int) (slug, token string, keys []string) {
	t.Helper()
	ctx := t.Context()
	token = "imgorder" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	slug = token + "-slug"
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO products (brand_id, category_id, slug, name, status)
 SELECT brand_id, category_id, $1, $2, 'draft' FROM products LIMIT 1 RETURNING id`, slug, token).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents) VALUES ($1, $2, 10000)`, id, "IMG-"+strings.ToUpper(token)); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		key := strings.Repeat(fmt.Sprintf("%x", i+1), 64)[:64]
		keys = append(keys, key)
		if _, err := pool.Exec(ctx, `INSERT INTO product_images (product_id, storage_key, alt_text, width, height, position) VALUES ($1, $2, '圖', 800, 800, $3)`, id, key, i*3); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET status = 'active', published_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.WithoutCancel(ctx), `DELETE FROM products WHERE id = $1`, id)
	})
	return slug, token, keys
}

func imageOrder(t *testing.T, slug string) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT pi.storage_key FROM product_images pi JOIN products p ON p.id = pi.product_id WHERE p.slug = $1 ORDER BY pi.position`, slug)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The first image is the cover on the product page and on every card.
func TestSettingTheCoverAndReorderingChangesTheFirstImageEverywhere(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug, token, keys := imageOrderProduct(t, 3)
	firstEverywhere := func(want string) {
		t.Helper()
		view, err := product.NewStore(pool).Load(ctx, slug, product.Selection{})
		if err != nil || len(view.Images) == 0 || !strings.Contains(view.Images[0].URL, want) {
			t.Fatalf("product page first image = %+v (err %v), want %s", view.Images, err, want)
		}
		found, err := catalog.NewStore(pool).Search(ctx, catalog.SearchPattern(token), 1)
		if err != nil || len(found.Products) != 1 || !strings.Contains(found.Products[0].ImageURL, want) {
			t.Fatalf("card image = %+v (err %v), want %s", found.Products, err, want)
		}
	}
	firstEverywhere(keys[0])

	if err := s.MoveImage(ctx, slug, keys[2], admin.MoveToCover); err != nil {
		t.Fatal(err)
	}
	if got := imageOrder(t, slug); strings.Join(got, ",") != strings.Join([]string{keys[2], keys[0], keys[1]}, ",") {
		t.Fatalf("after set-cover the order is %v", got)
	}
	firstEverywhere(keys[2])

	if err := s.MoveImage(ctx, slug, keys[2], admin.MoveDown); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveImage(ctx, slug, keys[1], admin.MoveUp); err != nil {
		t.Fatal(err)
	}
	if got := imageOrder(t, slug); strings.Join(got, ",") != strings.Join([]string{keys[0], keys[1], keys[2]}, ",") {
		t.Fatalf("after down and up the order is %v", got)
	}
	firstEverywhere(keys[0])

	var positions []int
	rows, err := pool.Query(ctx, `SELECT pi.position FROM product_images pi JOIN products p ON p.id = pi.product_id WHERE p.slug = $1 ORDER BY 1`, slug)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		positions = append(positions, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(positions) != "[0 1 2]" {
		t.Fatalf("positions are %v, want them renumbered 0..2", positions)
	}
	var audited int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'image.move' AND after->>'product' = $1`, slug).Scan(&audited); err != nil || audited != 3 {
		t.Fatalf("audit rows = %d (err %v), want 3", audited, err)
	}
}

func TestAStaleImageMoveIsRefusedWith422AndChangesNothing(t *testing.T) {
	ctx, _ := staffContext(t)
	ctx = i18n.WithLocale(ctx, i18n.En)
	slug, _, keys := imageOrderProduct(t, 2)
	h := adminHandlerOver(pool, admin.NewStore(pool, fakeRefunder{}, nil, nil))
	post := func(digest, move string) *httptest.ResponseRecorder {
		values := url.Values{"digest": {digest}, "move": {move}}
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/products/"+slug+"/images/move", strings.NewReader(values.Encode()))
		r.SetPathValue("slug", slug)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.MoveImage(w, r)
		return w
	}
	before := imageOrder(t, slug)
	for name, args := range map[string][2]string{
		"already the cover":   {keys[0], "cover"},
		"first cannot go up":  {keys[0], "up"},
		"last cannot go down": {keys[1], "down"},
		"image removed":       {strings.Repeat("f", 64), "cover"},
		"unknown move":        {keys[1], "sideways"},
	} {
		w := post(args[0], args[1])
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "image order changed") {
			t.Errorf("%s: status %d, want 422 with the notice", name, w.Code)
		}
	}
	if got := imageOrder(t, slug); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatalf("a refused move changed the order: %v -> %v", before, got)
	}
	if w := post(keys[1], "cover"); w.Code != http.StatusSeeOther {
		t.Fatalf("a valid move answered %d, want 303", w.Code)
	}
}

// Two reorders of one product queue on its row lock: neither computes from the
// order the other is about to replace, and no position is ever duplicated.
func TestConcurrentImageReordersNeverDuplicateAPosition(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug, _, keys := imageOrderProduct(t, 4)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.MoveImage(ctx, slug, keys[i%4], admin.MoveToCover)
			if err != nil && !errors.Is(err, admin.ErrInvalid) {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent reorder failed: %v", err)
	}
	var distinct, total int
	if err := pool.QueryRow(ctx, `SELECT count(DISTINCT pi.position), count(*) FROM product_images pi JOIN products p ON p.id = pi.product_id WHERE p.slug = $1`, slug).Scan(&distinct, &total); err != nil {
		t.Fatal(err)
	}
	if distinct != 4 || total != 4 {
		t.Fatalf("positions distinct=%d of %d images", distinct, total)
	}
}

// An attach computes max(position)+1 and a reorder renumbers every position; both
// take the product's lock first, so neither can collide with the other.
func TestConcurrentAttachAndReorderNeverCollideOnPosition(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug, _, keys := imageOrderProduct(t, 3)
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			digest := fmt.Sprintf("%064x", 0xd1500+i)
			if err := s.AttachImage(ctx, slug, digest, "新圖", "", 800, 800); err != nil {
				errs <- fmt.Errorf("attach: %w", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := s.MoveImage(ctx, slug, keys[i%3], admin.MoveToCover); err != nil && !errors.Is(err, admin.ErrInvalid) {
				errs <- fmt.Errorf("reorder: %w", err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var distinct, total int
	if err := pool.QueryRow(ctx, `SELECT count(DISTINCT pi.position), count(*) FROM product_images pi JOIN products p ON p.id = pi.product_id WHERE p.slug = $1`, slug).Scan(&distinct, &total); err != nil {
		t.Fatal(err)
	}
	if total != 11 || distinct != total {
		t.Fatalf("positions distinct=%d of %d images, want 11 of 11", distinct, total)
	}
}
