//go:build integration

package db_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db"
)

// Every query that fills a product card carries the in_campaign predicate as its
// own copy, so each copy is checked on its own: a product in a running campaign
// is in a campaign, one in an ended campaign and one in none is not.
func TestEveryTileQueryCarriesWhetherARunningCampaignFeaturesTheProduct(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	token := "tilecampaign" + strings.ReplaceAll(uuid.NewString(), "-", "")
	var category, user uuid.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO categories(slug,name,position) SELECT $1,'Tile campaign',coalesce(max(position)+1,0) FROM categories WHERE parent_id IS NULL RETURNING id`, "cat-"+token).Scan(&category); err != nil {
		t.Fatal(err)
	}
	product := func(slug string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err = tx.QueryRow(ctx, `INSERT INTO products(category_id,slug,name,status) VALUES($1,$2,$3,'draft') RETURNING id`, category, slug, token+" "+slug).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO product_variants(product_id,sku,price_cents,compare_at_price_cents,stock_quantity,position) VALUES($1,$2,10000,20000,5,0)`, id, "TK-"+strings.ToUpper(uuid.NewString()[:8])); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE products SET status='active',published_at=now() WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	seed := product(token + "-seed")
	running, ended, none := product(token+"-running"), product(token+"-ended"), product(token+"-none")
	want := map[string]bool{token + "-running": true, token + "-ended": false, token + "-none": false}

	campaign := func(slug, window string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err = tx.QueryRow(ctx, `INSERT INTO sale_campaigns(slug,title,starts_at,ends_at) VALUES($1,'Tile campaign',`+window+`) RETURNING id`, slug).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	runningCampaign := campaign("run-"+token, `now() - interval '1 day', now() + interval '1 day'`)
	endedCampaign := campaign("end-"+token, `now() - interval '30 days', now() - interval '1 day'`)
	for c, p := range map[uuid.UUID]uuid.UUID{runningCampaign: running, endedCampaign: ended} {
		if _, err = tx.Exec(ctx, `INSERT INTO sale_campaign_products(campaign_id,product_id,position) VALUES($1,$2,0)`, c, p); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.QueryRow(ctx, `INSERT INTO users(email) VALUES($1) RETURNING id`, token+"@example.com").Scan(&user); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{running, ended, none} {
		if _, err = tx.Exec(ctx, `INSERT INTO product_copurchases(product_id,other_product_id,orders) VALUES($1,$2,1)`, seed, id); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO wishlist_items(user_id,product_id) VALUES($1,$2)`, user, id); err != nil {
			t.Fatal(err)
		}
	}

	// A department's colour story shows one product, so each case has a department
	// and a product of its own; options come before variants.
	storyDepartment := map[string]string{}
	story := func(name string, campaign uuid.UUID) {
		t.Helper()
		var department, id, option uuid.UUID
		slug := "dept-" + name + "-" + token
		if err = tx.QueryRow(ctx, `INSERT INTO categories(slug,name,position) SELECT $1,'Tile campaign story',coalesce(max(position)+1,0) FROM categories WHERE parent_id IS NULL RETURNING id`, slug).Scan(&department); err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(ctx, `INSERT INTO products(category_id,slug,name,status) VALUES($1,$2,$3,'draft') RETURNING id`, department, token+"-story-"+name, token+" story "+name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(ctx, `INSERT INTO product_options(product_id,name,position) VALUES($1,'colour',0) RETURNING id`, id).Scan(&option); err != nil {
			t.Fatal(err)
		}
		for i, hex := range []string{"#111111", "#222222", "#333333"} {
			var value uuid.UUID
			if err = tx.QueryRow(ctx, `INSERT INTO product_option_values(product_id,option_id,value,swatch_hex,position) VALUES($1,$2,$3,$4,$5) RETURNING id`, id, option, "c"+hex, hex, i).Scan(&value); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO product_images(product_id,storage_key,alt_text,option_value_id,position) VALUES($1,$2,'colour',$3,$4)`, id, "story-"+uuid.NewString(), value, i); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO product_variants(product_id,sku,price_cents,compare_at_price_cents,stock_quantity,position) VALUES($1,$2,10000,20000,5,0)`, id, "TS-"+strings.ToUpper(uuid.NewString()[:8])); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE products SET status='active',published_at=now() WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if campaign != uuid.Nil {
			if _, err = tx.Exec(ctx, `INSERT INTO sale_campaign_products(campaign_id,product_id,position) VALUES($1,$2,1)`, campaign, id); err != nil {
				t.Fatal(err)
			}
		}
		storyDepartment[name] = slug
	}
	story("running", runningCampaign)
	story("ended", endedCampaign)
	story("none", uuid.Nil)

	q := db.New(tx)
	terms, exact := catalog.SearchTerms("%" + token + "%")
	const locale = "en"
	type tile struct {
		slug       string
		inCampaign bool
	}
	slugs := []string{token + "-running", token + "-ended", token + "-none"}
	tests := []struct {
		query string
		// listsAll is false where the query itself leaves a product out: the
		// deals and a campaign's shelf list only what a campaign features.
		listsAll bool
		read     func() ([]tile, error)
	}{
		{"CategoryListing", true, func() ([]tile, error) {
			rows, err := q.CategoryListing(ctx, db.CategoryListingParams{Locale: locale, CategoryIds: []uuid.UUID{category}, BrandIds: []uuid.UUID{}, PageSize: 50})
			return mapTiles(rows, func(r db.CategoryListingRow) tile { return tile{r.Slug, r.InCampaign} }), err
		}},
		{"SearchProducts", true, func() ([]tile, error) {
			rows, err := q.SearchProducts(ctx, db.SearchProductsParams{Locale: locale, Patterns: terms, ExactPattern: exact, PageSize: 50})
			return mapTiles(rows, func(r db.SearchProductsRow) tile { return tile{r.Slug, r.InCampaign} }), err
		}},
		{"NewestProducts", true, func() ([]tile, error) {
			rows, err := q.NewestProducts(ctx, db.NewestProductsParams{Locale: locale, PageSize: 500})
			return mapTiles(rows, func(r db.NewestProductsRow) tile { return tile{r.Slug, r.InCampaign} }), err
		}},
		{"DealProducts", false, func() ([]tile, error) {
			rows, err := q.DealProducts(ctx, db.DealProductsParams{Locale: locale, PageSize: 500})
			return mapTiles(rows, func(r db.DealProductsRow) tile { return tile{r.Slug, r.InCampaign} }), err
		}},
		{"CampaignProducts", false, func() ([]tile, error) {
			var out []tile
			for _, c := range []uuid.UUID{runningCampaign, endedCampaign} {
				rows, err := q.CampaignProducts(ctx, db.CampaignProductsParams{CampaignID: c, Locale: locale})
				if err != nil {
					return nil, err
				}
				out = append(out, mapTiles(rows, func(r db.CampaignProductsRow) tile { return tile{r.Slug, r.InCampaign} })...)
			}
			return out, nil
		}},
		{"CompareProducts", true, func() ([]tile, error) {
			rows, err := q.CompareProducts(ctx, db.CompareProductsParams{Locale: locale, Slugs: slugs})
			return mapTiles(rows, func(r db.CompareProductsRow) tile { return tile{r.Slug, r.InCampaign} }), err
		}},
		{"DepartmentColourStory", false, func() ([]tile, error) {
			var out []tile
			for name, slug := range storyDepartment {
				rows, err := q.DepartmentColourStory(ctx, db.DepartmentColourStoryParams{Locale: locale, Slug: slug})
				if err != nil {
					return nil, err
				}
				if len(rows) > 0 {
					out = append(out, tile{token + "-" + name, rows[0].InCampaign})
				}
			}
			return out, nil
		}},
		{"HomeTiles", true, func() ([]tile, error) {
			rows, err := q.HomeTiles(ctx, db.HomeTilesParams{Locale: locale, DepartmentID: uuid.NullUUID{UUID: category, Valid: true}, MaxTiles: 50})
			return mapTiles(rows, func(r db.HomeTilesRow) tile { return tile{r.Slug, r.InCampaign} }), err
		}},
		{"RelatedProducts", true, func() ([]tile, error) {
			rows, err := q.RelatedProducts(ctx, db.RelatedProductsParams{Locale: locale, CategoryID: category, ExcludeID: seed, RowLimit: 50})
			return mapTiles(rows, func(r db.RelatedProductsRow) tile { return tile{r.Slug, r.InCampaign} }), err
		}},
		{"BoughtTogether", true, func() ([]tile, error) {
			rows, err := q.BoughtTogether(ctx, db.BoughtTogetherParams{Locale: locale, ProductID: seed, MinOrders: 1, LimitTo: 50})
			return mapTiles(rows, func(r db.BoughtTogetherRow) tile { return tile{r.Slug, r.InCampaign} }), err
		}},
		{"WishlistItems", true, func() ([]tile, error) {
			rows, err := q.WishlistItems(ctx, db.WishlistItemsParams{UserID: user, Locale: locale})
			return mapTiles(rows, func(r db.WishlistItemsRow) tile { return tile{r.Slug, r.InCampaign} }), err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			tiles, err := tc.read()
			if err != nil {
				t.Fatalf("%s: %v", tc.query, err)
			}
			seen := map[string]bool{}
			for _, got := range tiles {
				wanted, ok := want[got.slug]
				if !ok {
					continue
				}
				seen[got.slug] = true
				if got.inCampaign != wanted {
					t.Errorf("%s: %s InCampaign = %v, want %v", tc.query, got.slug, got.inCampaign, wanted)
				}
			}
			if !seen[token+"-running"] || (tc.listsAll && len(seen) != len(want)) {
				t.Errorf("%s returned %d of the %d fixture products", tc.query, len(seen), len(want))
			}
		})
	}
}
