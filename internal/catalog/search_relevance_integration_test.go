//go:build integration

package catalog_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/catalog"
)

func TestSearchOrdersExplicitFieldRelevanceBeforeRecency(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
	token := "ranking" + uuid.NewString()[:8]
	slugs := make([]string, 5)
	for i := range slugs {
		slugs[i] = "relevance-" + uuid.NewString()
		name, brand, summary := "Fixture", "Fixture brand", "Fixture summary"
		switch i {
		case 0:
			name = token
		case 1:
			name = token + " edition"
		case 2:
			brand = token
		case 3:
			summary = token
		}
		var brandID, productID uuid.UUID
		if fixtureErr := tx.QueryRow(ctx, `INSERT INTO brands (slug, name) VALUES ($1, $2) RETURNING id`, "brand-"+uuid.NewString(), brand).Scan(&brandID); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
		if fixtureErr := tx.QueryRow(ctx, `INSERT INTO products (brand_id, category_id, slug, name, name_en, summary, status, published_at)
   SELECT $1, category_id, $2, 'Original fixture', $3, $4, 'draft', now() + ($5 * interval '1 second') FROM products LIMIT 1 RETURNING id`, brandID, slugs[i], name, summary, i).Scan(&productID); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
		if _, fixtureErr := tx.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents) VALUES ($1, $2, 10000)`, productID, "RANK-"+strings.ToUpper(uuid.NewString())); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
		if i == 4 {
			if _, fixtureErr := tx.Exec(ctx, `INSERT INTO product_specs (product_id, label, value, position) VALUES ($1, 'Lookup', $2, 0)`, productID, token); fixtureErr != nil {
				t.Fatal(fixtureErr)
			}
		}
		if _, fixtureErr := tx.Exec(ctx, `UPDATE products SET status = 'active' WHERE id = $1`, productID); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
	}
	view, err := catalog.NewStore(tx).Search(ctx, catalog.SearchPattern(strings.ToUpper(token)), 1)
	if err != nil {
		t.Fatal(err)
	}
	if view.Total != 5 || len(view.Products) != 5 {
		t.Fatalf("ranked matches total=%d rows=%d", view.Total, len(view.Products))
	}
	for i, slug := range slugs {
		if view.Products[i].Slug != slug {
			t.Errorf("rank %d = %s, want %s", i, view.Products[i].Slug, slug)
		}
	}
}

func TestSearchRelevanceTiesUsePublicationThenIdentity(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
	token := "tie" + uuid.NewString()
	slugs := make([]string, 3)
	ids := make([]uuid.UUID, 3)
	for i := range slugs {
		slugs[i] = "relevance-tie-" + uuid.NewString()
		age := 0
		if i == 0 {
			age = 1
		}
		if fixtureErr := tx.QueryRow(ctx, `INSERT INTO products (brand_id, category_id, slug, name, summary, status, published_at)
 SELECT brand_id, category_id, $1, 'Tie fixture', $2, 'draft', now() - ($3 * interval '1 day') FROM products LIMIT 1 RETURNING id`, slugs[i], token, age).Scan(&ids[i]); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
		if _, fixtureErr := tx.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents) VALUES ($1, $2, 10000)`, ids[i], "TIE-"+strings.ToUpper(uuid.NewString())); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
		if _, fixtureErr := tx.Exec(ctx, `UPDATE products SET status = 'active' WHERE id = $1`, ids[i]); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
	}
	want := []string{slugs[1], slugs[2], slugs[0]}
	if ids[1].String() < ids[2].String() {
		want[0], want[1] = want[1], want[0]
	}
	view, err := catalog.NewStore(tx).Search(ctx, catalog.SearchPattern(token), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Products) != 3 {
		t.Fatalf("tie fixture returned %d products", len(view.Products))
	}
	for i, slug := range want {
		if view.Products[i].Slug != slug {
			t.Errorf("tie order %d = %s, want %s", i, view.Products[i].Slug, slug)
		}
	}
}

// The Chinese name column and the English summary column each carry a ranking
// arm of their own. Every product here leaves the other language empty, so
// only the arm under test can place it, and each one is older than the
// products below it: recency alone would order them the other way round.
func TestSearchRanksTheChineseNameAndEnglishSummaryArms(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
	token := "zhrank" + uuid.NewString()[:8]
	fixtures := []struct {
		name, summaryEN string
		spec            bool
	}{
		{name: token},                        // exact name, in the name column only
		{name: token + "版"},                  // name substring, in the name column only
		{name: "原型", summaryEN: token},       // summary, in summary_en only
		{name: "原型", summaryEN: "unrelated"}, // spec only
	}
	slugs := make([]string, len(fixtures))
	for i, f := range fixtures {
		slugs[i] = "relevance-zh-" + uuid.NewString()
		var brandID, productID uuid.UUID
		if fixtureErr := tx.QueryRow(ctx, `INSERT INTO brands (slug, name) VALUES ($1, 'Fixture brand') RETURNING id`, "brand-"+uuid.NewString()).Scan(&brandID); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
		if fixtureErr := tx.QueryRow(ctx, `INSERT INTO products (brand_id, category_id, slug, name, summary, summary_en, status, published_at)
   SELECT $1, category_id, $2, $3, 'Fixture summary', NULLIF($4, ''), 'draft', now() + ($5 * interval '1 second') FROM products LIMIT 1 RETURNING id`, brandID, slugs[i], f.name, f.summaryEN, i).Scan(&productID); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
		if _, fixtureErr := tx.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents) VALUES ($1, $2, 10000)`, productID, "RANKZH-"+strings.ToUpper(uuid.NewString())); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
		if i == len(fixtures)-1 {
			if _, fixtureErr := tx.Exec(ctx, `INSERT INTO product_specs (product_id, label, value, position) VALUES ($1, 'Lookup', $2, 0)`, productID, token); fixtureErr != nil {
				t.Fatal(fixtureErr)
			}
		}
		if _, fixtureErr := tx.Exec(ctx, `UPDATE products SET status = 'active' WHERE id = $1`, productID); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
	}
	view, err := catalog.NewStore(tx).Search(ctx, catalog.SearchPattern(strings.ToUpper(token)), 1)
	if err != nil {
		t.Fatal(err)
	}
	if view.Total != int64(len(slugs)) || len(view.Products) != len(slugs) {
		t.Fatalf("ranked matches total=%d rows=%d", view.Total, len(view.Products))
	}
	for i, slug := range slugs {
		if view.Products[i].Slug != slug {
			t.Errorf("rank %d = %s, want %s", i, view.Products[i].Slug, slug)
		}
	}
}

func TestSearchFindsAProductByItsCategoryNameAndRanksItAboveASummaryMention(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
	token := "kind" + uuid.NewString()[:8]
	var categoryID uuid.UUID
	if fixtureErr := tx.QueryRow(ctx, `INSERT INTO categories (slug, name, name_en) VALUES ($1, $2, $3) RETURNING id`,
		"cat-"+uuid.NewString(), "類別"+token, "Category "+token).Scan(&categoryID); fixtureErr != nil {
		t.Fatal(fixtureErr)
	}
	// The mention is newer, so recency alone would list it first.
	fixtures := []struct {
		inCategory bool
		summary    string
	}{
		{inCategory: true, summary: "Fixture summary"},
		{summary: "Fits every " + token},
	}
	slugs := make([]string, len(fixtures))
	for i, f := range fixtures {
		slugs[i] = "relevance-cat-" + uuid.NewString()
		var category *uuid.UUID
		if f.inCategory {
			category = &categoryID
		}
		var brandID, productID uuid.UUID
		if fixtureErr := tx.QueryRow(ctx, `INSERT INTO brands (slug, name) VALUES ($1, 'Fixture brand') RETURNING id`, "brand-"+uuid.NewString()).Scan(&brandID); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
		if fixtureErr := tx.QueryRow(ctx, `INSERT INTO products (brand_id, category_id, slug, name, summary, status, published_at)
   SELECT $1, coalesce($5::uuid, category_id), $2, 'Original fixture', $3, 'draft', now() + ($4 * interval '1 second')
   FROM products LIMIT 1 RETURNING id`, brandID, slugs[i], f.summary, i, category).Scan(&productID); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
		if _, fixtureErr := tx.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents) VALUES ($1, $2, 10000)`, productID, "RANKCAT-"+strings.ToUpper(uuid.NewString())); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
		if _, fixtureErr := tx.Exec(ctx, `UPDATE products SET status = 'active' WHERE id = $1`, productID); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
	}
	for _, q := range []string{"類別" + token, strings.ToUpper("category " + token)} {
		view, searchErr := catalog.NewStore(tx).Search(ctx, catalog.SearchPattern(q), 1)
		if searchErr != nil {
			t.Fatal(searchErr)
		}
		if view.Total != 1 || len(view.Products) != 1 || view.Products[0].Slug != slugs[0] {
			t.Errorf("q=%q total=%d products=%v, want only the category's product", q, view.Total, view.Products)
		}
	}
	view, err := catalog.NewStore(tx).Search(ctx, catalog.SearchPattern(token), 1)
	if err != nil {
		t.Fatal(err)
	}
	if view.Total != 2 || len(view.Products) != 2 || view.Products[0].Slug != slugs[0] || view.Products[1].Slug != slugs[1] {
		t.Errorf("q=%q total=%d, want the category's product above the summary mention", token, view.Total)
	}
}
