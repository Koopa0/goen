//go:build integration

package products_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/admin/products/productstest"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
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
		// The upload product_images_media_digest_fk requires. Shared by every
		// product this makes, so it is kept rather than cleaned up.
		if _, err := pool.Exec(ctx, `INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size) VALUES ($1, 'image/png', '\x00', 800, 800, 1) ON CONFLICT (digest) DO NOTHING`, key); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO product_images (product_id, media_digest, alt_text, width, height, position) VALUES ($1, $2, '圖', 800, 800, $3)`, id, key, i*3); err != nil {
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
	rows, err := pool.Query(t.Context(), `SELECT coalesce(pi.media_digest, pi.bundled_key) FROM product_images pi JOIN products p ON p.id = pi.product_id WHERE p.slug = $1 ORDER BY pi.position`, slug)
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

// TestStore_BundledImageEdits: a seeded photograph lives in
// bundled_key, not media_digest, and the editor still names it by its one key.
// Run as the admin role, so the column grants are the ones production has.
func TestStore_BundledImageEdits(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(admintest.AdminRolePool(t, pool))
	slug, _, _ := imageOrderProduct(t, 0)
	const front, back = "pixelight-9-pro-01.webp", "pixelight-9-pro-02.webp"
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_images (product_id, bundled_key, alt_text, width, height, position)
		SELECT p.id, k.key, '隨附照片', 1600, 1200, k.position
		FROM products p, (VALUES ($2::text, 0), ($3::text, 1)) AS k(key, position)
		WHERE p.slug = $1`, slug, front, back); err != nil {
		t.Fatal(err)
	}

	if err := s.MoveImage(ctx, slug, back, products.MoveToCover); err != nil {
		t.Fatalf("move a bundled photograph: %v", err)
	}
	if got := imageOrder(t, slug); strings.Join(got, ",") != back+","+front {
		t.Fatalf("after set-cover the order is %v, want %s first", got, back)
	}
	if err := s.SetImageOption(ctx, slug, back, ""); err != nil {
		t.Fatalf("tag a bundled photograph: %v", err)
	}
	if err := s.DetachImage(ctx, slug, front); err != nil {
		t.Fatalf("remove a bundled photograph: %v", err)
	}
	if got := imageOrder(t, slug); strings.Join(got, ",") != back {
		t.Fatalf("after removing %s the images are %v", front, got)
	}
}

// TestStore_MoveImageCoverEverywhere: setting the cover and reordering changes
// the first image everywhere.
//
// With no photograph tagged, the first image is the cover on the product page and
// on every card.
func TestStore_MoveImageCoverEverywhere(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug, token, keys := imageOrderProduct(t, 3)
	firstEverywhere := func(want string) {
		t.Helper()
		view, err := catalog.NewStore(pool, slog.New(slog.DiscardHandler)).Product(ctx, slug, catalog.Selection{})
		if err != nil || len(view.Images) == 0 || !strings.Contains(view.Images[0].URL, want) {
			t.Fatalf("product page first image = %+v (err %v), want %s", view.Images, err, want)
		}
		found, err := catalog.NewStore(pool, slog.New(slog.DiscardHandler)).Search(ctx, catalog.SearchPattern(token), catalog.SortRelevance, 1)
		if err != nil || len(found.Products) != 1 || !strings.Contains(found.Products[0].ImageURL, want) {
			t.Fatalf("card image = %+v (err %v), want %s", found.Products, err, want)
		}
	}
	firstEverywhere(keys[0])

	if err := s.MoveImage(ctx, slug, keys[2], products.MoveToCover); err != nil {
		t.Fatal(err)
	}
	if got := imageOrder(t, slug); strings.Join(got, ",") != strings.Join([]string{keys[2], keys[0], keys[1]}, ",") {
		t.Fatalf("after set-cover the order is %v", got)
	}
	firstEverywhere(keys[2])

	if err := s.MoveImage(ctx, slug, keys[2], products.MoveDown); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveImage(ctx, slug, keys[1], products.MoveUp); err != nil {
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

// TestHandler_MoveImageStale: a stale image move is refused with 422 and changes
// nothing.
func TestHandler_MoveImageStale(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	ctx = i18n.WithLocale(ctx, i18n.En)
	slug, _, keys := imageOrderProduct(t, 2)
	h := productstest.NewHandler(pool, products.NewStore(pool))
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

// TestStore_MoveImageConcurrentReorders: concurrent image reorders never
// duplicate a position.
//
// Two reorders of one product queue on its row lock: neither computes from the
// order the other is about to replace, and no position is ever duplicated.
func TestStore_MoveImageConcurrentReorders(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug, _, keys := imageOrderProduct(t, 4)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.MoveImage(ctx, slug, keys[i%4], products.MoveToCover)
			if err != nil && !errors.Is(err, products.ErrInvalid) {
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

// TestStore_AttachImageConcurrentMove: concurrent attaches and reorders never
// collide on a position.
//
// An attach computes max(position)+1 and a reorder renumbers every position; both
// take the product's lock first, so neither can collide with the other.
func TestStore_AttachImageConcurrentMove(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug, _, keys := imageOrderProduct(t, 3)
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := range 8 {
		wg.Add(2)
		digest := storeMedia(t, pool)
		go func() {
			defer wg.Done()
			if err := s.AttachImage(ctx, slug, digest, "新圖", "", "", 800, 800); err != nil {
				errs <- fmt.Errorf("attach: %w", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := s.MoveImage(ctx, slug, keys[i%3], products.MoveToCover); err != nil && !errors.Is(err, products.ErrInvalid) {
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

// TestStore_MoveImageConcurrentUp: concurrent moves up lose no update.
//
// Three moves up of the last of four images, fired together: serialised they end
// with it at the cover; without the product lock they read the same starting
// order and the moves overwrite each other.
func TestStore_MoveImageConcurrentUp(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug, _, keys := imageOrderProduct(t, 4)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Go(func() {
			if err := s.MoveImage(ctx, slug, keys[3], products.MoveUp); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("a queued move failed: %v", err)
	}
	if got := imageOrder(t, slug); got[0] != keys[3] || strings.Join(got[1:], ",") != strings.Join(keys[:3], ",") {
		t.Fatalf("three serialised moves up left the order %v, want %s first then %v", got, keys[3], keys[:3])
	}
}
