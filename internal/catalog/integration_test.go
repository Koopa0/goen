//go:build integration

package catalog_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
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
	return getInLocale(t, i18n.ZhHant, target)
}

func getInLocale(t *testing.T, locale i18n.Locale, target string) (status int, body string) {
	t.Helper()
	h := catalog.NewHandler(catalog.NewStore(pool), slog.New(slog.DiscardHandler))
	req := httptest.NewRequestWithContext(
		i18n.WithLocale(t.Context(), locale),
		http.MethodGet, target, http.NoBody)
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

func headerSearchValue(html string) string {
	const marker = `id="site-search"`
	i := strings.Index(html, marker)
	if i < 0 {
		return ""
	}
	rest := html[i:]
	const attr = `value="`
	j := strings.Index(rest, attr)
	if j < 0 {
		return ""
	}
	rest = rest[j+len(attr):]
	k := strings.Index(rest, `"`)
	if k < 0 {
		return ""
	}
	return rest[:k]
}

// /c/accessories holds no products of its own; chargers and cases hold six
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

// tech > accessories > chargers is three levels: the department lists a product
// two levels down, and a department of the general-merchandise catalogue lists
// the products of its sub-categories.
func TestDepartmentListingReachesEveryLevel(t *testing.T) {
	for slug, name := range map[string]string{
		"tech":             "Aurora GaN 65W 充電器",
		"books-stationery": "山茶十二月",
		"food-drink":       "晨焙 咖啡豆",
	} {
		code, body := get(t, "/c/"+slug)
		if code != http.StatusOK {
			t.Fatalf("/c/%s status = %d, want 200", slug, code)
		}
		if !strings.Contains(body, name) {
			t.Errorf("/c/%s omits %q, a product below it", slug, name)
		}
	}
}

// A department's head offers its children as chips, from the department itself
// and from any sub-category under it, so a shopper in one sees the others.
func TestTheHeadOffersTheDepartmentsChildrenFromEveryPageUnderIt(t *testing.T) {
	// Tech is the department; accessories sits under it and chargers under that.
	for _, slug := range []string{"tech", "accessories", "chargers"} {
		view, err := catalog.NewStore(pool).Listing(i18n.WithLocale(t.Context(), i18n.ZhHant), slug, catalog.Filters{})
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(view.Theme.Children))
		for _, c := range view.Theme.Children {
			got = append(got, c.Slug)
		}
		want := []string{"phones", "laptops", "tablets", "audio", "wearables", "accessories"}
		if !slices.Equal(got, want) {
			t.Errorf("/c/%s offers %v, want %v", slug, got, want)
		}
	}
}

// A department holds no product itself, so the sitemap must judge it by the
// products below it.
func TestSitemapNamesADepartmentThatHoldsNoProductItself(t *testing.T) {
	rows, err := catalog.NewStore(pool).SitemapCategories(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	slugs := make([]string, 0, len(rows))
	for _, r := range rows {
		slugs = append(slugs, r.Slug)
	}
	for _, want := range []string{"tech", "accessories", "books-stationery", "food-drink"} {
		if !slices.Contains(slugs, want) {
			t.Errorf("sitemap categories = %v; missing %q", slugs, want)
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
	// The catalogue states "100%" in three products' text, so a bare % legitimately
	// finds exactly those; an unescaped one would find every product.
	code, body := get(t, "/search?q=%25")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	links := regexp.MustCompile(`class="goen-tile" href="/p/([^"]+)"`).FindAllStringSubmatch(body, -1)
	got := make([]string, 0, len(links))
	for _, m := range links {
		got = append(got, m[1])
	}
	slices.Sort(got)
	want := []string{"orili-cotton-tee", "orili-oxford-shirt", "restwood-linen-tea-towel"}
	if !slices.Equal(got, want) {
		t.Errorf("searching for a bare %% found %v, want only the products that contain a percent sign %v: the wildcard reached ILIKE unescaped", got, want)
	}

	// No product text holds an underscore, so an unescaped _ would still match
	// every product and an escaped one matches none.
	code, body = get(t, "/search?q=_")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !strings.Contains(body, "找不到") {
		t.Error("a search matching nothing did not render the empty state")
	}
}

func TestSearchPageKeepsTheHeaderInputInSyncWithTheHeading(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		t.Run(locale.Tag(), func(t *testing.T) {
			for _, tc := range []struct {
				name string
				q    string
				want string
			}{
				{"canonical term", "pixelight", "pixelight"},
				{"unicode trim", "\u00a0\u3000pixel\u2003", "pixel"},
				{"html specials", `a&b"c`, `a&amp;b&#34;c`},
				{
					"length cap",
					"\u00a0" + strings.Repeat("字", catalog.MaxQueryRunes+8) + "\u3000",
					strings.Repeat("字", catalog.MaxQueryRunes),
				},
			} {
				t.Run(tc.name, func(t *testing.T) {
					code, body := getInLocale(t, locale, "/search?q="+url.QueryEscape(tc.q))
					if code != http.StatusOK {
						t.Fatalf("status = %d, want 200", code)
					}
					if got := headerSearchValue(body); got != tc.want {
						t.Errorf("header search value = %q, want %q", got, tc.want)
					}
				})
			}
		})
	}
}

// Both scripts match by substring.
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
// things on top, which is a price list rather than a sale. Sellable products
// come first, and depth orders each group.
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

	last, soldOut := 2.0, false
	for i := range view.Products {
		tile := &view.Products[i]
		if !tile.InStock && !soldOut {
			soldOut, last = true, 2.0
		}
		if tile.InStock && soldOut {
			t.Errorf("product %d can be bought, after one that cannot", i)
		}
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

type sqlExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// featureNewProduct puts a new product with one discounted variant holding
// stock into the campaign; status is the product's status once featured.
func featureNewProduct(t *testing.T, db sqlExecer, campaignSlug string, stock int, status string) {
	t.Helper()
	ctx := t.Context()
	slug := "featured-" + uuid.NewString()
	if _, err := db.Exec(ctx, `
		WITH p AS (
		    INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		    SELECT (SELECT id FROM brands LIMIT 1),
		           (SELECT id FROM categories WHERE parent_id IS NULL LIMIT 1),
		           $1, '活動商品', 'active', now()
		    RETURNING id
		)
		INSERT INTO product_variants
		    (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position)
		SELECT p.id, upper(replace($1, '-', '')), 1000, 2000, $2, 0, 0 FROM p`, slug, stock); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO sale_campaign_products (campaign_id, product_id)
		SELECT c.id, p.id FROM sale_campaigns c, products p WHERE c.slug = $1 AND p.slug = $2`,
		campaignSlug, slug); err != nil {
		t.Fatalf("feature product: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE products SET status = $2 WHERE slug = $1`, slug, status); err != nil {
		t.Fatalf("set product status: %v", err)
	}
}

func listedSlugs(t *testing.T, s *catalog.Store) []string {
	t.Helper()
	view, err := s.ListedCampaigns(t.Context(), 1)
	if err != nil {
		t.Fatalf("running campaigns: %v", err)
	}
	out := make([]string, 0, len(view.Rows))
	for _, r := range view.Rows {
		out = append(out, r.Slug)
	}
	return out
}

// The shop lists a campaign only while it has a published featured product in
// stock; the same predicate serves the deals page and the home carousel. The campaign page itself stays reachable by direct link.
func TestACampaignIsListedOnlyWhileItHasSomethingToBuy(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool)
	tests := []struct {
		name   string
		stock  int
		status string
		listed bool
	}{
		{"nothing featured", -1, "", false},
		{"only sold out", 0, "active", false},
		{"only unpublished", 5, "draft", false},
		{"one sellable product", 5, "active", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug := campaign(t, "listed-"+uuid.NewString()[:8])
			if tt.stock >= 0 {
				featureNewProduct(t, pool, slug, tt.stock, tt.status)
			}
			if got := slices.Contains(listedSlugs(t, s), slug); got != tt.listed {
				t.Errorf("deals list holds %s = %v, want %v", slug, got, tt.listed)
			}
			if _, err := s.Campaign(ctx, slug); err != nil {
				t.Errorf("direct link to %s: %v, want it reachable", slug, err)
			}
		})
	}
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

func TestACampaignOutsideItsWindowIsShownAsNotStartedOrEnded(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool)

	running := campaign(t, "window-now")
	if err := feature(t, running, discountedSlug(t)); err != nil {
		t.Fatalf("feature: %v", err)
	}
	if view, err := s.Campaign(ctx, running); err != nil || view.Schedule.State != pages.CampaignRunning {
		t.Fatalf("a running campaign = state %q, %v", view.Schedule.State, err)
	}

	tests := []struct {
		name string
		// Spelled out: sale_campaigns_slug_format allows no spaces.
		slug  string
		setup string
		want  pages.CampaignState
	}{
		{"already ended", "window-ended", `UPDATE sale_campaigns SET starts_at = now() - interval '30 days',
			ends_at = now() - interval '1 day' WHERE slug = $1`, pages.CampaignEnded},
		{"not started yet", "window-future", `UPDATE sale_campaigns SET starts_at = now() + interval '1 day',
			ends_at = now() + interval '30 days' WHERE slug = $1`, pages.CampaignNotStarted},
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
			view, err := s.Campaign(ctx, slug)
			if err != nil {
				t.Fatalf("Campaign(%s) = %v, want the page", slug, err)
			}
			if view.Schedule.State != tt.want {
				t.Errorf("Campaign(%s) state = %q, want %q", slug, view.Schedule.State, tt.want)
			}
		})
	}

	off := campaign(t, "window-off")
	if _, err := pool.Exec(ctx, `UPDATE sale_campaigns SET is_active = false WHERE slug = $1`, off); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	if _, err := s.Campaign(ctx, off); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("Campaign of a switched-off campaign = %v, want ErrNotFound", err)
	}
}

// What can be bought comes before what cannot, however deep the sold-out
// markdown is. On /deals the tile shows the discounted variant, so a product
// whose discounted variant is sold out ranks as sold out even when a regular
// variant is in stock.
func TestSellableProductsComeBeforeSoldOutOnes(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
	s := catalog.NewStore(tx)

	deep := newDeal(t, tx, 100, 0)
	shallow := newDeal(t, tx, 950, 5)
	soldOutDiscount := newDeal(t, tx, 900, 0)
	if _, err = tx.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents, stock_quantity, safety_stock, position)
		SELECT id, upper(replace($1, '-', '')) || 'R', 1000, 5, 0, 1 FROM products WHERE slug = $1`, soldOutDiscount); err != nil {
		t.Fatalf("add regular variant: %v", err)
	}

	var slugs []string
	for page := 1; ; page++ {
		view, dealsErr := s.Deals(ctx, page)
		if dealsErr != nil {
			t.Fatalf("Deals(%d): %v", page, dealsErr)
		}
		for i := range view.Products {
			slugs = append(slugs, view.Products[i].Slug)
		}
		if int64(page*catalog.PageSize) >= view.Total {
			break
		}
	}
	at := func(slug string) int { return slices.Index(slugs, slug) }
	if at(shallow) < 0 || at(shallow) > at(deep) || at(shallow) > at(soldOutDiscount) {
		t.Errorf("Deals order: sellable %d, sold out %d and %d, want the sellable one first", at(shallow), at(deep), at(soldOutDiscount))
	}

	camp := "sellable-first-" + uuid.NewString()[:8]
	if _, err = tx.Exec(ctx, `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ($1, '測試活動', now() + interval '7 days')`, camp); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	// Positions: the sold-out one is first by the back office's order.
	for position, slug := range []string{deep, newDeal(t, tx, 800, 5), shallow} {
		if _, err = tx.Exec(ctx, `INSERT INTO sale_campaign_products (campaign_id, product_id, position)
			SELECT c.id, p.id, $3 FROM sale_campaigns c, products p WHERE c.slug = $1 AND p.slug = $2`, camp, slug, position); err != nil {
			t.Fatalf("feature %s: %v", slug, err)
		}
	}
	view, err := s.Campaign(ctx, camp)
	if err != nil {
		t.Fatalf("Campaign: %v", err)
	}
	stock := make([]bool, 0, len(view.Products))
	prices := make([]int64, 0, len(view.Products))
	for i := range view.Products {
		stock = append(stock, view.Products[i].InStock)
		prices = append(prices, view.Products[i].PriceCents)
	}
	if !slices.Equal(stock, []bool{true, true, false}) || !slices.Equal(prices[:2], []int64{800, 950}) {
		t.Errorf("Campaign products in stock %v at prices %v, want the sellable ones in position order, then the sold-out one", stock, prices)
	}
}

func newDeal(t *testing.T, db sqlExecer, price, stock int) string {
	t.Helper()
	slug := "deal-" + uuid.NewString()
	if _, err := db.Exec(t.Context(), `
		WITH p AS (
		    INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		    SELECT (SELECT id FROM brands LIMIT 1),
		           (SELECT id FROM categories WHERE parent_id IS NULL LIMIT 1),
		           $1, '特價商品', 'active', now()
		    RETURNING id
		)
		INSERT INTO product_variants
		    (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position)
		SELECT p.id, upper(replace($1, '-', '')), $2, 1000, $3, 0, 0 FROM p`, slug, price, stock); err != nil {
		t.Fatalf("create deal: %v", err)
	}
	return slug
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

// Both locales: only the English labels collide, so Chinese is the control.
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
			view, err := store.Compare(i18n.WithLocale(ctx, tt.locale), []string{"collide-spec", "pixelight-9-pro"})
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
// language at a time.
func TestSearchFindsAProductByItsEnglishName(t *testing.T) {
	s := catalog.NewStore(pool)
	ctx := t.Context()

	view, err := s.Search(ctx, "%case%", catalog.SortRelevance, 1)
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

// Specifications are searched in both languages, and a token present only in
// product_specs matches the product.
func TestSearchFindsAProductByItsSpecification(t *testing.T) {
	code, body := get(t, "/search?q=5000mAh")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !strings.Contains(body, "Pixelight 9 Pro 5G") {
		t.Errorf("searching spec value %q did not find %q", "5000mAh", "Pixelight 9 Pro 5G")
	}
}

// A product is on /deals when ANY active variant carries a discount, while a
// tile is priced on the cheapest BUYABLE variant — a different variant whenever
// the discounted one is dearer or sold out. Every product in the seed satisfies
// both rules with one variant, so this fixture puts the discount on the DEARER
// one.
func TestPromotionalTilesArePricedOnTheDiscountedVariant(t *testing.T) {
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
		     ),
		     -- A THIRD, dearer still and undiscounted: without it nothing is dearer
		     -- than the tile's price and the range claim is never exercised.
		     dearest AS (
		         INSERT INTO product_variants
		             (product_id, sku, price_cents, stock_quantity, safety_stock, position)
		         SELECT p.id, 'SPLIT-TOP-' || upper(replace(gen_random_uuid()::text, '-', '')), 200000, 10, 0, 2 FROM p
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

	if tile.PriceCents != 150000 {
		t.Errorf("the deals tile is priced at %d, want 150000 — the cheapest buyable "+
			"variant carries no discount, and this page is about discounts",
			tile.PriceCents)
	}
	if !tile.OnSale() {
		t.Error("a product on the sale page shows no sale badge, because the variant " +
			"it was priced on is not the one that is marked down")
	}

	// And it must NOT say 起: the discounted variant is the dearer one, so a
	// cheaper variant sits under the price the tile shows.
	if tile.PriceVaries {
		t.Error("the deals tile is marked as a range starting at this price, and a " +
			"cheaper variant exists — 起 on a price that is not the lowest")
	}

	// A campaign has the same admission rule as /deals: the product is present
	// because one active variant is discounted. It must not fall back to the
	// cheaper regular variant and erase the campaign's own markdown.
	var productID uuid.UUID
	if queryErr := pool.QueryRow(ctx, `SELECT id FROM products WHERE slug = $1`, slug).Scan(&productID); queryErr != nil {
		t.Fatalf("read campaign product: %v", queryErr)
	}
	campaignSlug := "discounted-tile-" + uuid.NewString()[:8]
	if _, execErr := pool.Exec(ctx, `
		WITH campaign AS (
			INSERT INTO sale_campaigns (slug, title, ends_at)
			VALUES ($1, '折扣變體活動', now() + interval '1 day')
			RETURNING id
		)
		INSERT INTO sale_campaign_products (campaign_id, product_id)
		SELECT id, $2 FROM campaign`, campaignSlug, productID); execErr != nil {
		t.Fatalf("feature split-price product: %v", execErr)
	}
	campaign, err := s.Campaign(ctx, campaignSlug)
	if err != nil {
		t.Fatalf("campaign: %v", err)
	}
	if len(campaign.Products) != 1 {
		t.Fatalf("campaign has %d products, want 1", len(campaign.Products))
	}
	campaignTile := campaign.Products[0]
	if campaignTile.PriceCents != 150000 || !campaignTile.OnSale() {
		t.Errorf("campaign tile = price %d/on-sale %t, want 150000/true; the regular "+
			"variant must not hide the discount", campaignTile.PriceCents, campaignTile.OnSale())
	}
	if campaignTile.PriceVaries {
		t.Error("campaign tile says its discounted price is the bottom of a range, but a cheaper variant exists")
	}
}

// The tile states a price, the "from" flag and the sale price of a variant the
// filters accepted; otherwise a product that qualifies through one variant shows
// another variant's price, outside the range the shopper asked for.
func TestListingTileShowsAVariantThePriceFiltersAccept(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, execErr := tx.Exec(ctx, `
		INSERT INTO products (id, brand_id, category_id, slug, name, status, published_at)
		SELECT 'eeee0005-0000-4000-8000-000000000001', b.id, c.id, 'tile-range', '價格區間機', 'active', now()
		FROM brands b, categories c WHERE b.slug='pixelight' AND c.slug='phones';

		INSERT INTO product_variants (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, position)
		VALUES ('eeee0005-0000-4000-8000-000000000001', 'RANGE-SOLDOUT', 300000, NULL, 0, 90),
		       ('eeee0005-0000-4000-8000-000000000001', 'RANGE-MID', 500000, NULL, 5, 91),
		       ('eeee0005-0000-4000-8000-000000000001', 'RANGE-HIGH', 1200000, 1500000, 5, 92);`,
	); execErr != nil {
		t.Fatalf("fixture: %v", execErr)
	}

	tests := []struct {
		name         string
		filters      catalog.Filters
		wantPrice    int64
		wantVaries   bool
		wantCompare  int64
		wantNotStale string
	}{
		{"unfiltered shows the cheapest buyable variant", catalog.Filters{}, 500000, true, 0, ""},
		{"min price shows the variant above it, not the cheaper one", catalog.Filters{MinPrice: 1000000}, 1200000, false, 1500000, "the cheaper in-range-excluded variant's price"},
		{"max price shows the sold-out variant that qualifies it", catalog.Filters{MaxPrice: 400000}, 300000, false, 0, "the dearer in-stock variant's price, above the maximum"},
		{"in stock and max price excludes dearer variants from the from flag", catalog.Filters{InStockOnly: true, MaxPrice: 600000}, 500000, false, 0, "a from flag for a variant above the maximum"},
	}
	store := catalog.NewStore(tx)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.filters.Page = 1
			view, listErr := store.Listing(ctx, "phones", tt.filters)
			if listErr != nil {
				t.Fatalf("listing: %v", listErr)
			}
			for _, p := range view.Products {
				if p.Slug != "tile-range" {
					continue
				}
				if p.PriceCents != tt.wantPrice || p.PriceVaries != tt.wantVaries || p.CompareCents != tt.wantCompare {
					t.Errorf("tile = price %d varies %t compare %d, want %d %t %d (not %s)",
						p.PriceCents, p.PriceVaries, p.CompareCents, tt.wantPrice, tt.wantVaries, tt.wantCompare, tt.wantNotStale)
				}
				return
			}
			t.Fatal("the product is missing from the filtered listing")
		})
	}
}

// Only the tech department compares in the seed. Phones sit one level down and
// inherit it; books sit one level down under a department that does not.
func TestOnlyTheDepartmentsThatCompareCarryTheBox(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool)
	for slug, want := range map[string]bool{"phones": true, "accessories": true, "books-stationery": false, "books": false} {
		view, err := s.Listing(ctx, slug, catalog.Filters{Page: 1})
		if err != nil {
			t.Fatalf("listing %s: %v", slug, err)
		}
		if len(view.Products) == 0 {
			t.Fatalf("%s lists nothing, so this proves nothing", slug)
		}
		for _, p := range view.Products {
			if p.Comparable != want {
				t.Errorf("%s: %s has Comparable = %v, want %v", slug, p.Slug, p.Comparable, want)
			}
		}
	}

	// A search crosses departments, so the box is decided per product.
	for _, c := range []struct {
		query, slug string
		want        bool
	}{
		{"Pixelight 9 Pro", "pixelight-9-pro", true},
		{"山茶十二月", "fernway-mountain-tea-seasons", false},
	} {
		view, err := s.Search(ctx, catalog.SearchPattern(c.query), catalog.SortRelevance, 1)
		if err != nil {
			t.Fatalf("search %q: %v", c.query, err)
		}
		var found bool
		for _, p := range view.Products {
			if p.Slug == c.slug {
				found = true
				if p.Comparable != c.want {
					t.Errorf("search %q: %s has Comparable = %v, want %v", c.query, p.Slug, p.Comparable, c.want)
				}
			}
		}
		if !found {
			t.Errorf("search %q does not find %s", c.query, c.slug)
		}
	}
}

// The answer is the nearest one up the trail, so turning a department off turns
// its shelves off with it, and turning another on reaches its sub-categories.
func TestComparisonFollowsTheDepartmentDownItsTrail(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `
		UPDATE categories SET comparable = false WHERE slug = 'tech';
		UPDATE categories SET comparable = true WHERE slug = 'books-stationery';`); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	s := catalog.NewStore(tx)
	for slug, want := range map[string]bool{"phones": false, "chargers": false, "books": true, "stationery": true} {
		view, listErr := s.Listing(ctx, slug, catalog.Filters{Page: 1})
		if listErr != nil {
			t.Fatalf("listing %s: %v", slug, listErr)
		}
		if len(view.Products) == 0 {
			t.Fatalf("%s lists nothing", slug)
		}
		for _, p := range view.Products {
			if p.Comparable != want {
				t.Errorf("%s: %s has Comparable = %v, want %v", slug, p.Slug, p.Comparable, want)
			}
		}
	}
}

// With nothing chosen, the comparison page points at a department that offers
// the box to tick, not at the home page, and at none when no department does.
func TestAnEmptyComparisonPointsAtADepartmentThatOffersIt(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `
		UPDATE categories SET comparable = NULL;
		UPDATE categories SET comparable = true WHERE slug = 'books-stationery';`); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	s := catalog.NewStore(tx)
	view, err := s.Compare(ctx, nil)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if view.StartSlug != "books-stationery" || view.StartHref() != "/c/books-stationery" {
		t.Errorf("start = %q (%q), want the one department that offers comparison", view.StartSlug, view.StartHref())
	}

	if _, err = tx.Exec(ctx, `UPDATE categories SET comparable = NULL`); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if view, err = s.Compare(ctx, nil); err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if view.StartSlug != "" || view.StartHref() != "/" {
		t.Errorf("start = %q (%q) with no department offering comparison, want none and the home page", view.StartSlug, view.StartHref())
	}
}

// A comparison of one offers the others on its own shelf, nearest in price
// first, at most six, never itself and never a product from another shelf.
func TestAComparisonOfOneSuggestsItsShelfNearestPriceFirst(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// Eight more phones at known prices around the chosen one, a draft one that
	// must not be offered, and one with no active variant.
	if _, err = tx.Exec(ctx, `
		INSERT INTO products (id, brand_id, category_id, slug, name, status, published_at)
		SELECT ('eeee0070-0000-4000-8000-0000000000' || lpad(n::text, 2, '0'))::uuid, b.id, c.id,
		       'near-' || n, '相近 ' || n, CASE WHEN n = 8 THEN 'draft' ELSE 'active' END, now()
		FROM generate_series(1, 9) n, brands b, categories c
		WHERE b.slug = 'pixelight' AND c.slug = 'phones';
		INSERT INTO product_variants (product_id, sku, price_cents, stock_quantity, position)
		SELECT p.id, 'NEAR-' || substr(p.slug, 6), 3390000 + (substr(p.slug, 6)::int * 1000), 5, 70
		FROM products p WHERE p.slug LIKE 'near-%' AND p.slug <> 'near-9';`); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	s := catalog.NewStore(tx)

	view, err := s.Compare(ctx, []string{"pixelight-9-pro"})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if len(view.Products) != 1 || view.ShelfSlug != "phones" {
		t.Fatalf("products = %+v, want the one chosen, on the phones shelf", view.Products)
	}
	if n := len(view.Suggestions); n != 6 {
		t.Fatalf("%d suggestions, want 6 of the nine others on the shelf", n)
	}
	anchor := view.Products[0].PriceCents
	var last int64 = -1
	for _, sg := range view.Suggestions {
		if sg.Slug == "pixelight-9-pro" || sg.Slug == "near-8" || sg.Slug == "near-9" {
			t.Errorf("%s is offered, and must not be", sg.Slug)
		}
		d := sg.PriceCents - anchor
		if d < 0 {
			d = -d
		}
		if d < last {
			t.Errorf("%s is %d from the anchor after one %d away: not nearest first", sg.Slug, d, last)
		}
		last = d
	}

	// Naming the nearest already leaves it out, and two products offer nothing.
	again, err := s.Compare(ctx, []string{"pixelight-9-pro", view.Suggestions[0].Slug})
	if err != nil {
		t.Fatalf("compare two: %v", err)
	}
	if len(again.Suggestions) != 0 {
		t.Errorf("a comparison of two offers %d suggestions", len(again.Suggestions))
	}

	// Another shelf offers its own: a laptop is never suggested for a phone.
	laptop, err := s.Compare(ctx, []string{"meridian-book-14"})
	if err != nil {
		t.Fatalf("compare laptop: %v", err)
	}
	if len(laptop.Suggestions) == 0 {
		t.Fatal("a laptop has no suggestions, though another laptop is on its shelf")
	}
	for _, sg := range laptop.Suggestions {
		// The laptops shelf in the seed besides the one chosen.
		if sg.Slug != "meridian-book-16-pro" && sg.Slug != "meridian-book-13" {
			t.Errorf("%s is suggested for a laptop, off its shelf", sg.Slug)
		}
	}
}
