//go:build integration

package admin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
)

func TestOptionAxesMustPrecedeVariants(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := draftProduct(t, ctx, s)
	if errs, err := s.AddVariant(ctx, slug, &admin.VariantForm{SKU: "AXIS-" + strings.ToUpper(uuid.NewString()[:8]), PriceCents: 10000}); err != nil || len(errs) != 0 {
		t.Fatalf("create optionless SKU: %v %v", err, errs)
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		local := i18n.WithLocale(ctx, locale)
		body := url.Values{"name": {"Size"}, "name_en": {"Size"}}
		req := httptest.NewRequestWithContext(local, http.MethodPost, "/admin/products/"+slug+"/options", strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		adminHandlerOver(pool, s).AddOption(rec, req)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s response = %d: %s", locale, rec.Code, rec.Body.String())
		}
		for _, want := range []string{i18n.T(local, i18n.KeyFormOptionBeforeVariants), `aria-describedby="opt-name-error"`, `aria-invalid="true"`} {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("%s refusal missing %q", locale, want)
			}
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE product_variants SET is_active = false WHERE product_id = (SELECT id FROM products WHERE slug = $1)`, slug); err != nil {
		t.Fatal(err)
	}
	errs, err := s.AddOption(ctx, slug, admin.OptionDraft{Name: "Size"})
	if err != nil || errs["option"] != i18n.T(ctx, i18n.KeyFormOptionBeforeVariants) {
		t.Fatalf("inactive SKU did not freeze axes: %v %v", err, errs)
	}
	var axes, variants int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM product_options WHERE product_id = p.id), (SELECT count(*) FROM product_variants WHERE product_id = p.id) FROM products p WHERE slug = $1`, slug).Scan(&axes, &variants); err != nil {
		t.Fatal(err)
	}
	if axes != 0 || variants != 1 {
		t.Fatalf("refusal changed catalogue: axes=%d variants=%d", axes, variants)
	}
	empty := draftProduct(t, ctx, s)
	if errs, err := s.AddOption(ctx, empty, admin.OptionDraft{Name: "Size"}); err != nil || len(errs) != 0 {
		t.Fatalf("axis before SKU refused: %v %v", err, errs)
	}
	_, err = pool.Exec(ctx, `UPDATE product_options SET product_id = (SELECT id FROM products WHERE slug = $1) WHERE product_id = (SELECT id FROM products WHERE slug = $2)`, slug, empty)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "product_options_before_variants" {
		t.Fatalf("reparent bypassed axis guard: %v", err)
	}
}

func TestOptionAxisAndVariantCreationSerialize(t *testing.T) {
	for _, variantFirst := range []bool{true, false} {
		name := "axis-first"
		if variantFirst {
			name = "variant-first"
		}
		t.Run(name, func(t *testing.T) {
			ctx, _ := staffContext(t)
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
			slug := draftProduct(t, ctx, s)
			first, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = first.Rollback(context.WithoutCancel(ctx)) }()
			if _, err := first.Exec(ctx, `SET LOCAL ROLE admin`); err != nil {
				t.Fatal(err)
			}
			var pid int32
			if err := first.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if variantFirst {
				_, err = first.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents) SELECT id, $2, 10000 FROM products WHERE slug = $1`, slug, "RACE-"+strings.ToUpper(uuid.NewString()[:8]))
			} else {
				_, err = first.Exec(ctx, `INSERT INTO product_options (product_id, name) SELECT id, 'Size' FROM products WHERE slug = $1`, slug)
			}
			if err != nil {
				t.Fatal(err)
			}
			app := "axis-race-" + uuid.NewString()
			other := namedAdminPool(t, app)
			if _, err := other.Exec(ctx, `SET ROLE admin`); err != nil {
				t.Fatal(err)
			}
			writer := admin.NewStore(other, fakeRefunder{}, nil, nil)
			type outcome struct {
				fields map[string]string
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				var fields map[string]string
				var err error
				if variantFirst {
					fields, err = writer.AddOption(ctx, slug, admin.OptionDraft{Name: "Size"})
				} else {
					fields, err = writer.AddVariant(ctx, slug, &admin.VariantForm{SKU: "RACE-" + strings.ToUpper(uuid.NewString()[:8]), PriceCents: 10000})
				}
				done <- outcome{fields, err}
			}()
			waitForBlockedApplication(t, ctx, app, pid)
			if err := first.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			result := <-done
			field, key := "options", i18n.KeyFormVariantNeedsEveryOption
			if variantFirst {
				field, key = "option", i18n.KeyFormOptionBeforeVariants
			}
			if result.err != nil || result.fields[field] != i18n.T(ctx, key) {
				t.Fatalf("concurrent write = %v %v, want %s", result.err, result.fields, field)
			}
		})
	}
}
