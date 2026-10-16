//go:build integration

package campaigns_test

import (
	"testing"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/admin/products/productstest"
)

func TestTheCampaignProductSearchOffersWhatIsNotFeaturedYet(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug := admintest.CampaignSlug(t)
	if _, err := campaigns.NewStore(pool).Create(ctx, &campaigns.Form{Slug: slug, Title: "測試活動", Days: 7}); err != nil {
		t.Fatal(err)
	}
	featured := admintest.DiscountedProductSlug(t, pool)
	if err := campaigns.NewStore(pool).FeatureProduct(ctx, slug, featured); err != nil {
		t.Fatal(err)
	}
	fresh := productstest.Draft(t, ctx, pool, s)

	got, err := campaigns.NewStore(pool).SearchProducts(ctx, slug, fresh)
	if err != nil || len(got) != 1 || got[0].Slug != fresh {
		t.Fatalf("search by slug = %v %v, want exactly %s", got, err, fresh)
	}
	if byName, searchErr := campaigns.NewStore(pool).SearchProducts(ctx, slug, "規格表測試 "+fresh); searchErr != nil || len(byName) != 1 {
		t.Errorf("search by name = %v %v, want the one product", byName, searchErr)
	}
	// A slug is a substring of its siblings' (pixelight-9-pro, pixelight-9-pro-case),
	// so the search may offer those; only the featured product itself is refused.
	again, searchErr := campaigns.NewStore(pool).SearchProducts(ctx, slug, featured)
	if searchErr != nil {
		t.Fatal(searchErr)
	}
	for _, offered := range again {
		if offered.Slug == featured {
			t.Errorf("the featured product %s is offered again: %v", featured, again)
		}
	}
	if _, execErr := pool.Exec(ctx, `UPDATE products SET status = 'archived' WHERE slug = $1`, fresh); execErr != nil {
		t.Fatal(execErr)
	}
	if archived, searchErr := campaigns.NewStore(pool).SearchProducts(ctx, slug, fresh); searchErr != nil || len(archived) != 0 {
		t.Errorf("an archived product is offered: %v %v", archived, searchErr)
	}
	if none, searchErr := campaigns.NewStore(pool).SearchProducts(ctx, slug, "   "); searchErr != nil || len(none) != 0 {
		t.Errorf("a blank search = %v %v, want nothing", none, searchErr)
	}
}

func TestCampaignProductListsUseTheStorefrontVariantAndSellableStock(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	slug := productstest.Draft(t, ctx, pool, products.NewStore(pool))
	for position, variant := range []struct {
		price, original int64
		stock, safety   int
		active          bool
	}{
		{50000, 0, 5, 2, true},
		{99000, 129000, 8, 3, true},
		{19000, 29000, 0, 0, true},
		{1000, 2000, 9, 0, false},
		{109000, 139000, 4, 6, true},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO product_variants
			    (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, is_active, position)
			SELECT id, upper(replace(slug, '-', '')) || '-' || $2::integer::text, $3, nullif($4::bigint, 0), $5, $6, $7, $2
			FROM products WHERE slug = $1`, slug, position, variant.price, variant.original, variant.stock, variant.safety, variant.active); err != nil {
			t.Fatalf("insert variant %d: %v", position, err)
		}
	}
	campaign := admintest.CampaignSlug(t)
	store := campaigns.NewStore(admintest.AdminRolePool(t, pool))
	if _, err := store.Create(ctx, &campaigns.Form{Slug: campaign, Title: "Product offers", Days: 7}); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	matches, err := store.SearchProducts(ctx, campaign, slug)
	if err != nil || len(matches) != 1 {
		t.Fatalf("SearchProducts = %v %v, want one product", matches, err)
	}
	if product := matches[0]; product.PriceCents != 99000 || product.CompareCents != 129000 || product.SellableUnits != 8 || !product.HasVariant || !product.HasDiscountedVariant || !product.Draft {
		t.Errorf("search product = %+v, want discounted buyable variant 99000/129000, 8 sellable units and draft", product)
	}
	if _, updateErr := pool.Exec(ctx, `UPDATE product_variants v SET compare_at_price_cents = NULL FROM products p WHERE p.id = v.product_id AND p.slug = $1 AND v.position = 1`, slug); updateErr != nil {
		t.Fatalf("leave only unavailable discounts: %v", updateErr)
	}
	fallback, err := store.SearchProducts(ctx, campaign, slug)
	if err != nil || len(fallback) != 1 {
		t.Fatalf("fallback search = %v %v, want one product", fallback, err)
	}
	if product := fallback[0]; product.PriceCents != 50000 || product.CompareCents != 0 || !product.HasDiscountedVariant || product.SellableUnits != 8 {
		t.Errorf("fallback search product = %+v, want buyable regular variant 50000, a discounted alternative and 8 sellable units", product)
	}
	if featureErr := store.FeatureProduct(ctx, campaign, slug); featureErr != nil {
		t.Fatalf("feature product: %v", featureErr)
	}
	featured, err := store.Products(ctx, campaign)
	if err != nil || len(featured) != 1 {
		t.Fatalf("Products = %v %v, want one product", featured, err)
	}
	if product := featured[0]; product.PriceCents != 50000 || product.CompareCents != 0 || product.SellableUnits != 8 || !product.HasVariant || !product.HasDiscountedVariant || !product.Draft {
		t.Errorf("featured product = %+v, want the same regular selected variant, discounted alternative, stock and draft state", product)
	}
}
