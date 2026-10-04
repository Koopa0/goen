//go:build integration

package campaigns_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/campaigns"
)

func TestFeatureProductUnknownProductReturnsNotFound(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := campaigns.NewStore(pool)
	slug := admintest.CampaignSlug(t)
	if _, err := s.Create(ctx, &campaigns.Form{
		Slug: slug, Title: "測試活動", Days: 7,
	}); err != nil {
		t.Fatalf("create campaign: %v", err)
	}

	missing := "no-such-product-" + uuid.NewString()
	before := admintest.AuditRows(t, pool, audit.ActionFeatureProduct)
	if err := s.FeatureProduct(ctx, slug, missing); !errors.Is(err, campaigns.ErrNotFound) {
		t.Fatalf("unknown product = %v, want ErrNotFound", err)
	}
	if after := admintest.AuditRows(t, pool, audit.ActionFeatureProduct); after != before {
		t.Errorf("%d audit rows after a zero-row feature, want %d", after, before)
	}
}

func TestFeatureProductAlreadyFeaturedReturnsNotFound(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := campaigns.NewStore(pool)
	slug := admintest.CampaignSlug(t)
	productSlug := admintest.DiscountedProductSlug(t, pool)
	if _, err := s.Create(ctx, &campaigns.Form{
		Slug: slug, Title: "測試活動", Days: 7,
	}); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if err := s.FeatureProduct(ctx, slug, productSlug); err != nil {
		t.Fatalf("first feature: %v", err)
	}
	if err := s.FeatureProduct(ctx, slug, productSlug); !errors.Is(err, campaigns.ErrNotFound) {
		t.Fatalf("already featured = %v, want ErrNotFound", err)
	}
}

func TestFeatureProductHandlerNeverOKOnMiss(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := campaigns.NewStore(pool)
	h := handlerOver(s)
	slug := admintest.CampaignSlug(t)
	if _, err := s.Create(ctx, &campaigns.Form{
		Slug: slug, Title: "測試活動", Days: 7,
	}); err != nil {
		t.Fatalf("create campaign: %v", err)
	}

	post := func(product string) *httptest.ResponseRecorder {
		body := url.Values{"product": {product}}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/admin/campaigns/"+slug+"/products", strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("slug", slug)
		res := httptest.NewRecorder()
		h.FeatureProduct(res, req)
		return res
	}

	missing := "no-such-product-" + uuid.NewString()
	if res := post(missing); res.Code != http.StatusSeeOther ||
		!strings.Contains(res.Header().Get("Location"), "?refused=1") ||
		strings.Contains(res.Header().Get("Location"), "?ok=1") {
		t.Fatalf("unknown product redirect = %d %q, want refused without ok",
			res.Code, res.Header().Get("Location"))
	}

	productSlug := admintest.DiscountedProductSlug(t, pool)
	if ok := post(productSlug); ok.Code != http.StatusSeeOther ||
		!strings.Contains(ok.Header().Get("Location"), "?ok=1") {
		t.Fatalf("first feature redirect = %d %q, want ok=1", ok.Code, ok.Header().Get("Location"))
	}
	if again := post(productSlug); again.Code != http.StatusSeeOther ||
		!strings.Contains(again.Header().Get("Location"), "?refused=1") ||
		strings.Contains(again.Header().Get("Location"), "?ok=1") {
		t.Fatalf("already featured redirect = %d %q, want refused without ok",
			again.Code, again.Header().Get("Location"))
	}
}

func discountedProductSlugs(t *testing.T, n int) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT DISTINCT p.slug FROM products p JOIN product_variants pv ON pv.product_id = p.id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.compare_at_price_cents > pv.price_cents
		ORDER BY p.slug
		LIMIT $1`, n)
	if err != nil {
		t.Fatalf("find discounted products: %v", err)
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var slug string
		if scanErr := rows.Scan(&slug); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		slugs = append(slugs, slug)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate discounted products: %v", err)
	}
	if len(slugs) < n {
		t.Fatalf("found %d discounted products, need %d", len(slugs), n)
	}
	return slugs
}

// TestTwoConcurrentCampaignFeaturesTakeDistinctPositions proves the advisory lock
// is in the statement: without it two staff members featuring at once both read
// the same max(position) and sale_campaign_products_position_key refuses one.
func TestTwoConcurrentCampaignFeaturesTakeDistinctPositions(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	slug := admintest.CampaignSlug(t)
	if _, err := campaigns.NewStore(pool).Create(ctx, &campaigns.Form{
		Slug: slug, Title: "併發活動", Days: 7,
	}); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM sale_campaigns WHERE slug = $1`, slug)
	})
	slugs := discountedProductSlugs(t, 2)

	poolA := admintest.NamedPool(t, pool, "campaign-append-a-"+uuid.NewString()[:8])
	poolB := admintest.NamedPool(t, pool, "campaign-append-b-"+uuid.NewString()[:8])
	storeA := campaigns.NewStore(poolA)
	storeB := campaigns.NewStore(poolB)

	start := make(chan struct{})
	type result struct {
		product string
		err     error
	}
	done := make(chan result, 2)
	go func() {
		<-start
		done <- result{product: slugs[0], err: storeA.FeatureProduct(ctx, slug, slugs[0])}
	}()
	go func() {
		<-start
		done <- result{product: slugs[1], err: storeB.FeatureProduct(ctx, slug, slugs[1])}
	}()
	close(start)

	featured := make([]string, 0, 2)
	for range 2 {
		got := <-done
		if got.err != nil {
			t.Fatalf("FeatureProduct(%s): %v", got.product, got.err)
		}
		featured = append(featured, got.product)
	}

	var positions []int32
	rows, err := pool.Query(ctx, `
		SELECT cp.position
		FROM sale_campaign_products cp
		JOIN sale_campaigns c ON c.id = cp.campaign_id
		JOIN products p ON p.id = cp.product_id
		WHERE c.slug = $1 AND p.slug = ANY($2)
		ORDER BY cp.position`, slug, featured)
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p int32
		if scanErr := rows.Scan(&p); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		positions = append(positions, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate campaign positions: %v", err)
	}
	if len(positions) != 2 || positions[0] == positions[1] {
		t.Errorf("positions = %v, want two distinct values — the lock is not in "+
			"the statement, so two concurrent features collided on max(position)+1",
			positions)
	}
}
