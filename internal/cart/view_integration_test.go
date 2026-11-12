//go:build integration

package cart_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/order/ordertest"
	"github.com/koopa0/goen/internal/picture"
)

// TestStore_ViewCurrentPrice holds that View shows a line's current price and
// availability.
func TestStore_ViewCurrentPrice(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	vid := ordertest.FreshVariant(t, pool, "current-price")

	if err := s.Add(ctx, id, vid, 2); err != nil {
		t.Fatalf("add: %v", err)
	}
	view, err := s.View(ctx, id)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if len(view.Lines) != 1 {
		t.Fatalf("cart holds %d lines, want 1", len(view.Lines))
	}
	if view.Lines[0].Unavailable || view.Lines[0].Short {
		t.Fatal("a freshly added, in-stock line reads as unavailable")
	}
	if view.ItemCount != 2 {
		t.Errorf("item count = %d, want 2 — the badge counts units, not lines", view.ItemCount)
	}
	want := view.Lines[0].UnitCents * 2
	if view.SubtotalCents != want {
		t.Errorf("subtotal = %d, want %d", view.SubtotalCents, want)
	}
}

// TestStore_ViewColourPhoto holds the cart thumbnail to the line's variant: its
// value's photograph when the shop tagged one, else the product's first, which
// is the only picture an untagged product has.
func TestStore_ViewColourPhoto(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	for _, sku := range []string{"PXL-9P-2-1", "PXL-9P-1-1"} {
		var variant uuid.UUID
		if err := pool.QueryRow(ctx,
			`SELECT id FROM product_variants WHERE sku = $1`, sku).Scan(&variant); err != nil {
			t.Fatalf("read %s: %v", sku, err)
		}
		if err := s.Add(ctx, id, variant, 1); err != nil {
			t.Fatalf("add %s: %v", sku, err)
		}
	}
	view, err := s.View(ctx, id)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if len(view.Lines) != 2 {
		t.Fatalf("cart holds %d lines, want 2", len(view.Lines))
	}
	want := map[string]string{
		"PXL-9P-2-1": picture.Image{BundledKey: "pixelight-9-pro-02.webp"}.URL(), // 曜石黑, tagged by the seed
		"PXL-9P-1-1": picture.Image{BundledKey: "pixelight-9-pro-01.webp"}.URL(), // 星霧藍, the product's first
	}
	for _, line := range view.Lines {
		if line.ImageURL != want[line.SKU] {
			t.Errorf("line %s shows %q, want %q", line.SKU, line.ImageURL, want[line.SKU])
		}
	}
}

// TestStore_ItemCountBadge holds that ItemCount counts what the cart holds for
// the header badge.
func TestStore_ItemCountBadge(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	a, b, _ := ordertest.ThreeVariants(t, pool, "badgefix")

	count := func() int {
		t.Helper()
		n, err := s.ItemCount(ctx, id)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	if got := count(); got != 0 {
		t.Errorf("an empty cart counts %d, want 0", got)
	}
	if err := s.Add(ctx, id, a, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := count(); got != 1 {
		t.Errorf("one unit counts %d, want 1", got)
	}
	if err := s.Add(ctx, id, a, 2); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := count(); got != 3 {
		t.Errorf("three units of one variant count %d, want 3 — the badge is "+
			"counting lines, not items", got)
	}
	if err := s.Add(ctx, id, b, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := count(); got != 4 {
		t.Errorf("four units across two variants count %d, want 4", got)
	}

	other := newCart(t, s)
	if err := s.Add(ctx, other, a, 5); err != nil {
		t.Fatalf("add to other: %v", err)
	}
	if got := count(); got != 4 {
		t.Errorf("this cart counts %d after another cart was filled, want 4", got)
	}
}

// TestStore_ViewCampaignLine holds that View strikes a line's price only while
// a listed campaign features its product, and carries when the first such
// campaign ends: a compare-at price alone, or one under a campaign that has
// ended, leaves the line at its plain price.
func TestStore_ViewCampaignLine(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	sale := ordertest.FreshVariant(t, pool, "campaign-sale")
	ended := ordertest.FreshVariant(t, pool, "campaign-ended")
	plain := ordertest.FreshVariant(t, pool, "campaign-plain")
	var endsAt time.Time
	for _, v := range []struct {
		variant uuid.UUID
		ends    string
	}{{sale, "5 days"}, {sale, "9 days"}, {ended, "-1 hour"}, {plain, ""}} {
		if _, err := pool.Exec(ctx, `UPDATE product_variants SET compare_at_price_cents = price_cents + 10000 WHERE id = $1`, v.variant); err != nil {
			t.Fatalf("set compare-at price: %v", err)
		}
		if v.ends == "" {
			continue
		}
		var campaignEnds time.Time
		if err := pool.QueryRow(ctx, `
			WITH c AS (
			    INSERT INTO sale_campaigns (slug, title, starts_at, ends_at)
			    VALUES ($1, '測試活動', now() - interval '1 day', now() + $2::interval) RETURNING id, ends_at
			)
			INSERT INTO sale_campaign_products (campaign_id, product_id)
			SELECT c.id, pv.product_id FROM c, product_variants pv WHERE pv.id = $3
			RETURNING (SELECT ends_at FROM c)`,
			"cart-"+uuid.NewString()[:8], v.ends, v.variant).Scan(&campaignEnds); err != nil {
			t.Fatalf("feature the product in a campaign ending in %s: %v", v.ends, err)
		}
		if v.variant == sale && v.ends == "5 days" {
			endsAt = campaignEnds
		}
	}
	for _, v := range []uuid.UUID{sale, ended, plain} {
		if err := s.Add(ctx, id, v, 1); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	view, err := s.View(ctx, id)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	want := map[string]bool{sale.String(): true, ended.String(): false, plain.String(): false}
	for _, line := range view.Lines {
		if line.OnSale() != want[line.VariantID] {
			t.Errorf("line %s on sale = %t, want %t", line.SKU, line.OnSale(), want[line.VariantID])
		}
		if line.VariantID != sale.String() {
			continue
		}
		if got, want := line.Sale.Datetime(), campaign.NewEnd(endsAt, time.Now()).Datetime(); got != want {
			t.Errorf("the sale line ends %s, want %s, the first of its two campaigns to end", got, want)
		}
		if line.CompareCents != line.UnitCents+10000 {
			t.Errorf("the sale line strikes %d, want %d", line.CompareCents, line.UnitCents+10000)
		}
	}
}
