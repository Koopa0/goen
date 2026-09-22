//go:build integration

package product_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/ui/pages"
)

type presentationReadKey struct{}
type presentationReadPause struct {
	once             sync.Once
	entered, release chan struct{}
}

func (p *presentationReadPause) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, presentationReadKey{}, strings.Contains(data.SQL, "-- name: ProductBySlug"))
}
func (p *presentationReadPause) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	matched, _ := ctx.Value(presentationReadKey{}).(bool)
	if matched {
		p.once.Do(func() {
			close(p.entered)
			select {
			case <-p.release:
			case <-ctx.Done():
			}
		})
	}
}

type cacheProductFixture struct {
	id, categoryID uuid.UUID
	slug           string
}

func newCacheProduct(t *testing.T) cacheProductFixture {
	t.Helper()
	ctx := t.Context()
	slug := "cache-race-" + uuid.NewString()
	var id, category, variant uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO categories(slug,name,name_en) VALUES($1,'Category before','Category before EN') RETURNING id`, slug).Scan(&category); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO products(slug,name,name_en,brand_id,category_id) SELECT $1,'Before','Before EN',id,$2 FROM brands ORDER BY id LIMIT 1 RETURNING id`, slug, category).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO product_variants(product_id,sku,price_cents) VALUES($1,$2,100000) RETURNING id`, id, strings.ToUpper(slug)).Scan(&variant); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT record_inventory_movement($1,10,'receipt',$2,NULL,NULL)`, variant, slug); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_specs(product_id,label,value,value_en) VALUES($1,'Revision','Before','Before EN')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET status='active',published_at=now() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	return cacheProductFixture{id: id, categoryID: category, slug: slug}
}

func editCacheProduct(t *testing.T, fixture cacheProductFixture, unpublish bool) {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	if _, err = tx.Exec(t.Context(), `UPDATE products SET name='After',name_en='After EN' WHERE id=$1`, fixture.id); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), `UPDATE product_specs SET value='After',value_en='After EN' WHERE product_id=$1`, fixture.id); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), `UPDATE categories SET name='Category after',name_en='Category after EN' WHERE id=$1`, fixture.categoryID); err != nil {
		t.Fatal(err)
	}
	if unpublish {
		if _, err = tx.Exec(t.Context(), `UPDATE products SET status='draft',published_at=NULL WHERE id=$1`, fixture.id); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCacheRejectsMixedCommittedPresentation(t *testing.T) {
	addr := dbtest.Valkey(t)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			fixture := newCacheProduct(t)
			cacheA := openCacheOnAddr(t, addr, product.DefaultCacheConfig())
			defer cacheA.Close()
			cacheB := openCacheOnAddr(t, addr, product.DefaultCacheConfig())
			defer cacheB.Close()
			pause := &presentationReadPause{entered: make(chan struct{}), release: make(chan struct{})}
			pcfg := pool.Config().Copy()
			pcfg.ConnConfig.Tracer = pause
			traced, err := pgxpool.NewWithConfig(t.Context(), pcfg)
			if err != nil {
				t.Fatal(err)
			}
			defer traced.Close()
			ctx, cancel := context.WithTimeout(i18n.WithLocale(t.Context(), locale), 10*time.Second)
			defer cancel()
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(pause.release) })
			storeA := product.NewStoreWithCache(traced, cacheA)
			storeB := product.NewStoreWithCache(pool, cacheB)
			type result struct {
				view pages.ProductView
				err  error
			}
			done := make(chan result, 1)
			go func() { view, loadErr := storeA.Load(ctx, fixture.slug, nil); done <- result{view, loadErr} }()
			select {
			case <-pause.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("presentation query not reached")
			}
			editCacheProduct(t, fixture, false)
			wantName, wantCategory := "After", "Category after"
			if locale == i18n.En {
				wantName += " EN"
				wantCategory += " EN"
			}
			check := func(view pages.ProductView, loadErr error) {
				t.Helper()
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				if view.Name != wantName || view.CategoryName != wantCategory || len(view.Specs) != 1 || view.Specs[0].Value != wantName {
					t.Fatalf("mixed committed presentation: name=%q category=%q specs=%+v", view.Name, view.CategoryName, view.Specs)
				}
			}
			check(storeB.Load(ctx, fixture.slug, nil))
			releaseOnce.Do(func() { close(pause.release) })
			select {
			case got := <-done:
				check(got.view, got.err)
			case <-time.After(5 * time.Second):
				t.Fatal("paused fill did not finish")
			}
			check(storeA.Load(ctx, fixture.slug, nil))
			if stats := product.CacheStatsOf(cacheA); stats.Fills != 0 {
				t.Fatalf("stale version was stored before retry hit current key: %+v", stats)
			}
		})
	}
}

func TestUnpublishDuringPresentationAssemblyDoesNotPopulateCache(t *testing.T) {
	fixture := newCacheProduct(t)
	cache, stop := openCache(t, product.DefaultCacheConfig())
	defer stop()
	pause := &presentationReadPause{entered: make(chan struct{}), release: make(chan struct{})}
	cfg := pool.Config().Copy()
	cfg.ConnConfig.Tracer = pause
	traced, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var once sync.Once
	defer once.Do(func() { close(pause.release) })
	store := product.NewStoreWithCache(traced, cache)
	done := make(chan error, 1)
	go func() { _, loadErr := store.Load(ctx, fixture.slug, nil); done <- loadErr }()
	select {
	case <-pause.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("presentation query not reached")
	}
	editCacheProduct(t, fixture, true)
	once.Do(func() { close(pause.release) })
	if loadErr := cacheResult(t, done); !errors.Is(loadErr, product.ErrNotFound) {
		t.Fatalf("unpublished fill=%v", loadErr)
	}
	if stats := product.CacheStatsOf(cache); stats.Fills != 0 {
		t.Fatalf("unpublished fill reached cache: %+v", stats)
	}
	if _, loadErr := store.Load(ctx, fixture.slug, nil); !errors.Is(loadErr, product.ErrNotFound) {
		t.Fatalf("later unpublished request=%v", loadErr)
	}
}

func TestCacheRevisionCoversRenamesAndMovedChildRows(t *testing.T) {
	cache, stop := openCache(t, product.DefaultCacheConfig())
	defer stop()
	store := product.NewStoreWithCache(pool, cache)
	source, destination := newCacheProduct(t), newCacheProduct(t)
	if _, err := pool.Exec(t.Context(), `DELETE FROM product_specs WHERE product_id=$1`, destination.id); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []cacheProductFixture{source, destination} {
		if _, err := store.Load(t.Context(), fixture.slug, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(t.Context(), `UPDATE product_specs SET product_id=$1 WHERE product_id=$2`, destination.id, source.id); err != nil {
		t.Fatal(err)
	}
	sourceView, err := store.Load(t.Context(), source.slug, nil)
	if err != nil {
		t.Fatal(err)
	}
	destinationView, err := store.Load(t.Context(), destination.slug, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceView.Specs) != 0 || len(destinationView.Specs) != 1 {
		t.Fatalf("moved child cached under old parents: source=%+v destination=%+v", sourceView.Specs, destinationView.Specs)
	}
	renamed := source.slug + "-renamed"
	if _, err = pool.Exec(t.Context(), `UPDATE products SET slug=$1 WHERE id=$2`, renamed, source.id); err != nil {
		t.Fatal(err)
	}
	renamedView, err := store.Load(t.Context(), renamed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if renamedView.Slug != renamed {
		t.Fatalf("renamed product kept cached slug %q", renamedView.Slug)
	}
	if _, err = pool.Exec(t.Context(), `UPDATE categories SET slug=$1 WHERE id=$2`, renamed, source.categoryID); err != nil {
		t.Fatal(err)
	}
	renamedView, err = store.Load(t.Context(), renamed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if renamedView.CategorySlug != renamed {
		t.Fatalf("renamed category kept cached slug %q", renamedView.CategorySlug)
	}
}
