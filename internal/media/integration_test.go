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
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/koopa0/goen/internal/media"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("goen"),
		postgres.WithUsername("goen"),
		postgres.WithPassword("goen"),
		postgres.BasicWaitStrategies(),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		panic(err)
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic(err)
	}
	schema, err := os.ReadFile("../../migrations/001_initial_schema.up.sql")
	if err != nil {
		panic(err)
	}
	pool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		panic(err)
	}
	if _, err := pool.Exec(ctx, string(schema)); err != nil {
		panic(err)
	}
	code := m.Run()
	pool.Close()
	// Terminated here rather than by a defer: os.Exit does not run defers, so
	// a deferred terminate leaks the container on every run.
	_ = testcontainers.TerminateContainer(container)
	os.Exit(code)
}

// samplePNG is a real PNG of the given size.
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

// TestTheSamePictureIsOneRow proves two uploads of one picture deduplicate.
//
// Content addressing is the whole storage model: two uploads of the same
// picture must be one row, or every product that reuses a generic accessory
// shot stores it again and a cache holds it twice under two URLs.
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

// TestTheStoredBytesAreServedBackUnchanged proves bytea round-trips exactly.
//
// Round-tripping through bytea must not alter a byte: an image that comes back
// different is a corrupt image, and content addressing makes the failure silent
// because the digest is never re-derived on read.
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

// TestAStoredImageCannotBeRewritten proves a digest always names the same
// bytes.
//
// media_objects_immutable. The digest names the bytes, so changing the bytes
// under a digest would make every cached copy in the world wrong — and the
// year-long immutable Cache-Control is what makes that unfixable.
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

// TestTheSchemaRefusesAnInconsistentRow proves each rule fires where it cannot
// be bypassed.
//
// The Go side checks these too, but the schema is where they cannot be
// bypassed — every one of these is a row that would make a page lie about what
// a visitor is about to download.
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
			// more than one rule, and asserting "an error happened" would not
			// say the intended one fired.
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

// TestAbandonedUploadsAreReclaimed holds that an upload nothing points at goes
// away, and that one something points at does not.
//
// UnreferencedMedia and DeleteMedia shipped with the media pipeline and neither
// was ever called: every abandoned upload — a staff member who picked the wrong
// file, a form filled in and never saved — stayed in media_objects for good.
// The bytes are stored IN PostgreSQL, which makes that the whole cost of the
// storage decision paid for nothing.
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
	// Attached to a product, which is what makes it off limits. Without this
	// the test would only prove that the sweeper deletes things.
	//
	// The product is created here rather than found: this suite loads the
	// schema and no catalogue, so `SELECT ... FROM products LIMIT 1` matched
	// nothing and the INSERT silently wrote zero rows — the attached upload was
	// never attached, and the test failed for the right reason by luck.
	tag, err := pool.Exec(ctx, `
		WITH b AS (INSERT INTO brands (slug, name) VALUES ('sweep-brand', '測試品牌') RETURNING id),
		     c AS (INSERT INTO categories (slug, name) VALUES ('sweep-cat', '測試分類') RETURNING id),
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

	// Inside the grace window, both are left alone: an upload is stored before
	// it is attached, and those are two requests.
	early, err := s.Sweep(ctx, log)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if early != 0 {
		t.Fatalf("the sweeper reclaimed %d uploads inside the grace window", early)
	}

	// media_objects is append-only, so the rows cannot be aged in place. The
	// grace window is aged instead — a shorter one is the same test with the
	// clock moved rather than a special case in the production query.
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

// TestAHeroSlidesImageIsNotReclaimed. The predicate names every referencing
// column, and hero_slides is the one that is easy to forget: it was added after
// product_images, and a delete that missed it would blank the front page.
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

// ageUploads pushes rows past the sweeper's grace window.
//
// media_objects is append-only — media_objects_immutable refuses UPDATE, which
// is the point of content addressing — so the row cannot be edited. The rows
// are re-inserted from a copy carrying an older created_at instead, which is
// the only door the schema leaves open and therefore the honest fixture.
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
