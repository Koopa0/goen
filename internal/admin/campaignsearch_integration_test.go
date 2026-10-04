//go:build integration

package admin_test

import (
	"testing"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/admin/products"
)

func TestTheCampaignProductSearchOffersWhatIsNotFeaturedYet(t *testing.T) {
	ctx, _ := staffContext(t)
	s := products.NewStore(pool)
	slug := admintest.CampaignSlug(t)
	if _, err := campaigns.NewStore(pool).Create(ctx, &campaigns.Form{Slug: slug, Title: "測試活動", Days: 7}); err != nil {
		t.Fatal(err)
	}
	featured := admintest.DiscountedProductSlug(t, pool)
	if err := campaigns.NewStore(pool).FeatureProduct(ctx, slug, featured); err != nil {
		t.Fatal(err)
	}
	fresh := admintest.DraftProduct(t, ctx, pool, s)

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
