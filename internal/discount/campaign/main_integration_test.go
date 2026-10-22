//go:build integration

package campaign_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/tile"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p

	seed, err := os.ReadFile("../../../seed/dev_catalog.sql")
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

func dealSlugs(t *testing.T, s *campaign.Store) (slugs []string, total int64) {
	t.Helper()
	for page := 1; page <= 50; page++ {
		view, err := s.Deals(t.Context(), page)
		if err != nil {
			t.Fatalf("deals page %d: %v", page, err)
		}
		total = view.Total
		if len(view.Products) == 0 {
			break
		}
		for i := range view.Products {
			slugs = append(slugs, view.Products[i].Slug)
		}
	}
	return slugs, total
}

// TestStore_DealsRunningOnly: Deals lists only what a running campaign
// features.
func TestStore_DealsRunningOnly(t *testing.T) {
	s := campaign.NewStore(pool)
	_, before := dealSlugs(t, s)

	product := discountedProduct(t)
	slugs, total := dealSlugs(t, s)
	if slices.Contains(slugs, product) || total != before {
		t.Errorf("a discounted product in no campaign is on the deals page (listed %v, total %d, was %d)",
			slices.Contains(slugs, product), total, before)
	}

	slug := runningCampaign(t, "deals-"+uuid.NewString()[:8])
	if err := feature(t, slug, product); err != nil {
		t.Fatalf("feature: %v", err)
	}
	slugs, total = dealSlugs(t, s)
	if !slices.Contains(slugs, product) || total != before+1 {
		t.Errorf("a product of a running campaign with stock is missing from the deals page (listed %v, total %d, want %d)",
			slices.Contains(slugs, product), total, before+1)
	}

	if _, err := pool.Exec(t.Context(), `UPDATE sale_campaigns SET starts_at = now() - interval '30 days',
		ends_at = now() - interval '1 day' WHERE slug = $1`, slug); err != nil {
		t.Fatalf("end campaign: %v", err)
	}
	slugs, total = dealSlugs(t, s)
	if slices.Contains(slugs, product) || total != before {
		t.Errorf("a product of an ended campaign is on the deals page (listed %v, total %d, was %d)",
			slices.Contains(slugs, product), total, before)
	}
}

// By fraction, not amount: ordering by absolute saving puts the expensive
// things on top, which is a price list rather than a sale. Sellable products
// come first, and depth orders each group.
func TestDealsAreOrderedByHowDeepTheCutIs(t *testing.T) {
	ctx := t.Context()
	s := campaign.NewStore(pool)

	view, err := s.Deals(ctx, 1)
	if err != nil {
		t.Fatalf("deals: %v", err)
	}
	if len(view.Products) < 2 {
		t.Skipf("only %d deals; ordering cannot be observed", len(view.Products))
	}

	last, soldOut := 2.0, false
	for i := range view.Products {
		deal := &view.Products[i]
		if !deal.InStock && !soldOut {
			soldOut, last = true, 2.0
		}
		if deal.InStock && soldOut {
			t.Errorf("product %d can be bought, after one that cannot", i)
		}
		if deal.CompareCents <= 0 {
			continue
		}
		cut := float64(deal.CompareCents-deal.PriceCents) / float64(deal.CompareCents)
		if cut > last+1e-9 {
			t.Errorf("product %d is %.0f%% off, after one at %.0f%% — the deepest "+
				"cut is not first", i, cut*100, last*100)
		}
		last = cut
	}
}

// runningCampaign creates a running campaign. Its window opens an hour ago rather than
// at the column default now(): the page judges the window with the host's clock,
// and a database that runs a millisecond ahead of the host (Docker Desktop's VM
// does) would otherwise read a campaign made a moment ago as not started.
func runningCampaign(t *testing.T, slug string) string {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO sale_campaigns (slug, title, starts_at, ends_at)
		VALUES ($1, '測試活動', now() - interval '1 hour', now() + interval '7 days')`, slug); err != nil {
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
func featureNewProduct(t *testing.T, exec sqlExecer, campaignSlug string, stock int, status string) {
	t.Helper()
	ctx := t.Context()
	slug := "featured-" + uuid.NewString()
	if _, err := exec.Exec(ctx, `
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
	if _, err := exec.Exec(ctx, `
		INSERT INTO sale_campaign_products (campaign_id, product_id)
		SELECT c.id, p.id FROM sale_campaigns c, products p WHERE c.slug = $1 AND p.slug = $2`,
		campaignSlug, slug); err != nil {
		t.Fatalf("feature product: %v", err)
	}
	if _, err := exec.Exec(ctx, `UPDATE products SET status = $2 WHERE slug = $1`, slug, status); err != nil {
		t.Fatalf("set product status: %v", err)
	}
}

// listedSlugs reads every page: other tests commit listed campaigns, so one
// page of ListedPageSize rows need not hold the campaign under test.
func listedSlugs(t *testing.T, s *campaign.Store) []string {
	t.Helper()
	var out []string
	for page, last := 1, 1; page <= last; page++ {
		view, err := s.Listed(t.Context(), page)
		if err != nil {
			t.Fatalf("running campaigns page %d: %v", page, err)
		}
		last = int((view.Total + campaign.ListedPageSize - 1) / campaign.ListedPageSize)
		for _, r := range view.Rows {
			out = append(out, r.Slug)
		}
	}
	return out
}

// TestStore_ListedSomethingToBuy: a campaign is listed only while it has
// something to buy. The shop lists a campaign only while it has a published
// featured product in stock; the same predicate serves the deals page and the
// home carousel. The campaign page itself stays reachable by direct link.
func TestStore_ListedSomethingToBuy(t *testing.T) {
	ctx := t.Context()
	s := campaign.NewStore(pool)
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
			slug := runningCampaign(t, "listed-"+uuid.NewString()[:8])
			t.Cleanup(func() {
				_, _ = pool.Exec(context.WithoutCancel(ctx), `UPDATE sale_campaigns SET is_active = false WHERE slug = $1`, slug)
			})
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

// TestStateAtFixtureClock: a fixture campaign runs whatever the host clock
// reads. The state of a campaign is judged with the host's clock, the window's
// edges come from the database's, and the two may differ by a millisecond or
// more. A campaign the fixture means to be running must not depend on which is
// ahead: judged with a host clock behind the database's at the moment of
// creation, it is still running.
func TestStateAtFixtureClock(t *testing.T) {
	slug := runningCampaign(t, "window-skew-"+uuid.NewString())
	var startsAt, endsAt, createdAt time.Time
	if err := pool.QueryRow(t.Context(),
		`SELECT starts_at, ends_at, created_at FROM sale_campaigns WHERE slug = $1`, slug,
	).Scan(&startsAt, &endsAt, &createdAt); err != nil {
		t.Fatalf("read campaign: %v", err)
	}
	for _, behind := range []time.Duration{0, 2 * time.Millisecond, time.Second} {
		hostNow := createdAt.Add(-behind)
		if got := campaign.StateAt(startsAt, endsAt, hostNow); got != campaign.Running {
			t.Errorf("a campaign created at %v, judged by a host clock %v behind, is %q, want running",
				createdAt, behind, got)
		}
	}
}

// TestStore_CampaignWindow: a campaign outside its window is shown as not
// started or ended.
func TestStore_CampaignWindow(t *testing.T) {
	ctx := t.Context()
	s := campaign.NewStore(pool)

	suffix := "-" + uuid.NewString()

	running := runningCampaign(t, "window-now"+suffix)
	if err := feature(t, running, discountedSlug(t)); err != nil {
		t.Fatalf("feature: %v", err)
	}
	// What the fact line leads with, and what it notes on the end, says which
	// state campaign.NewSchedule laid the page out for.
	if view, err := s.Campaign(ctx, running); err != nil || view.Schedule.Facts[0].Label != i18n.T(ctx, i18n.KeySlideItems) {
		t.Fatalf("a running campaign = facts %+v, %v; want its item count first", view.Schedule.Facts, err)
	}

	tests := []struct {
		name string
		// Spelled out: sale_campaigns_slug_format allows no spaces.
		slug      string
		setup     string
		wantFirst i18n.Key
		wantNote  string
	}{
		{"already ended", "window-ended" + suffix, `UPDATE sale_campaigns SET starts_at = now() - interval '30 days',
			ends_at = now() - interval '1 day' WHERE slug = $1`, i18n.KeySlideEnds, i18n.T(ctx, i18n.KeyCampaignEnded)},
		{"not started yet", "window-future" + suffix, `UPDATE sale_campaigns SET starts_at = now() + interval '1 day',
			ends_at = now() + interval '30 days' WHERE slug = $1`, i18n.KeyCampaignStarts, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug := runningCampaign(t, tt.slug)
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
			if first := view.Schedule.Facts[0].Label; first != i18n.T(ctx, tt.wantFirst) || view.Schedule.Ends.Note != tt.wantNote {
				t.Errorf("Campaign(%s) leads with %q and notes the end %q, want %q and %q",
					slug, first, view.Schedule.Ends.Note, i18n.T(ctx, tt.wantFirst), tt.wantNote)
			}
		})
	}

	off := runningCampaign(t, "window-off"+suffix)
	if _, err := pool.Exec(ctx, `UPDATE sale_campaigns SET is_active = false WHERE slug = $1`, off); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	if _, err := s.Campaign(ctx, off); !errors.Is(err, campaign.ErrNotFound) {
		t.Errorf("Campaign of a switched-off campaign = %v, want ErrNotFound", err)
	}
}

// TestStore_SellableBeforeSoldOut: sellable products come before sold-out ones.
// What can be bought comes before what cannot, however deep the sold-out
// markdown is. On /deals the tile shows the discounted variant, so a product
// whose discounted variant is sold out ranks as sold out even when a regular
// variant is in stock.
func TestStore_SellableBeforeSoldOut(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Rollback(ctx, tx)
	s := campaign.NewStore(tx)

	deep := newDeal(t, tx, 100, 0)
	shallow := newDeal(t, tx, 950, 5)
	soldOutDiscount := newDeal(t, tx, 900, 0)
	if _, err = tx.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents, stock_quantity, safety_stock, position)
		SELECT id, upper(replace($1, '-', '')) || 'R', 1000, 5, 0, 1 FROM products WHERE slug = $1`, soldOutDiscount); err != nil {
		t.Fatalf("add regular variant: %v", err)
	}

	dealsCamp := "deals-sellable-" + uuid.NewString()[:8]
	if _, err = tx.Exec(ctx, `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ($1, '測試活動', now() + interval '7 days')`, dealsCamp); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	for position, slug := range []string{deep, shallow, soldOutDiscount} {
		if _, err = tx.Exec(ctx, `INSERT INTO sale_campaign_products (campaign_id, product_id, position)
			SELECT c.id, p.id, $3 FROM sale_campaigns c, products p WHERE c.slug = $1 AND p.slug = $2`, dealsCamp, slug, position); err != nil {
			t.Fatalf("feature %s: %v", slug, err)
		}
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
		if int64(page*campaign.DealsPageSize) >= view.Total {
			break
		}
	}
	at := func(slug string) int { return slices.Index(slugs, slug) }
	if at(shallow) < 0 || at(deep) >= 0 || at(soldOutDiscount) >= 0 {
		t.Errorf("Deals lists: sellable %d, sold out %d and %d, want only the sellable one", at(shallow), at(deep), at(soldOutDiscount))
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

func newDeal(t *testing.T, exec sqlExecer, price, stock int) string {
	t.Helper()
	slug := "deal-" + uuid.NewString()
	if _, err := exec.Exec(t.Context(), `
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

// TestSaleCampaignNeedsDiscount: only discounted products can be featured: the
// sale_campaign_needs_discount trigger refuses a product with nothing marked
// down. It takes a lock on the product before it reads the variants.
func TestSaleCampaignNeedsDiscount(t *testing.T) {
	slug := runningCampaign(t, "only-discounted-"+uuid.NewString()[:8])

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

// TestStore_CampaignActiveProducts: a campaign shows only active products.
func TestStore_CampaignActiveProducts(t *testing.T) {
	ctx := t.Context()
	s := campaign.NewStore(pool)
	slug := runningCampaign(t, "active-only-"+uuid.NewString()[:8])
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

// TestStore_TilesDiscountedVariant: promotional tiles are priced on the
// discounted variant. The tile shows a discounted variant that can be bought,
// which is a different variant from the cheapest buyable one whenever the
// discount sits on the dearer variant. Every product in the seed has the
// discount on its cheapest variant, so this fixture puts it on the DEARER one.
func TestStore_TilesDiscountedVariant(t *testing.T) {
	ctx := t.Context()
	s := campaign.NewStore(pool)

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
	if err := feature(t, runningCampaign(t, "split-"+uuid.NewString()[:8]), slug); err != nil {
		t.Fatalf("feature the product: %v", err)
	}

	view, err := s.Deals(ctx, 1)
	if err != nil {
		t.Fatalf("deals: %v", err)
	}
	var deal *tile.Tile
	for i := range view.Products {
		if view.Products[i].Slug == slug {
			deal = &view.Products[i]
			break
		}
	}
	if deal == nil {
		t.Fatalf("the product is not on /deals at all, so this proved nothing")
	}

	if deal.PriceCents != 150000 {
		t.Errorf("the deals tile is priced at %d, want 150000 — the cheapest buyable "+
			"variant carries no discount, and this page is about discounts",
			deal.PriceCents)
	}
	if !deal.OnSale {
		t.Error("a product on the sale page shows no sale badge, because the variant " +
			"it was priced on is not the one that is marked down")
	}

	// And it must NOT say 起: the discounted variant is the dearer one, so a
	// cheaper variant sits under the price the tile shows.
	if deal.PriceVaries {
		t.Error("the deals tile is marked as a range starting at this price, and a " +
			"cheaper variant exists — 起 on a price that is not the lowest")
	}

	// The campaign shelf must not fall back to the cheaper regular variant and
	// erase the campaign's own markdown.
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
	campaignView, err := s.Campaign(ctx, campaignSlug)
	if err != nil {
		t.Fatalf("campaign: %v", err)
	}
	if len(campaignView.Products) != 1 {
		t.Fatalf("campaign has %d products, want 1", len(campaignView.Products))
	}
	campaignTile := campaignView.Products[0]
	if campaignTile.PriceCents != 150000 || !campaignTile.OnSale {
		t.Errorf("campaign tile = price %d/on-sale %t, want 150000/true; the regular "+
			"variant must not hide the discount", campaignTile.PriceCents, campaignTile.OnSale)
	}
	if campaignTile.PriceVaries {
		t.Error("campaign tile says its discounted price is the bottom of a range, but a cheaper variant exists")
	}
}

// discountedProduct creates an active product with one discounted variant in stock,
// featured by no campaign.
func discountedProduct(t *testing.T) string {
	t.Helper()
	slug := "discounted-" + uuid.NewString()
	if _, err := pool.Exec(t.Context(), `
		WITH p AS (
		    INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		    SELECT (SELECT id FROM brands LIMIT 1),
		           (SELECT id FROM categories WHERE parent_id IS NULL LIMIT 1),
		           $1, '折扣商品', 'active', now()
		    RETURNING id
		)
		INSERT INTO product_variants
		    (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position)
		SELECT p.id, upper(replace($1, '-', '')), 1000, 2000, 5, 0, 0 FROM p`, slug); err != nil {
		t.Fatalf("create product: %v", err)
	}
	return slug
}

// TestStore_NoStrikeSoldOutVariant: a card never strikes a price of a sold-out
// variant. A card strikes only a price the shopper can pay: a product whose
// discounted variant is sold out, with another in stock at full price, shows
// the full price unstruck on /deals and on its campaign page, and /deals does
// not list it.
func TestStore_NoStrikeSoldOutVariant(t *testing.T) {
	ctx := t.Context()
	s := campaign.NewStore(pool)

	slug := "split-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `
		WITH p AS (
		    INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		    SELECT (SELECT id FROM brands LIMIT 1),
		           (SELECT id FROM categories WHERE parent_id IS NULL LIMIT 1),
		           $1, '售完折扣測試', 'active', now()
		    RETURNING id
		)
		INSERT INTO product_variants
		    (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position)
		SELECT p.id, 'SOLD-' || upper(replace($1, '-', '')), 100000, 200000, 0, 0, 0 FROM p
		UNION ALL
		SELECT p.id, 'FULL-' || upper(replace($1, '-', '')), 150000, NULL, 10, 0, 1 FROM p`, slug); err != nil {
		t.Fatalf("create product: %v", err)
	}
	campaignSlug := runningCampaign(t, "soldout-"+uuid.NewString()[:8])
	if err := feature(t, campaignSlug, slug); err != nil {
		t.Fatalf("feature: %v", err)
	}
	// Another product keeps the campaign something to buy.
	if _, err := pool.Exec(ctx, `INSERT INTO sale_campaign_products (campaign_id, product_id, position)
		SELECT c.id, p.id, 1 FROM sale_campaigns c, products p WHERE c.slug = $1 AND p.slug = $2`, campaignSlug, discountedProduct(t)); err != nil {
		t.Fatalf("feature the other product: %v", err)
	}

	// A product with an undiscounted variant (no compare price) and a discounted
	// one, both in stock, must show the discounted one struck.
	mixed := "mixed-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `
		WITH p AS (
		    INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		    SELECT (SELECT id FROM brands LIMIT 1),
		           (SELECT id FROM categories WHERE parent_id IS NULL LIMIT 1),
		           $1, '混合折扣測試', 'active', now()
		    RETURNING id
		)
		INSERT INTO product_variants
		    (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position)
		SELECT p.id, 'PLAIN-' || upper(replace($1, '-', '')), 100000, NULL, 10, 0, 0 FROM p
		UNION ALL
		SELECT p.id, 'DISC-' || upper(replace($1, '-', '')), 150000, 300000, 10, 0, 1 FROM p`, mixed); err != nil {
		t.Fatalf("create mixed product: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sale_campaign_products (campaign_id, product_id, position)
		SELECT c.id, p.id, 2 FROM sale_campaigns c, products p WHERE c.slug = $1 AND p.slug = $2`, campaignSlug, mixed); err != nil {
		t.Fatalf("feature the mixed product: %v", err)
	}

	slugs, _ := dealSlugs(t, s)
	if slices.Contains(slugs, slug) {
		t.Error("/deals lists a product whose only discounted variant is sold out")
	}

	view, err := s.Campaign(ctx, campaignSlug)
	if err != nil {
		t.Fatalf("campaign: %v", err)
	}
	var sawSoldOut, sawMixed bool
	for i := range view.Products {
		switch card := &view.Products[i]; card.Slug {
		case slug:
			sawSoldOut = true
			if card.PriceCents != 150000 || card.OnSale {
				t.Errorf("campaign card: price %d, on sale %v; want the buyable NT$1,500 unstruck", card.PriceCents, card.OnSale)
			}
		case mixed:
			sawMixed = true
			if card.PriceCents != 150000 || !card.OnSale || card.CompareCents != 300000 {
				t.Errorf("campaign card of a product with a plain and a discounted variant in stock: price %d, was %d, on sale %v; want NT$1,500 struck from NT$3,000",
					card.PriceCents, card.CompareCents, card.OnSale)
			}
		}
	}
	if !sawSoldOut || !sawMixed {
		t.Error("a product is missing from its campaign page")
	}
	for page := 1; page <= 50; page++ {
		deals, dealsErr := s.Deals(ctx, page)
		if dealsErr != nil {
			t.Fatalf("deals page %d: %v", page, dealsErr)
		}
		if len(deals.Products) == 0 {
			break
		}
		for i := range deals.Products {
			if card := &deals.Products[i]; card.Slug == mixed {
				if card.PriceCents != 150000 || !card.OnSale {
					t.Errorf("deals card of a product with a plain and a discounted variant in stock: price %d, on sale %v; want NT$1,500 struck", card.PriceCents, card.OnSale)
				}
				return
			}
		}
	}
	t.Error("the mixed product is not on /deals")
}
