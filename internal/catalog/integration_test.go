//go:build integration

package catalog_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
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

func get(t *testing.T, target string) (status int, body string) {
	t.Helper()
	h := catalog.NewHandler(catalog.NewStore(pool), slog.New(slog.DiscardHandler))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	res := httptest.NewRecorder()

	if strings.HasPrefix(target, "/c/") {
		slug := strings.TrimPrefix(target, "/c/")
		if i := strings.IndexByte(slug, '?'); i >= 0 {
			slug = slug[:i]
		}
		req.SetPathValue("slug", slug)
		h.Listing(res, req)
	} else {
		h.Search(res, req)
	}
	return res.Code, res.Body.String()
}

// /c/accessories holds no products of its own; chargers and cases hold four
// between them.
func TestListingIncludesDescendants(t *testing.T) {
	code, body := get(t, "/c/accessories")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	for _, name := range []string{
		"Aurora GaN 65W 充電器",
		"Koto 編織 USB-C 線 2m",
		"Pixelight 9 Pro 保護殼",
		"Meridian 筆電內袋",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("a descendant category's product is missing from the listing: %q", name)
		}
	}
}

func TestUnknownCategoryIs404(t *testing.T) {
	code, body := get(t, "/c/no-such-category")
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
	if strings.Contains(body, "goen-tiles__grid") {
		t.Error("an unknown category rendered a product grid")
	}
}

// The fixture has a cheap sold-out variant and an expensive available one, so a
// query checking the two conditions separately finds one variant for each.
func TestFacetsMatchOneVariant(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const setup = `
	INSERT INTO products (id, brand_id, category_id, slug, name, status, published_at)
	SELECT 'eeee0001-0000-4000-8000-000000000001', b.id, c.id, 'split-stock', '分裂庫存機', 'active', now()
	FROM brands b, categories c WHERE b.slug='pixelight' AND c.slug='phones';

	INSERT INTO product_variants (id, product_id, sku, price_cents, stock_quantity, safety_stock, position) VALUES
	  ('eeee0002-0000-4000-8000-000000000001','eeee0001-0000-4000-8000-000000000001','SPLIT-CHEAP', 100000, 0, 2, 80),
	  ('eeee0002-0000-4000-8000-000000000002','eeee0001-0000-4000-8000-000000000001','SPLIT-PRICEY',900000,50, 2, 81);`

	if _, err := tx.Exec(ctx, setup); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	store := catalog.NewStore(tx)

	// In stock AND at most NT$1,000: only the sold-out variant is that cheap.
	trapped, trapErr := store.Listing(ctx, "phones", catalog.Filters{
		InStockOnly: true,
		MaxPrice:    100000,
		Page:        1,
	})
	if trapErr != nil {
		t.Fatalf("listing: %v", trapErr)
	}
	for _, p := range trapped.Products {
		if p.Slug == "split-stock" {
			t.Error("a product matched 'in stock AND cheap' with the two conditions " +
				"satisfied by different variants: its cheap variant is sold out")
		}
	}

	// The control: without it a query returning nothing at all would pass.
	found, findErr := store.Listing(ctx, "phones", catalog.Filters{
		InStockOnly: true,
		MinPrice:    900000,
		Page:        1,
	})
	if findErr != nil {
		t.Fatalf("listing: %v", findErr)
	}
	var ok bool
	for _, p := range found.Products {
		if p.Slug == "split-stock" {
			ok = true
		}
	}
	if !ok {
		t.Error("the same product did not match 'in stock AND expensive', which its " +
			"available variant satisfies — the filter refuses everything, not just the trap")
	}
}

// record_inventory_movement refuses a hold that would take stock below
// safety_stock, so a variant sitting at the floor has stock and cannot be sold.
func TestInStockMeansSellable(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO products (id, brand_id, category_id, slug, name, status, published_at)
		SELECT 'eeee0003-0000-4000-8000-000000000001', b.id, c.id, 'at-the-floor', '安全庫存機', 'active', now()
		FROM brands b, categories c WHERE b.slug='pixelight' AND c.slug='phones';

		INSERT INTO product_variants (id, product_id, sku, price_cents, stock_quantity, safety_stock, position)
		VALUES ('eeee0004-0000-4000-8000-000000000001','eeee0003-0000-4000-8000-000000000001','FLOOR-1',500000,2,2,82);`,
	); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	view, listErr := catalog.NewStore(tx).Listing(ctx, "phones", catalog.Filters{InStockOnly: true, Page: 1})
	if listErr != nil {
		t.Fatalf("listing: %v", listErr)
	}
	for _, p := range view.Products {
		if p.Slug == "at-the-floor" {
			t.Error("a variant holding exactly safety_stock counted as in stock; " +
				"record_inventory_movement would refuse the sale")
		}
	}

	all, allErr := catalog.NewStore(tx).Listing(ctx, "phones", catalog.Filters{Page: 1})
	if allErr != nil {
		t.Fatalf("listing: %v", allErr)
	}
	var seen bool
	for _, p := range all.Products {
		if p.Slug == "at-the-floor" {
			seen = true
			if p.InStock {
				t.Error("the product reports itself in stock while its only variant is at the floor")
			}
		}
	}
	if !seen {
		t.Error("the product vanished from an unfiltered listing")
	}
}

func TestSearchEscapesWildcards(t *testing.T) {
	code, body := get(t, "/search?q=%25")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if strings.Contains(body, "Pixelight 9 Pro 5G") {
		t.Error("searching for a bare % returned products; the wildcard reached ILIKE unescaped")
	}
	if !strings.Contains(body, "找不到") {
		t.Error("a search matching nothing did not render the empty state")
	}
}

// The trigram index serves only one of the two scripts.
func TestSearchFindsLatinAndChinese(t *testing.T) {
	for _, tc := range []struct{ q, want string }{
		{"pixel", "Pixelight"},
		{"耳機", "Koto"},
		{"Meridian", "Meridian"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			code, body := get(t, "/search?q="+tc.q)
			if code != http.StatusOK {
				t.Fatalf("status = %d, want 200", code)
			}
			if !strings.Contains(body, tc.want) {
				t.Errorf("searching %q did not find %q", tc.q, tc.want)
			}
		})
	}
}

func TestListingPriceIsBuyable(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO products (id, brand_id, category_id, slug, name, status, published_at)
		SELECT 'eeee0005-0000-4000-8000-000000000001', b.id, c.id, 'cheap-gone', '便宜缺貨機', 'active', now()
		FROM brands b, categories c WHERE b.slug='pixelight' AND c.slug='phones';

		INSERT INTO product_variants (id, product_id, sku, price_cents, stock_quantity, safety_stock, position) VALUES
		  ('eeee0006-0000-4000-8000-000000000001','eeee0005-0000-4000-8000-000000000001','GONE-CHEAP', 100000, 0,2,83),
		  ('eeee0006-0000-4000-8000-000000000002','eeee0005-0000-4000-8000-000000000001','HAVE-PRICEY',700000,50,2,84);`,
	); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	view, listErr := catalog.NewStore(tx).Listing(ctx, "phones", catalog.Filters{Page: 1})
	if listErr != nil {
		t.Fatalf("listing: %v", listErr)
	}
	for _, p := range view.Products {
		if p.Slug != "cheap-gone" {
			continue
		}
		if p.PriceCents != 700000 {
			t.Errorf("card shows NT$%d; want the buyable variant's NT$7,000 rather than "+
				"the sold-out NT$1,000", p.PriceCents/100)
		}
		return
	}
	t.Error("the fixture product is not in the listing")
}

// "On sale" is a variant fact, and a product qualifies when ANY active variant
// carries one.
func TestDealsShowsOnlyWhatIsMarkedDown(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool)

	view, err := s.Deals(ctx, 1)
	if err != nil {
		t.Fatalf("deals: %v", err)
	}
	if view.Total == 0 {
		t.Fatal("no deals at all; the seed has marked-down variants, so the query is wrong")
	}

	for _, tile := range view.Products {
		var discounted bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM product_variants pv JOIN products p ON p.id = pv.product_id
				WHERE p.slug = $1 AND pv.is_active
				  AND pv.compare_at_price_cents > pv.price_cents)`,
			tile.Slug).Scan(&discounted); err != nil {
			t.Fatalf("check %s: %v", tile.Slug, err)
		}
		if !discounted {
			t.Errorf("%q is on the deals page with nothing marked down", tile.Slug)
		}
	}

	var expected int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM products p
		WHERE p.status = 'active' AND EXISTS (
			SELECT 1 FROM product_variants dv
			WHERE dv.product_id = p.id AND dv.is_active
			  AND dv.compare_at_price_cents > dv.price_cents)`).Scan(&expected); err != nil {
		t.Fatalf("count: %v", err)
	}
	if view.Total != expected {
		t.Errorf("deals shows %d products, the catalogue has %d marked down",
			view.Total, expected)
	}
}

// By fraction, not amount: ordering by absolute saving puts the expensive
// things on top, which is a price list rather than a sale.
func TestDealsAreOrderedByHowDeepTheCutIs(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool)

	view, err := s.Deals(ctx, 1)
	if err != nil {
		t.Fatalf("deals: %v", err)
	}
	if len(view.Products) < 2 {
		t.Skipf("only %d deals; ordering cannot be observed", len(view.Products))
	}

	last := 2.0
	for i, tile := range view.Products {
		if tile.CompareCents <= 0 {
			continue
		}
		cut := float64(tile.CompareCents-tile.PriceCents) / float64(tile.CompareCents)
		if cut > last+1e-9 {
			t.Errorf("product %d is %.0f%% off, after one at %.0f%% — the deepest "+
				"cut is not first", i, cut*100, last*100)
		}
		last = cut
	}
}

// campaign creates one running for a week and returns its slug.
func campaign(t *testing.T, slug string) string {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO sale_campaigns (slug, title, ends_at)
		VALUES ($1, '測試活動', now() + interval '7 days')`, slug); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	return slug
}

func feature(t *testing.T, campaignSlug, productSlug string) error {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO sale_campaign_products (campaign_id, product_id)
		SELECT c.id, p.id FROM sale_campaigns c, products p
		WHERE c.slug = $1 AND p.slug = $2`, campaignSlug, productSlug)
	return err
}

func discountedSlug(t *testing.T) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(), `
		SELECT p.slug FROM products p JOIN product_variants pv ON pv.product_id = p.id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.compare_at_price_cents > pv.price_cents
		LIMIT 1`).Scan(&slug); err != nil {
		t.Fatalf("find discounted product: %v", err)
	}
	return slug
}

func plainSlug(t *testing.T) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(), `
		SELECT p.slug FROM products p
		WHERE p.status = 'active' AND NOT EXISTS (
			SELECT 1 FROM product_variants v
			WHERE v.product_id = p.id AND v.compare_at_price_cents IS NOT NULL)
		LIMIT 1`).Scan(&slug); err != nil {
		t.Fatalf("find undiscounted product: %v", err)
	}
	return slug
}

func TestACampaignOutsideItsWindowIsNotFound(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool)

	running := campaign(t, "window-now")
	if err := feature(t, running, discountedSlug(t)); err != nil {
		t.Fatalf("feature: %v", err)
	}
	if _, err := s.Campaign(ctx, running); err != nil {
		t.Fatalf("a running campaign was not found: %v", err)
	}

	tests := []struct {
		name string
		// Spelled out: sale_campaigns_slug_format allows no spaces.
		slug  string
		setup string
	}{
		{"already ended", "window-ended", `UPDATE sale_campaigns SET starts_at = now() - interval '30 days',
			ends_at = now() - interval '1 day' WHERE slug = $1`},
		{"not started yet", "window-future", `UPDATE sale_campaigns SET starts_at = now() + interval '1 day',
			ends_at = now() + interval '30 days' WHERE slug = $1`},
		{"switched off", "window-off", `UPDATE sale_campaigns SET is_active = false WHERE slug = $1`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug := campaign(t, tt.slug)
			if err := feature(t, slug, discountedSlug(t)); err != nil {
				t.Fatalf("feature: %v", err)
			}
			if _, err := pool.Exec(ctx, tt.setup, slug); err != nil {
				t.Fatalf("setup: %v", err)
			}
			if _, err := s.Campaign(ctx, slug); !errors.Is(err, catalog.ErrNotFound) {
				t.Errorf("got %v, want ErrNotFound", err)
			}
		})
	}
}

// sale_campaign_needs_discount takes a lock on the product before it reads the
// variants.
func TestOnlyDiscountedProductsCanBeFeatured(t *testing.T) {
	slug := campaign(t, "only-discounted")

	if err := feature(t, slug, discountedSlug(t)); err != nil {
		t.Errorf("a discounted product could not be featured: %v", err)
	}

	err := feature(t, slug, plainSlug(t))
	if err == nil {
		t.Fatal("a product with nothing marked down was featured; the campaign " +
			"would advertise it at full price")
	}
	if _, name := constraintName(err); name != "sale_campaign_needs_discount" {
		t.Errorf("refused by %q, want sale_campaign_needs_discount: %v", name, err)
	}
}

func TestACampaignShowsOnlyActiveProducts(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool)
	slug := campaign(t, "active-only")
	product := discountedSlug(t)
	if err := feature(t, slug, product); err != nil {
		t.Fatalf("feature: %v", err)
	}

	view, err := s.Campaign(ctx, slug)
	if err != nil {
		t.Fatalf("campaign: %v", err)
	}
	if len(view.Products) != 1 {
		t.Fatalf("%d products, want 1", len(view.Products))
	}

	if _, hideErr := pool.Exec(ctx,
		`UPDATE products SET status = 'draft' WHERE slug = $1`, product); hideErr != nil {
		t.Fatalf("unpublish: %v", hideErr)
	}
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is cancelled in Cleanup
		_, _ = pool.Exec(context.Background(),
			`UPDATE products SET status = 'active' WHERE slug = $1`, product)
	})

	after, err := s.Campaign(ctx, slug)
	if err != nil {
		t.Fatalf("campaign: %v", err)
	}
	if len(after.Products) != 0 {
		t.Errorf("%d products after the only one was unpublished; the page would "+
			"link to a 404", len(after.Products))
	}
}

func constraintName(err error) (code, name string) {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return "", ""
	}
	return pgErr.Code, pgErr.ConstraintName
}

func TestComparisonIsBoundedDeduplicatedAndForgiving(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool)
	slugs := activeSlugs(t, 5)

	tests := []struct {
		name string
		in   []string
		want int
	}{
		{"two products", slugs[:2], 2},
		{"the ceiling", slugs[:4], 4},
		{"past the ceiling", slugs, 4},
		{"a repeat is one column", []string{slugs[0], slugs[0], slugs[1]}, 2},
		{"an unknown slug is dropped", []string{slugs[0], "no-such-product", slugs[1]}, 2},
		{"nothing at all", nil, 0},
		{"only unknown slugs", []string{"nope", "also-nope"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view, err := s.Compare(ctx, tt.in)
			if err != nil {
				t.Fatalf("compare: %v", err)
			}
			if len(view.Products) != tt.want {
				got := make([]string, 0, len(view.Products))
				for _, p := range view.Products {
					got = append(got, p.Slug)
				}
				t.Errorf("%d columns %v, want %d", len(view.Products), got, tt.want)
			}
		})
	}
}

// Both locales: the Chinese page was always correct, so a fix that only moved
// the collision would pass a one-locale test.
func TestTwoSpecsThatShareATranslationStayTwoRows(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Two labels, one English word, hand-written: a seed a shop later
	// translates differently would silently stop exercising this.
	const setup = `
	INSERT INTO products (id, brand_id, category_id, slug, name, status, published_at)
	SELECT 'eeee0009-0000-4000-8000-000000000001', b.id, c.id, 'collide-spec', '同譯規格機', 'active', now()
	FROM brands b, categories c WHERE b.slug='pixelight' AND c.slug='phones';

	INSERT INTO product_variants (id, product_id, sku, price_cents, stock_quantity, safety_stock, position)
	VALUES ('eeee000a-0000-4000-8000-000000000001','eeee0009-0000-4000-8000-000000000001','COLLIDE-1',100000,9,2,80);

	INSERT INTO product_specs (product_id, label, label_en, value, value_en, position) VALUES
	  ('eeee0009-0000-4000-8000-000000000001', '輸出', 'Ports', '65W GaN',   '65W GaN',   1),
	  ('eeee0009-0000-4000-8000-000000000001', '孔位', 'Ports', 'USB-C x2',  'USB-C x2',  2);`
	if _, err := tx.Exec(ctx, setup); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	store := catalog.NewStore(tx)
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
	}{
		{name: "Chinese", locale: i18n.ZhHant},
		{name: "English", locale: i18n.En},
	} {
		t.Run(tt.name, func(t *testing.T) {
			view, err := store.Compare(i18n.WithLocale(ctx, tt.locale), []string{"collide-spec"})
			if err != nil {
				t.Fatalf("compare: %v", err)
			}

			// The values, not the row count: two rows both saying 65W GaN would
			// pass a count assertion.
			var got []string
			for _, row := range view.Rows {
				got = append(got, row.Values...)
			}
			for _, want := range []string{"65W GaN", "USB-C x2"} {
				if !slices.Contains(got, want) {
					t.Errorf("the comparison lost %q; it shows %v — two specs whose "+
						"labels share a translation collapsed into one row", want, got)
				}
			}
		})
	}
}

func TestTheColumnsKeepTheOrderTheURLNamed(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool)
	slugs := activeSlugs(t, 3)

	forward, err := s.Compare(ctx, slugs)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	reversed := []string{slugs[2], slugs[1], slugs[0]}
	backward, err := s.Compare(ctx, reversed)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}

	for i := range slugs {
		if forward.Products[i].Slug != slugs[i] {
			t.Errorf("column %d is %q, want %q", i, forward.Products[i].Slug, slugs[i])
		}
		if backward.Products[i].Slug != reversed[i] {
			t.Errorf("reversed column %d is %q, want %q",
				i, backward.Products[i].Slug, reversed[i])
		}
	}
}

func TestSharedSpecsComeFirstAndGapsAreVisible(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool)
	a, b := twoProductsWithSpecs(t)

	view, err := s.Compare(ctx, []string{a, b})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if len(view.Rows) < 2 {
		t.Fatalf("%d spec rows, want at least the shared one and the lone one",
			len(view.Rows))
	}

	if view.Rows[0].SharedBy < view.Rows[len(view.Rows)-1].SharedBy {
		t.Errorf("the first row is shared by %d products and the last by %d — "+
			"the rows worth comparing are not at the top",
			view.Rows[0].SharedBy, view.Rows[len(view.Rows)-1].SharedBy)
	}

	for _, row := range view.Rows {
		if len(row.Values) != len(view.Products) {
			t.Errorf("row %q has %d cells for %d products; the columns would "+
				"not line up", row.Label, len(row.Values), len(view.Products))
		}
	}

	var lone *pages.CompareRow
	for i := range view.Rows {
		if view.Rows[i].SharedBy == 1 {
			lone = &view.Rows[i]
			break
		}
	}
	if lone == nil {
		t.Fatal("no spec is carried by only one product; the fixture is not testing gaps")
	}
	blank := 0
	for i := range view.Products {
		if lone.Value(i) == "—" {
			blank++
		}
	}
	if blank != 1 {
		t.Errorf("a spec only one product states rendered %d blanks, want 1", blank)
	}
}

func activeSlugs(t *testing.T, n int) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT slug FROM products WHERE status = 'active' ORDER BY slug LIMIT $1`, n)
	if err != nil {
		t.Fatalf("find products: %v", err)
	}
	defer rows.Close()

	out := make([]string, 0, n)
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, slug)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(out) < n {
		t.Fatalf("only %d active products, need %d", len(out), n)
	}
	return out
}

// twoProductsWithSpecs builds a pair sharing one spec, each with one the other
// lacks.
func twoProductsWithSpecs(t *testing.T) (first, second string) {
	t.Helper()
	ctx := t.Context()
	for i, slug := range []*string{&first, &second} {
		*slug = "cmp-" + uuid.NewString()
		var productID uuid.UUID
		if err := pool.QueryRow(ctx, `
			WITH b AS (
				INSERT INTO brands (slug, name) VALUES ('cb-'||gen_random_uuid(), '比較品牌') RETURNING id
			), c AS (
				INSERT INTO categories (slug, name, position) SELECT 'cc-'||gen_random_uuid(), '比較分類', coalesce(max(position) + 1, 0) FROM categories WHERE parent_id IS NULL RETURNING id
			), p AS (
				INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
				SELECT b.id, c.id, $1, '比較測試商品', 'draft', now() FROM b, c RETURNING id
			), v AS (
				INSERT INTO product_variants (product_id, sku, price_cents)
				SELECT p.id, 'CMP-'||upper(replace(gen_random_uuid()::text,'-','')), 100000 FROM p
				RETURNING product_id
			)
			SELECT product_id FROM v`, *slug).Scan(&productID); err != nil {
			t.Fatalf("create product: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE products SET status = 'active' WHERE slug = $1`, *slug); err != nil {
			t.Fatalf("publish: %v", err)
		}
		// The lone label sorts alphabetically BEFORE the shared one: the other
		// way round, the case stays green with shared_by DESC deleted.
		if _, err := pool.Exec(ctx, `
			INSERT INTO product_specs (product_id, label, value, position)
			VALUES ($1, 'ZZ 共同規格', $2, 0), ($1, $3, '獨有的值', 1)`,
			productID, "值 "+uuid.NewString()[:4],
			"AA 獨有規格 "+uuid.NewString()[:4]); err != nil {
			t.Fatalf("create specs: %v", err)
		}
		_ = i
	}
	return first, second
}

// Matching only the localized column would make the catalogue searchable in one
// language at a time, which the shop cannot see because its language works.
func TestSearchFindsAProductByItsEnglishName(t *testing.T) {
	s := catalog.NewStore(pool)
	ctx := t.Context()

	view, err := s.Search(ctx, "%case%", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	var found bool
	for i := range view.Products {
		if view.Products[i].Slug == "pixelight-9-pro-case" {
			found = true
		}
	}
	if !found {
		names := make([]string, 0, len(view.Products))
		for i := range view.Products {
			names = append(names, view.Products[i].Slug)
		}
		t.Errorf("searching \"Case\" found %v, want pixelight-9-pro-case", names)
	}

	// The count is a second query with the same predicate; updating one and not
	// the other reports "3 results" above one row.
	if view.Total < int64(len(view.Products)) {
		t.Errorf("the page shows %d products and reports %d results",
			len(view.Products), view.Total)
	}
}

// TestADealsTileIsPricedOnTheDiscountedVariant holds two rules that agree on
// every product in the seed and are not the same rule.
//
// A product is on /deals when ANY active variant carries a discount. The tile's
// price came from the listing's LATERAL, which takes the cheapest BUYABLE
// variant — a different variant whenever the discounted one is dearer or out of
// stock. The sale page could then quote a price with no discount on it, and no
// badge beside it, because OnSale() compares the figures it was given.
//
// Measured against the dev seed, both orderings pick the same variant for all
// eight qualifying products: the defect is latent there, which is exactly what
// mistake #37 says a fixture where two rules agree is worth. This one is built
// so they disagree — the discounted variant is the DEARER one.
func TestADealsTileIsPricedOnTheDiscountedVariant(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool)

	var slug string
	if err := pool.QueryRow(ctx, `
		WITH b AS (SELECT id FROM brands LIMIT 1),
		     c AS (SELECT id FROM categories WHERE parent_id IS NULL LIMIT 1),
		     p AS (
		         INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		         SELECT b.id, c.id, 'deals-split-' || gen_random_uuid(), '折扣分岔測試',
		                'active', now()
		         FROM b, c RETURNING id, slug
		     ),
		     cheap AS (
		         INSERT INTO product_variants (product_id, sku, price_cents, stock_quantity, safety_stock, position)
		         SELECT p.id, 'SPLIT-CHEAP-' || upper(replace(gen_random_uuid()::text, '-', '')), 100000, 10, 0, 0 FROM p
		     ),
		     dear AS (
		         INSERT INTO product_variants
		             (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position)
		         SELECT p.id, 'SPLIT-DEAR-' || upper(replace(gen_random_uuid()::text, '-', '')), 150000, 300000, 10, 0, 1 FROM p
		     )
		SELECT slug FROM p`).Scan(&slug); err != nil {
		t.Fatalf("build a product whose discount is on the dearer variant: %v", err)
	}

	view, err := s.Deals(ctx, 1)
	if err != nil {
		t.Fatalf("deals: %v", err)
	}
	var tile *pages.ProductTile
	for i := range view.Products {
		if view.Products[i].Slug == slug {
			tile = &view.Products[i]
			break
		}
	}
	if tile == nil {
		t.Fatalf("the product is not on /deals at all, so this proved nothing")
	}

	// The DEARER variant, because it is the one carrying the discount that put
	// the product here. Pricing the cheaper one quotes NT$1,000 with no
	// markdown on a page that exists to show markdowns.
	if tile.PriceCents != 150000 {
		t.Errorf("the deals tile is priced at %d, want 150000 — the cheapest buyable "+
			"variant carries no discount, and this page is about discounts",
			tile.PriceCents)
	}
	if !tile.OnSale() {
		t.Error("a product on the sale page shows no sale badge, because the variant " +
			"it was priced on is not the one that is marked down")
	}
}
