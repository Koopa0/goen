//go:build integration

package campaigns_test

import (
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/web"
)

// The shop's list, its count and the back office's reason must agree on which
// campaign has something to buy.
func TestTheShopListCountAndBackOfficeAgreeOnWhatIsBuyable(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	shop := catalog.NewStore(pool)
	office := campaigns.NewStore(pool)

	tests := []struct {
		name     string
		status   string
		stock    int
		safety   int
		inactive bool
		plain    bool // an active full-price variant in stock beside the discounted one
		want     bool
	}{
		{"sellable", "active", 6, 5, false, false, true},
		{"at safety stock", "active", 5, 5, false, false, false},
		{"unpublished", "draft", 6, 5, false, false, false},
		{"only an inactive variant in stock", "active", 0, 0, true, false, false},
		{"only full-price stock", "active", 0, 0, false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, err := shop.ListedCampaigns(ctx, 1)
			if err != nil {
				t.Fatalf("read the shop list: %v", err)
			}
			slug := admintest.CampaignSlug(t)
			if _, execErr := pool.Exec(ctx, `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ($1, '測試活動', now() + interval '7 days')`, slug); execErr != nil {
				t.Fatalf("create campaign: %v", execErr)
			}
			product := "listed-" + uuid.NewString()
			if _, execErr := pool.Exec(ctx, `
				WITH p AS (
				    INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
				    SELECT (SELECT id FROM brands LIMIT 1),
				           (SELECT id FROM categories WHERE parent_id IS NULL LIMIT 1),
				           $1, '活動商品', 'active', now()
				    RETURNING id
				)
				INSERT INTO product_variants
				    (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, is_active, position)
				SELECT p.id, upper(replace($1, '-', '')) || 'A', 1000, 2000, $2, $3, true, 0 FROM p`,
				product, tt.stock, tt.safety); execErr != nil {
				t.Fatalf("create product: %v", execErr)
			}
			if tt.inactive {
				if _, execErr := pool.Exec(ctx, `
					INSERT INTO product_variants (product_id, sku, price_cents, stock_quantity, safety_stock, is_active, position)
					SELECT id, upper(replace($1, '-', '')) || 'B', 1000, 10, 0, false, 1 FROM products WHERE slug = $1`, product); execErr != nil {
					t.Fatalf("create inactive variant: %v", execErr)
				}
			}
			if tt.plain {
				if _, execErr := pool.Exec(ctx, `
					INSERT INTO product_variants (product_id, sku, price_cents, stock_quantity, safety_stock, is_active, position)
					SELECT id, upper(replace($1, '-', '')) || 'C', 1000, 10, 0, true, 2 FROM products WHERE slug = $1`, product); execErr != nil {
					t.Fatalf("create full-price variant: %v", execErr)
				}
			}
			if _, execErr := pool.Exec(ctx, `
				INSERT INTO sale_campaign_products (campaign_id, product_id)
				SELECT c.id, p.id FROM sale_campaigns c, products p WHERE c.slug = $1 AND p.slug = $2`, slug, product); execErr != nil {
				t.Fatalf("feature product: %v", execErr)
			}
			if _, execErr := pool.Exec(ctx, `UPDATE products SET status = $2 WHERE slug = $1`, product, tt.status); execErr != nil {
				t.Fatalf("set status: %v", execErr)
			}

			after, err := shop.ListedCampaigns(ctx, 1)
			if err != nil {
				t.Fatalf("read the shop list: %v", err)
			}
			if got := after.Total - before.Total; (got == 1) != tt.want {
				t.Errorf("ListedCampaignsCount rose by %d, want listed = %v", got, tt.want)
			}
			listed := false
			for page := 1; page <= after.Pages() && !listed; page++ {
				view, listErr := shop.ListedCampaigns(ctx, page)
				if listErr != nil {
					t.Fatalf("read the shop list page %d: %v", page, listErr)
				}
				for _, r := range view.Rows {
					listed = listed || r.Slug == slug
				}
			}
			if listed != tt.want {
				t.Errorf("ListedCampaigns holds the campaign = %v, want %v", listed, tt.want)
			}
			detail, err := office.Detail(ctx, slug)
			if err != nil {
				t.Fatalf("read the back-office detail: %v", err)
			}
			if detail.Sellable != tt.want {
				t.Errorf("AdminCampaign sellable = %v, want %v", detail.Sellable, tt.want)
			}
			if got := listRowSellable(t, office, slug); got != tt.want {
				t.Errorf("AdminCampaigns sellable = %v, want %v", got, tt.want)
			}
		})
	}
}

func listRowSellable(t *testing.T, office *campaigns.Store, slug string) bool {
	t.Helper()
	var after []string
	for {
		view, err := office.List(t.Context(), after...)
		if err != nil {
			t.Fatalf("read the back-office list: %v", err)
		}
		for _, r := range view.Rows {
			if r.Slug == slug {
				return r.Sellable
			}
		}
		next, err := url.Parse(view.Next)
		if err != nil || view.Next == "" {
			t.Fatalf("campaign %s is not in the back-office list (next %q, %v)", slug, view.Next, err)
		}
		after = []string{next.Query().Get(web.KeysetParam)}
	}
}
