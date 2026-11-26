//go:build integration

package catalog_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
)

// TestStore_ListingOnSale holds that the sale switch keeps what a card would
// strike, campaign.OnSale's question asked of the variant the card prices: a
// product a listed campaign features, priced by a variant in stock below its
// compare-at price. A product at full price, a featured one whose discounted
// variant is sold out and a discounted one in no campaign are not on sale; a
// product whose cheapest variant is at full price is on sale through a dearer
// one, and its card then prices that one, struck.
func TestStore_ListingOnSale(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	token := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	category := "sale-" + token
	if _, err = tx.Exec(ctx, `INSERT INTO categories (slug, name, position)
		SELECT $1, '特價測試', coalesce(max(position) + 1, 0) FROM categories WHERE parent_id IS NULL`, category); err != nil {
		t.Fatalf("category: %v", err)
	}
	// Each variant is price, compare-at (0 for none) and stock.
	type variant struct{ price, compare, stock int }
	product := func(name string, variants ...variant) {
		t.Helper()
		var id uuid.UUID
		if err = tx.QueryRow(ctx, `INSERT INTO products (category_id, slug, name, status)
			SELECT id, $2, $3, 'draft' FROM categories WHERE slug = $1 RETURNING id`, category, name+"-"+token, name).Scan(&id); err != nil {
			t.Fatalf("product %s: %v", name, err)
		}
		for i, v := range variants {
			if _, err = tx.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position)
				VALUES ($1, $2, $3, nullif($4, 0), $5, 0, $6)`, id, strings.ToUpper(name+token)+string(rune('A'+i)), v.price, v.compare, v.stock, i); err != nil {
				t.Fatalf("variant of %s: %v", name, err)
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE products SET status = 'active', published_at = now() WHERE id = $1`, id); err != nil {
			t.Fatalf("publish %s: %v", name, err)
		}
	}
	product("deal", variant{100000, 200000, 5})
	product("full", variant{100000, 0, 5})
	product("soldout", variant{100000, 200000, 0}, variant{150000, 0, 5})
	product("nocampaign", variant{100000, 200000, 5})
	product("dearer", variant{50000, 0, 5}, variant{80000, 160000, 5})
	if _, err = tx.Exec(ctx, `
		WITH c AS (
		    INSERT INTO sale_campaigns (slug, title, starts_at, ends_at)
		    VALUES ($1, '特價測試', now() - interval '1 day', now() + interval '7 days') RETURNING id
		)
		INSERT INTO sale_campaign_products (campaign_id, product_id, position)
		SELECT c.id, p.id, row_number() OVER (ORDER BY p.slug) FROM c, products p WHERE p.slug = ANY($2)`,
		"sale-"+token, []string{"deal-" + token, "soldout-" + token, "dearer-" + token}); err != nil {
		t.Fatalf("campaign: %v", err)
	}

	store := catalog.NewStore(tx, slog.New(slog.DiscardHandler))
	view, err := store.ListingPage(ctx, category, catalog.Filters{OnSaleOnly: true, Page: 1}, catalog.ViewGrid)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	got := make([]string, 0, len(view.Products))
	for _, p := range view.Products {
		got = append(got, strings.TrimSuffix(p.Slug, "-"+token))
		if !p.OnSale || p.CompareCents <= p.PriceCents {
			t.Errorf("%s is on the sale shelf without a struck price: %+v", p.Slug, p)
		}
		if p.Slug == "dearer-"+token && p.PriceCents != 80000 {
			t.Errorf("the dearer deal's card prices %d, not the discounted variant's 80000", p.PriceCents)
		}
	}
	slices.Sort(got)
	if want := []string{"deal", "dearer"}; !slices.Equal(got, want) || view.Total != 2 {
		t.Errorf("on sale = %v (total %d), want %v", got, view.Total, want)
	}
	if view.Prices[0].Count != view.Total {
		t.Errorf("the band for any price counts %d under the sale switch, the shelf %d", view.Prices[0].Count, view.Total)
	}

	// Without the switch the cheaper variant at full price is the card's.
	all, err := store.ListingPage(ctx, category, catalog.Filters{Page: 1}, catalog.ViewGrid)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	for _, p := range all.Products {
		if p.Slug == "dearer-"+token && (p.PriceCents != 50000 || p.OnSale) {
			t.Errorf("unfiltered, the dearer deal's card prices %d on sale %t, want 50000 at full price", p.PriceCents, p.OnSale)
		}
	}
	if all.Total != 5 {
		t.Errorf("unfiltered total %d, want 5", all.Total)
	}
}

// TestStore_ListingPriceBands holds that a price band's count is what choosing
// it lists, under every other filter, and that the band for any price counts
// the shelf as the other filters leave it.
func TestStore_ListingPriceBands(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	store := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	for _, base := range []catalog.Filters{{Page: 1}, {InStockOnly: true, Page: 1}} {
		view, err := store.ListingPage(ctx, "tech", base, catalog.ViewGrid)
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		if len(view.Prices) != 4 || view.Prices[0].Count != view.Total {
			t.Fatalf("bands %+v: want four, the first counting the shelf's %d", view.Prices, view.Total)
		}
		bounded := 0
		for _, band := range view.Prices[1:] {
			f := base
			f.Price = band.Band
			chosen, err := store.ListingPage(ctx, "tech", f, catalog.ViewGrid)
			if err != nil {
				t.Fatalf("listing in %q: %v", band.Band.ID, err)
			}
			if chosen.Total != band.Count {
				t.Errorf("in stock %t: band %q counts %d, choosing it lists %d", base.InStockOnly, band.Band.ID, band.Count, chosen.Total)
			}
			if band.Count > 0 {
				bounded++
			}
		}
		if bounded < 2 {
			t.Errorf("in stock %t: %d bands hold products; the seed's tech spans at least two", base.InStockOnly, bounded)
		}
	}
}

// TestStore_SearchPageOrder holds that the search page lists a search's
// matches in the order Search does, chosen sort or none: SearchMatches copies
// SearchProducts' predicate and relevance tiers, and this is what notices the
// day one changes without the other.
func TestStore_SearchPageOrder(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	store := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	for _, query := range []string{"pixelight", "aurora 65w", "耳機", "pro", "茶"} {
		for _, sort := range []catalog.Sort{catalog.SortRelevance, catalog.SortPriceAsc, catalog.SortRating} {
			want, err := store.Search(ctx, catalog.SearchPattern(query), sort, 1)
			if err != nil {
				t.Fatalf("search %q: %v", query, err)
			}
			got, err := store.SearchPage(ctx, query, catalog.Filters{Sort: sort, Page: 1}, catalog.ViewGrid)
			if err != nil {
				t.Fatalf("search page %q: %v", query, err)
			}
			slugs := func(v catalog.SearchView) []string {
				out := make([]string, len(v.Products))
				for i := range v.Products {
					out[i] = v.Products[i].Slug
				}
				return out
			}
			if !slices.Equal(slugs(got), slugs(want)) || got.Total != want.Total || int64(got.Matches) != want.Total {
				t.Errorf("%q sorted %q: the page lists %v (%d, %d matches), Search %v (%d)", query, sort, slugs(got), got.Total, got.Matches, slugs(want), want.Total)
			}
			if len(want.Products) == 0 {
				t.Errorf("%q matches nothing in the seed; the order is not compared", query)
			}
		}
	}
}

// TestStore_SearchPageDepartment holds that a search's departments count its
// matches, and that choosing one keeps the matches filed under it.
func TestStore_SearchPageDepartment(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	store := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	all, err := store.SearchPage(ctx, "aurora", catalog.Filters{Page: 1}, catalog.ViewGrid)
	if err != nil {
		t.Fatal(err)
	}
	if all.DepartmentsTotal() != int64(all.Matches) || len(all.Departments) == 0 {
		t.Fatalf("departments %+v hold %d of %d matches", all.Departments, all.DepartmentsTotal(), all.Matches)
	}
	for _, d := range all.Departments {
		narrowed, err := store.SearchPage(ctx, "aurora", catalog.Filters{Department: d.Slug, Page: 1}, catalog.ViewGrid)
		if err != nil {
			t.Fatal(err)
		}
		if narrowed.Total != d.Count {
			t.Errorf("department %s counts %d matches, choosing it lists %d", d.Slug, d.Count, narrowed.Total)
		}
	}
}

// TestHandler_Shop holds that /shop is every active product on one shelf, its
// tree's first line the page itself.
func TestHandler_Shop(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	var active int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM products WHERE status = 'active'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	view, err := catalog.NewStore(pool, slog.New(slog.DiscardHandler)).ShopPage(ctx, catalog.Filters{Page: 1}, catalog.ViewGrid)
	if err != nil {
		t.Fatal(err)
	}
	if view.Total != active || view.Departments[0].Count != active || !view.Departments[0].Current {
		t.Errorf("/shop lists %d and its first line counts %d (current %t), want every active product, %d",
			view.Total, view.Departments[0].Count, view.Departments[0].Current, active)
	}

	h := catalog.NewHandler(catalog.NewStore(pool, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), "https://goen.example")
	res := httptest.NewRecorder()
	h.Shop(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/shop?view=list", http.NoBody))
	body := res.Body.String()
	if res.Code != http.StatusOK || !strings.Contains(body, `<h1 class="goen-pagehead__title">全館選物</h1>`) || !strings.Contains(body, `class="goen-speclist"`) {
		t.Errorf("GET /shop?view=list = %d, want the shelf of every product as a list", res.Code)
	}
}
