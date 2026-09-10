//go:build integration

package media_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/media"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	code := m.Run()
	stop()
	os.Exit(code)
}

func samplePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

func TestTheSamePictureIsOneRow(t *testing.T) {
	ctx := t.Context()
	s := media.NewStore(pool)
	body := samplePNG(t, 120, 80)

	first, err := s.Put(ctx, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	// Same pixels, different bytes on the wire: a trailer the re-encode drops.
	second, err := s.Put(ctx, bytes.NewReader(append(append([]byte{}, body...), []byte("junk")...)))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.Digest != second.Digest {
		t.Errorf("two uploads of one picture gave %s and %s", first.Digest[:12], second.Digest[:12])
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM media_objects WHERE digest = $1`, first.Digest).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d rows for one picture, want 1", n)
	}
}

// The digest is never re-derived on read, so corruption would be silent.
func TestTheStoredBytesAreServedBackUnchanged(t *testing.T) {
	ctx := t.Context()
	s := media.NewStore(pool)

	obj, err := s.Put(ctx, bytes.NewReader(samplePNG(t, 200, 150)))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	contentType, data, err := s.Bytes(ctx, obj.Digest)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if contentType != obj.ContentType {
		t.Errorf("content type came back %q, want %q", contentType, obj.ContentType)
	}
	if len(data) != int(obj.ByteSize) {
		t.Errorf("%d bytes came back, %d were stored", len(data), obj.ByteSize)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the round-tripped image does not decode: %v", err)
	}
	if cfg.Width != 200 || cfg.Height != 150 {
		t.Errorf("came back %dx%d, want 200x150", cfg.Width, cfg.Height)
	}
}

// A year-long immutable Cache-Control makes a rewritten digest unfixable.
func TestAStoredImageCannotBeRewritten(t *testing.T) {
	ctx := t.Context()
	obj, err := media.NewStore(pool).Put(ctx, bytes.NewReader(samplePNG(t, 60, 60)))
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	_, err = pool.Exec(ctx,
		`UPDATE media_objects SET bytes = '\x00'::bytea WHERE digest = $1`, obj.Digest)
	if err == nil {
		t.Fatal("a stored image was rewritten")
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "media_objects_immutable" {
		t.Errorf("refused by %v, want media_objects_immutable", err)
	}

	// Deleting IS allowed: that is how an unreferenced upload is reclaimed.
	if _, err := pool.Exec(ctx, `DELETE FROM media_objects WHERE digest = $1`, obj.Digest); err != nil {
		t.Errorf("a stored image could not be deleted: %v", err)
	}
}

// Each rule fires where it cannot be bypassed; the Go side checks these too.
func TestTheSchemaRefusesAnInconsistentRow(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		name       string
		digest     string
		mime       string
		bytes      []byte
		w, h, size int
		want       string
	}{
		{"a size that does not match the bytes", digestOf("a"), "image/png",
			[]byte("abc"), 10, 10, 999, "media_objects_size_matches"},
		{"a type goen cannot produce", digestOf("b"), "image/svg+xml",
			[]byte("abc"), 10, 10, 3, "media_objects_type_supported"},
		{"a zero dimension", digestOf("c"), "image/png",
			[]byte("abc"), 0, 10, 3, "media_objects_dimensions_sane"},
		{"a dimension past the ceiling", digestOf("d"), "image/png",
			[]byte("abc"), 9000, 10, 3, "media_objects_dimensions_sane"},
		{"a digest that is not a sha256", "not-a-digest", "image/png",
			[]byte("abc"), 10, 10, 3, "media_objects_digest_format"},
		{"an uppercase digest", "A" + digestOf("e")[1:], "image/png",
			[]byte("abc"), 10, 10, 3, "media_objects_digest_format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, `
				INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size)
				VALUES ($1, $2, $3, $4, $5, $6)`,
				tt.digest, tt.mime, tt.bytes, tt.w, tt.h, tt.size)
			if err == nil {
				t.Fatal("accepted")
			}
			// Bound to the constraint NAME: several of these rows would trip
			// more than one rule.
			pgErr, ok := errors.AsType[*pgconn.PgError](err)
			if !ok || pgErr.ConstraintName != tt.want {
				t.Errorf("refused by %q, want %s", constraintOf(err), tt.want)
			}
		})
	}
}

// digestOf makes a distinct valid-looking digest per case, so a case never
// fails on the primary key instead of the rule it is testing.
func digestOf(seed string) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = "0123456789abcdef"[(i+int(seed[0]))%16]
	}
	return string(out)
}

func constraintOf(err error) string {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		if pgErr.ConstraintName != "" {
			return pgErr.ConstraintName
		}
		return pgErr.Code
	}
	return err.Error()
}

// An upload nothing points at goes away; one something points at does not.
func TestAbandonedUploadsAreReclaimed(t *testing.T) {
	ctx := t.Context()
	s := media.NewStore(pool)
	log := slog.New(slog.DiscardHandler)

	abandoned, err := s.Put(ctx, bytes.NewReader(samplePNG(t, 61, 43)))
	if err != nil {
		t.Fatalf("upload the abandoned one: %v", err)
	}
	attached, err := s.Put(ctx, bytes.NewReader(samplePNG(t, 62, 44)))
	if err != nil {
		t.Fatalf("upload the attached one: %v", err)
	}
	// Attached to a product, which is what makes it off limits. The product is
	// created here rather than found: this suite loads the schema and no
	// catalogue, so selecting one matches nothing and attaches nothing.
	tag, err := pool.Exec(ctx, `
		WITH b AS (INSERT INTO brands (slug, name) VALUES ('sweep-brand', '測試品牌') RETURNING id),
		     c AS (INSERT INTO categories (slug, name, position) SELECT 'sweep-cat', '測試分類', coalesce(max(position) + 1, 0) FROM categories WHERE parent_id IS NULL RETURNING id),
		     p AS (INSERT INTO products (brand_id, category_id, slug, name)
		           SELECT b.id, c.id, 'sweep-product', '測試商品' FROM b, c RETURNING id)
		INSERT INTO product_images (product_id, storage_key, alt_text, position)
		SELECT p.id, $1, '測試圖片', 0 FROM p`, attached.Digest)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("attached %d images, want 1 — the fixture did not reach its precondition",
			tag.RowsAffected())
	}

	// Inside the grace window, both are left alone.
	early, err := s.Sweep(ctx, log)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if early != 0 {
		t.Fatalf("the sweeper reclaimed %d uploads inside the grace window", early)
	}

	ageUploads(t, abandoned.Digest, attached.Digest)

	reclaimed, err := s.Sweep(ctx, log)
	if err != nil {
		t.Fatalf("sweep after the grace window: %v", err)
	}
	if reclaimed == 0 {
		t.Fatal("the sweeper reclaimed nothing")
	}

	if exists(t, abandoned.Digest) {
		t.Error("the abandoned upload survived")
	}
	if !exists(t, attached.Digest) {
		t.Error("an attached upload was reclaimed — a live image is now broken")
	}
}

// TestAHeroSlidesImageIsNotReclaimed covers the referencing column that is
// easy to forget; a delete that missed it would blank the front page.
func TestAHeroSlidesImageIsNotReclaimed(t *testing.T) {
	ctx := t.Context()
	s := media.NewStore(pool)

	hero, err := s.Put(ctx, bytes.NewReader(samplePNG(t, 71, 51)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href,
		                         image_key, image_alt, position)
		VALUES ('版面檢查', '看看', '/deals', $1, '測試圖片', 99)`, hero.Digest); err != nil {
		t.Fatalf("attach to a hero slide: %v", err)
	}
	ageUploads(t, hero.Digest)

	if _, err := s.Sweep(ctx, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if !exists(t, hero.Digest) {
		t.Error("the home page's hero image was reclaimed")
	}
}

func exists(t *testing.T, digest string) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM media_objects WHERE digest = $1`, digest).Scan(&n); err != nil {
		t.Fatalf("count media: %v", err)
	}
	return n > 0
}

// ageUploads pushes rows past the sweeper's grace window. media_objects_immutable
// refuses UPDATE, so each row is re-inserted with an older created_at.
func ageUploads(t *testing.T, digests ...string) {
	t.Helper()
	for _, digest := range digests {
		if _, err := pool.Exec(t.Context(), `
			WITH old AS (DELETE FROM media_objects WHERE digest = $1 RETURNING *)
			INSERT INTO media_objects (digest, content_type, byte_size, width, height, bytes, created_at)
			SELECT digest, content_type, byte_size, width, height, bytes, now() - interval '48 hours'
			FROM old`, digest); err != nil {
			t.Fatalf("age %s: %v", digest, err)
		}
	}
}

// TestAnUploadAttachedMidSweepSurvivesIt holds the window between the two
// statements the sweep is made of.
//
// There is no foreign key to lean on: product_images.storage_key holds either an
// embedded filename from the seed or a digest, so it cannot point at
// media_objects. The reference predicate is repeated inside the DELETE instead,
// and the interleaving is DRIVEN rather than hoped for: the candidate list is
// taken first, the attach commits, and only then is the delete asked for.
func TestAnUploadAttachedMidSweepSurvivesIt(t *testing.T) {
	ctx := t.Context()
	s := media.NewStore(pool)

	up, err := s.Put(ctx, bytes.NewReader(samplePNG(t, 73, 53)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	ageUploads(t, up.Digest)

	// Sweep's FIRST statement, taken while nothing points at the upload.
	candidates, err := s.Candidates(ctx, 200)
	if err != nil {
		t.Fatalf("read the candidate list: %v", err)
	}
	if !slices.Contains(candidates, up.Digest) {
		t.Fatalf("the upload is not a sweep candidate, so this proved nothing")
	}
	candidate := up.Digest

	// Attached AFTER that read, exactly as a staff member saving a product would.
	// A product image; the hero half is exercised below.
	tag, attachErr := pool.Exec(ctx, `
		WITH b AS (INSERT INTO brands (slug, name) VALUES ('midsweep-brand', '測試品牌') RETURNING id),
		     c AS (INSERT INTO categories (slug, name, position) SELECT 'midsweep-cat', '測試分類', coalesce(max(position) + 1, 0) FROM categories WHERE parent_id IS NULL RETURNING id),
		     p AS (INSERT INTO products (brand_id, category_id, slug, name)
		           SELECT b.id, c.id, 'midsweep-product', '測試商品' FROM b, c RETURNING id)
		INSERT INTO product_images (product_id, storage_key, alt_text, position)
		SELECT p.id, $1, '測試圖片', 0 FROM p`, candidate)
	if attachErr != nil {
		t.Fatalf("attach: %v", attachErr)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("attached %d images, want 1 — the fixture did not reach its precondition",
			tag.RowsAffected())
	}

	// And now Sweep's SECOND statement, issued against that stale list — which is
	// the whole window: calling Sweep here would re-read the list instead.
	gone, reclaimErr := s.Reclaim(ctx, candidate)
	if reclaimErr != nil {
		t.Fatalf("reclaim: %v", reclaimErr)
	}
	if gone {
		t.Error("the delete went through against a stale list")
	}
	if !exists(t, candidate) {
		t.Error("an upload attached between the sweeper's read and its delete was " +
			"reclaimed; whatever it was attached to now points at a 404")
	}

	// The HERO half of the same window, as its own subject. The re-check names
	// two tables and a fixture that attaches to one of them proves only that one.
	hero, heroErr := s.Put(ctx, bytes.NewReader(samplePNG(t, 74, 54)))
	if heroErr != nil {
		t.Fatalf("upload: %v", heroErr)
	}
	ageUploads(t, hero.Digest)
	heroCandidates, listErr := s.Candidates(ctx, 200)
	if listErr != nil {
		t.Fatalf("read the candidate list: %v", listErr)
	}
	if !slices.Contains(heroCandidates, hero.Digest) {
		t.Fatalf("the hero upload is not a sweep candidate, so this proved nothing")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href,
		                         image_key, image_alt, position)
		VALUES ('搶在清掃前掛上', '看看', '/deals', $1, '測試圖片', 98)`,
		hero.Digest); err != nil {
		t.Fatalf("attach a hero: %v", err)
	}
	heroGone, heroReclaimErr := s.Reclaim(ctx, hero.Digest)
	if heroReclaimErr != nil {
		t.Fatalf("reclaim: %v", heroReclaimErr)
	}
	if heroGone || !exists(t, hero.Digest) {
		t.Error("a hero image attached between the sweeper's read and its delete " +
			"was reclaimed; the home page's largest picture is now a 404")
	}
}
