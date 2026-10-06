//go:build integration

package db_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db"
)

// The nine queries that fill a product card carry the colours the product page's
// picker offers: the first option all of whose offered values are colours, and a
// value counts only while an active variant uses it. Each copy of the subquery is
// checked on its own, so none can drift.
func TestEveryTileQueryCarriesTheColoursThePickerOffers(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	token := "tilecolours" + strings.ReplaceAll(uuid.NewString(), "-", "")
	var category, campaign, user uuid.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO categories(slug,name,position) SELECT $1,'Tile colours',coalesce(max(position)+1,0) FROM categories WHERE parent_id IS NULL RETURNING id`, "cat-"+token).Scan(&category); err != nil {
		t.Fatal(err)
	}

	type colour struct {
		name, swatch string
		retired      bool // used only by a variant that is no longer sold
	}
	product := func(slug string, options ...[]colour) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err = tx.QueryRow(ctx, `INSERT INTO products(category_id,slug,name,status) VALUES($1,$2,$3,'draft') RETURNING id`, category, slug, token+" "+slug).Scan(&id); err != nil {
			t.Fatal(err)
		}
		type pick struct{ option, value uuid.UUID }
		var variants [][]pick
		var retired []bool
		for o, values := range options {
			var optionID uuid.UUID
			if err = tx.QueryRow(ctx, `INSERT INTO product_options(product_id,name,position) VALUES($1,$2,$3) RETURNING id`, id, "option"+strings.Repeat("x", o+1), o).Scan(&optionID); err != nil {
				t.Fatal(err)
			}
			for i, v := range values {
				var valueID uuid.UUID
				if err = tx.QueryRow(ctx, `INSERT INTO product_option_values(product_id,option_id,value,swatch_hex,position) VALUES($1,$2,$3,nullif($4,''),$5) RETURNING id`, id, optionID, v.name, v.swatch, i).Scan(&valueID); err != nil {
					t.Fatal(err)
				}
				for len(variants) <= i {
					variants = append(variants, nil)
					retired = append(retired, false)
				}
				variants[i] = append(variants[i], pick{optionID, valueID})
				retired[i] = retired[i] || v.retired
			}
		}
		if len(variants) == 0 {
			variants, retired = [][]pick{nil}, []bool{false}
		}
		for i, picks := range variants {
			var variantID uuid.UUID
			if err = tx.QueryRow(ctx, `INSERT INTO product_variants(product_id,sku,price_cents,compare_at_price_cents,stock_quantity,is_active,position) VALUES($1,$2,10000,20000,5,$3,$4) RETURNING id`, id, "TC-"+strings.ToUpper(uuid.NewString()[:8]), !retired[i], i).Scan(&variantID); err != nil {
				t.Fatal(err)
			}
			for _, p := range picks {
				if _, err = tx.Exec(ctx, `INSERT INTO variant_option_values(product_id,variant_id,option_id,option_value_id) VALUES($1,$2,$3,$4)`, id, variantID, p.option, p.value); err != nil {
					t.Fatal(err)
				}
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE products SET status='active',published_at=now() WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	seed := product(token + "-seed")
	choice := product(token+"-choice",
		[]colour{{name: "128"}, {name: "256"}, {name: "512"}},
		[]colour{{name: "black", swatch: "#111111"}, {name: "white", swatch: "#222222"}, {name: "red", swatch: "#333333", retired: true}},
	)
	single := product(token+"-single", []colour{{name: "black", swatch: "#111111"}})
	unlabelled := product(token+"-unlabelled", []colour{{name: "black", swatch: "#111111"}, {name: "plain"}})
	want := map[string][]string{
		token + "-choice":     {"#111111", "#222222"},
		token + "-single":     {"#111111"},
		token + "-unlabelled": nil,
	}

	if err = tx.QueryRow(ctx, `INSERT INTO sale_campaigns(slug,title,ends_at) VALUES($1,'Tile colours',now()+interval '1 day') RETURNING id`, "camp-"+token).Scan(&campaign); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO users(email) VALUES($1) RETURNING id`, token+"@example.com").Scan(&user); err != nil {
		t.Fatal(err)
	}
	for position, id := range []uuid.UUID{choice, single, unlabelled} {
		if _, err = tx.Exec(ctx, `INSERT INTO sale_campaign_products(campaign_id,product_id,position) VALUES($1,$2,$3)`, campaign, id, position); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO product_copurchases(product_id,other_product_id,orders) VALUES($1,$2,1)`, seed, id); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO wishlist_items(user_id,product_id) VALUES($1,$2)`, user, id); err != nil {
			t.Fatal(err)
		}
	}

	q := db.New(tx)
	terms, exact := catalog.SearchTerms(token)
	const locale = "en"
	type tile struct {
		slug    string
		colours []string
	}
	tests := []struct {
		query string
		read  func() ([]tile, error)
	}{
		{"CategoryListing", func() ([]tile, error) {
			rows, err := q.CategoryListing(ctx, db.CategoryListingParams{Locale: locale, CategoryIds: []uuid.UUID{category}, PageSize: 50})
			return mapTiles(rows, func(r db.CategoryListingRow) tile { return tile{r.Slug, r.Colours} }), err
		}},
		{"SearchProducts", func() ([]tile, error) {
			rows, err := q.SearchProducts(ctx, db.SearchProductsParams{Locale: locale, Patterns: terms, ExactPattern: exact, PageSize: 50})
			return mapTiles(rows, func(r db.SearchProductsRow) tile { return tile{r.Slug, r.Colours} }), err
		}},
		{"NewestProducts", func() ([]tile, error) {
			rows, err := q.NewestProducts(ctx, db.NewestProductsParams{Locale: locale, PageSize: 500})
			return mapTiles(rows, func(r db.NewestProductsRow) tile { return tile{r.Slug, r.Colours} }), err
		}},
		{"DealProducts", func() ([]tile, error) {
			rows, err := q.DealProducts(ctx, db.DealProductsParams{Locale: locale, PageSize: 500})
			return mapTiles(rows, func(r db.DealProductsRow) tile { return tile{r.Slug, r.Colours} }), err
		}},
		{"CampaignProducts", func() ([]tile, error) {
			rows, err := q.CampaignProducts(ctx, db.CampaignProductsParams{CampaignID: campaign, Locale: locale})
			return mapTiles(rows, func(r db.CampaignProductsRow) tile { return tile{r.Slug, r.Colours} }), err
		}},
		{"HomeTiles", func() ([]tile, error) {
			rows, err := q.HomeTiles(ctx, db.HomeTilesParams{Locale: locale, CampaignID: uuid.NullUUID{UUID: campaign, Valid: true}, MaxTiles: 50})
			return mapTiles(rows, func(r db.HomeTilesRow) tile { return tile{r.Slug, r.Colours} }), err
		}},
		{"RelatedProducts", func() ([]tile, error) {
			rows, err := q.RelatedProducts(ctx, db.RelatedProductsParams{Locale: locale, CategoryID: category, ExcludeID: seed, RowLimit: 50})
			return mapTiles(rows, func(r db.RelatedProductsRow) tile { return tile{r.Slug, r.Colours} }), err
		}},
		{"BoughtTogether", func() ([]tile, error) {
			rows, err := q.BoughtTogether(ctx, db.BoughtTogetherParams{Locale: locale, ProductID: seed, MinOrders: 1, LimitTo: 50})
			return mapTiles(rows, func(r db.BoughtTogetherRow) tile { return tile{r.Slug, r.Colours} }), err
		}},
		{"WishlistItems", func() ([]tile, error) {
			rows, err := q.WishlistItems(ctx, db.WishlistItemsParams{UserID: user, Locale: locale})
			return mapTiles(rows, func(r db.WishlistItemsRow) tile { return tile{r.Slug, r.Colours} }), err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			tiles, err := tc.read()
			if err != nil {
				t.Fatalf("%s: %v", tc.query, err)
			}
			seen := 0
			for _, got := range tiles {
				colours, ok := want[got.slug]
				if !ok {
					continue
				}
				seen++
				if !slices.Equal(got.colours, colours) {
					t.Errorf("%s: %s colours = %v, want %v", tc.query, got.slug, got.colours, colours)
				}
			}
			if seen != len(want) {
				t.Errorf("%s returned %d of the %d fixture products", tc.query, seen, len(want))
			}
		})
	}
}

func mapTiles[R any, T any](rows []R, f func(R) T) []T {
	out := make([]T, len(rows))
	for i := range rows {
		out[i] = f(rows[i])
	}
	return out
}
