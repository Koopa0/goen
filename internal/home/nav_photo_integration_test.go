//go:build integration

package home_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestNavigationFollowsConfiguredDepartmentPhotographs(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer pgtx.Rollback(ctx, tx)

			var root uuid.UUID
			if err = tx.QueryRow(ctx, `
				INSERT INTO categories (slug, name, position)
				VALUES ('photo-department', 'Photo department',
				    (SELECT coalesce(max(position), -1) + 1 FROM categories WHERE parent_id IS NULL))
				RETURNING id`).Scan(&root); err != nil {
				t.Fatalf("insert department: %v", err)
			}
			if _, err = tx.Exec(ctx, `
				WITH c AS (
				    INSERT INTO categories (slug, name, parent_id, position)
				    VALUES ('photo-child', 'Photo child', $1, 0) RETURNING id
				), p AS (
				    INSERT INTO products (category_id, slug, name, status, published_at)
				    SELECT c.id, 'photo-product', 'Photo product', 'active', now() FROM c RETURNING id
				)
				INSERT INTO product_variants (product_id, sku, price_cents, stock_quantity, safety_stock, position)
				SELECT p.id, 'PHOTO-PRODUCT', 1000, 5, 0, 0 FROM p`, root); err != nil {
				t.Fatalf("insert child and listed product: %v", err)
			}
			if _, err = tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
				t.Fatalf("validate catalogue fixture: %v", err)
			}

			var upload bytes.Buffer
			if err = png.Encode(&upload, image.NewRGBA(image.Rect(0, 0, 600, 400))); err != nil {
				t.Fatalf("encode upload: %v", err)
			}
			obj, data, err := media.Normalise(&upload)
			if err != nil {
				t.Fatalf("normalise upload: %v", err)
			}
			q := db.New(tx)
			if err = q.PutMedia(ctx, db.PutMediaParams{
				Digest: obj.Digest, ContentType: obj.ContentType, Bytes: data,
				Width: obj.Width, Height: obj.Height, ByteSize: obj.ByteSize,
			}); err != nil {
				t.Fatalf("store upload: %v", err)
			}

			store := home.NewStore(tx)
			listing := catalog.NewStore(tx)
			for _, slug := range []string{"photo-department", "books-stationery"} {
				if n, setErr := q.SetCategoryImage(ctx, db.SetCategoryImageParams{
					Slug: slug, ImageKey: obj.Digest, ImageAlt: "Department photograph",
				}); setErr != nil || n != 1 {
					t.Fatalf("set %s photograph: rows=%d err=%v", slug, n, setErr)
				}
				url := "/media/" + obj.Digest
				srcset := url + "/400 400w, " + url + " 600w"
				assertDepartmentPhotograph(t, ctx, store, listing, slug, url, srcset)
				if n, clearErr := q.ClearCategoryImage(ctx, slug); clearErr != nil || n != 1 {
					t.Fatalf("clear %s photograph: rows=%d err=%v", slug, n, clearErr)
				}
				assertDepartmentPhotograph(t, ctx, store, listing, slug, "", "")
			}
		})
	}
}

func assertDepartmentPhotograph(t *testing.T, ctx context.Context, store *home.Store, listing *catalog.Store, slug, url, srcset string) {
	t.Helper()
	nav, err := store.Nav(ctx)
	if err != nil {
		t.Fatalf("Nav(): %v", err)
	}
	var item *layouts.NavItem
	for i := range nav {
		if nav[i].Slug == slug {
			item = &nav[i]
			break
		}
	}
	if item == nil {
		t.Fatalf("Nav() omits %s", slug)
	}
	if item.PhotoURL != url || item.PhotoSrcset != srcset {
		t.Errorf("Nav() %s photograph = %q, %q; want %q, %q", slug, item.PhotoURL, item.PhotoSrcset, url, srcset)
	}
	view, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	found := false
	for _, category := range view.Categories {
		if category.Slug == slug {
			found = true
			if category.Photo.URL != url || category.Photo.Srcset != srcset {
				t.Errorf("home %s photograph = %+v; want %q, %q", slug, category.Photo, url, srcset)
			}
		}
	}
	if !found {
		t.Errorf("home omits %s", slug)
	}
	category, err := listing.Listing(ctx, slug, catalog.Filters{})
	if err != nil {
		t.Fatalf("Listing(): %v", err)
	}
	if photo := category.Theme.Image(); photo.URL != url || photo.Srcset != srcset {
		t.Errorf("landing %s photograph = %+v; want %q, %q", slug, photo, url, srcset)
	}
	var header strings.Builder
	if err = layouts.Header(layouts.Page{}).Render(layouts.WithTopNav(ctx, nav), &header); err != nil {
		t.Fatalf("render header: %v", err)
	}
	browse := regexp.MustCompile(`(?s)<a class="goen-dept__browse" href="/c/` + regexp.QuoteMeta(slug) + `">(.*?)</a>`).FindString(header.String())
	if browse == "" {
		t.Fatalf("header omits %s panel", slug)
	}
	photos := regexp.MustCompile(`<img class="goen-dept__photo"[^>]*>`).FindAllString(browse, -1)
	if url == "" {
		if len(photos) != 0 {
			t.Errorf("cleared %s photograph still rendered: %v", slug, photos)
		}
		return
	}
	if len(photos) != 1 {
		t.Fatalf("%s panel renders %d photographs, want 1", slug, len(photos))
	}
	for _, want := range []string{`src="` + url + `"`, `srcset="` + srcset + `"`, `alt=""`} {
		if !strings.Contains(photos[0], want) {
			t.Errorf("%s panel photograph omits %s", slug, want)
		}
	}
}
