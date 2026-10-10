//go:build integration

package product_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/shoptime"
)

type relatedRankingVariant struct {
	price       int64
	optionValue string
	active      bool
	inStock     bool
}

type relatedRankingProduct struct {
	specLabel  string
	specValue  string
	optionName string
	sameBrand  bool
	variants   []relatedRankingVariant
}

func TestRelatedProductsRankContentBeforeRecency(t *testing.T) {
	tests := []struct {
		name      string
		configure func(reference, older, newer *relatedRankingProduct)
		wantOlder bool
		tieDate   bool
	}{
		{"shared spec and price", func(_, older, newer *relatedRankingProduct) {
			older.specValue = "64"
			newer.variants[0].price = 50000
		}, true, false},
		{"spec value", func(_, older, _ *relatedRankingProduct) {
			older.specValue = "64"
		}, true, false},
		{"spec label", func(_, older, newer *relatedRankingProduct) {
			older.specValue = "64"
			newer.specLabel, newer.specValue = "Memory", "64"
		}, true, false},
		{"shared attribute outweighs price", func(_, older, _ *relatedRankingProduct) {
			older.specValue = "64"
			older.variants[0].price = 50000
		}, true, false},
		{"option value", func(_, older, newer *relatedRankingProduct) {
			older.optionName, newer.optionName = "Finish", "Finish"
			older.variants[0].optionValue, newer.variants[0].optionValue = "Black", "White"
		}, true, false},
		{"option name", func(_, older, newer *relatedRankingProduct) {
			older.optionName, newer.optionName = "Finish", "Shade"
			older.variants[0].optionValue, newer.variants[0].optionValue = "Black", "Black"
		}, true, false},
		{"brand", func(_, older, _ *relatedRankingProduct) {
			older.sameBrand = true
		}, true, false},
		{"missing brands are not a match", func(reference, older, _ *relatedRankingProduct) {
			reference.sameBrand = false
			older.sameBrand = true
		}, false, false},
		{"price proximity", func(_, older, newer *relatedRankingProduct) {
			older.variants[0].price, newer.variants[0].price = 9000, 50000
		}, true, false},
		{"reference minimum active price", func(reference, _, newer *relatedRankingProduct) {
			reference.variants[0].inStock = false
			reference.variants = append(reference.variants, relatedRankingVariant{40000, "White", true, true})
			newer.variants[0].price = 40000
		}, true, false},
		{"candidate minimum active price", func(_, older, newer *relatedRankingProduct) {
			older.variants[0].inStock = false
			older.variants = append(older.variants, relatedRankingVariant{40000, "", true, true})
			newer.variants[0].price = 20000
		}, true, false},
		{"inactive reference price", func(reference, _, newer *relatedRankingProduct) {
			reference.variants = append(reference.variants, relatedRankingVariant{0, "White", false, false})
			newer.variants[0].price = 0
		}, true, false},
		{"inactive candidate price", func(_, older, newer *relatedRankingProduct) {
			older.variants[0].price, newer.variants[0].price = 9000, 50000
			newer.variants = append(newer.variants, relatedRankingVariant{10000, "", false, false})
		}, true, false},
		{"inactive reference option", func(reference, older, newer *relatedRankingProduct) {
			reference.variants = append(reference.variants, relatedRankingVariant{10000, "White", false, false})
			older.optionName, newer.optionName = "Finish", "Finish"
			older.variants[0].optionValue, newer.variants[0].optionValue = "Black", "White"
		}, true, false},
		{"inactive candidate option", func(_, older, newer *relatedRankingProduct) {
			older.optionName, newer.optionName = "Finish", "Finish"
			older.variants[0].optionValue, newer.variants[0].optionValue = "Black", "White"
			newer.variants = append(newer.variants, relatedRankingVariant{10000, "Black", false, false})
		}, true, false},
		{"options count once across variants", func(_, older, newer *relatedRankingProduct) {
			older.specValue = "64"
			older.optionName, newer.optionName = "Finish", "Finish"
			older.variants[0].optionValue, newer.variants[0].optionValue = "Black", "Black"
			newer.variants = append(newer.variants,
				relatedRankingVariant{10000, "Black", true, true},
				relatedRankingVariant{10000, "Black", true, true})
		}, true, false},
		{"zero prices", func(reference, older, _ *relatedRankingProduct) {
			reference.variants[0].price, older.variants[0].price = 0, 0
		}, true, false},
		{"maximum price", func(reference, older, newer *relatedRankingProduct) {
			reference.variants[0].price, older.variants[0].price = money.MaxCents, money.MaxCents
			newer.variants[0].price = 0
		}, true, false},
		{"stock still leads", func(_, older, _ *relatedRankingProduct) {
			older.specValue = "64"
			older.variants[0].inStock = false
		}, false, false},
		{"no specs keeps recency", func(reference, older, newer *relatedRankingProduct) {
			reference.specLabel, reference.specValue = "", ""
			older.sameBrand, older.optionName = true, "Finish"
			older.variants[0].optionValue = "Black"
			newer.variants[0].price = 50000
		}, false, false},
		{"equal scores keep recency", func(_, _, _ *relatedRankingProduct) {}, false, false},
		{"equal scores and dates use ID", func(_, _, _ *relatedRankingProduct) {}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pgtx.Rollback(ctx, tx)

			var categoryID, brandID uuid.UUID
			if err := tx.QueryRow(ctx, `INSERT INTO categories (slug, name)
				VALUES ($1, 'Ranking') RETURNING id`, "ranking-"+uuid.NewString()).Scan(&categoryID); err != nil {
				t.Fatal(err)
			}
			if err := tx.QueryRow(ctx, `INSERT INTO brands (slug, name)
				VALUES ($1, 'Ranking') RETURNING id`, "ranking-"+uuid.NewString()).Scan(&brandID); err != nil {
				t.Fatal(err)
			}
			candidate := func() relatedRankingProduct {
				return relatedRankingProduct{
					specLabel: "Capacity", specValue: "128",
					variants: []relatedRankingVariant{{10000, "", true, true}},
				}
			}
			reference, older, newer := candidate(), candidate(), candidate()
			reference.specValue, reference.optionName, reference.sameBrand = "64", "Finish", true
			reference.variants[0].optionValue = "Black"
			tt.configure(&reference, &older, &newer)

			published, ok := shoptime.ParseInputDay("2026-01-01")
			if !ok {
				t.Fatal("invalid fixture date")
			}
			newerDate := published.AddDate(0, 0, 1)
			if tt.tieDate {
				newerDate = published
			}
			referenceID := insertRelatedRankingProduct(t, tx, categoryID, brandID, "reference", published, reference)
			olderID := insertRelatedRankingProduct(t, tx, categoryID, brandID, "older", published, older)
			newerID := insertRelatedRankingProduct(t, tx, categoryID, brandID, "newer", newerDate, newer)
			if _, err := tx.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE"); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, "SET LOCAL ROLE store"); err != nil {
				t.Fatal(err)
			}

			want := []string{"newer", "older"}
			if tt.wantOlder || (tt.tieDate && olderID.String() > newerID.String()) {
				slices.Reverse(want)
			}
			q := db.New(tx)
			for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
				for _, limit := range []int32{1, product.RelatedCount} {
					rows, err := q.RelatedProducts(ctx, db.RelatedProductsParams{
						Locale: string(locale), CategoryID: categoryID, ExcludeID: referenceID, RowLimit: limit,
					})
					if err != nil {
						t.Fatal(err)
					}
					var got []string
					for _, row := range rows {
						got = append(got, row.Slug)
					}
					if diff := cmp.Diff(want[:min(int(limit), len(want))], got); diff != "" {
						t.Errorf("%s limit %d related ranking (-want +got):\n%s", locale, limit, diff)
					}
				}
			}
		})
	}
}

func insertRelatedRankingProduct(t *testing.T, tx pgx.Tx, categoryID, brandID uuid.UUID, slug string, published time.Time, p relatedRankingProduct) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	brand := uuid.NullUUID{UUID: brandID, Valid: p.sameBrand}
	var productID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO products (category_id, brand_id, slug, name, status, published_at)
		VALUES ($1, $2, $3, $3, 'active', $4) RETURNING id`, categoryID, brand, slug, published).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	if p.specLabel != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO product_specs (product_id, label, value)
			VALUES ($1, $2, $3)`, productID, p.specLabel, p.specValue); err != nil {
			t.Fatal(err)
		}
	}
	var optionID uuid.UUID
	values := map[string]uuid.UUID{}
	if p.optionName != "" {
		if err := tx.QueryRow(ctx, `INSERT INTO product_options (product_id, name)
			VALUES ($1, $2) RETURNING id`, productID, p.optionName).Scan(&optionID); err != nil {
			t.Fatal(err)
		}
		for _, v := range p.variants {
			if _, exists := values[v.optionValue]; exists {
				continue
			}
			var valueID uuid.UUID
			if err := tx.QueryRow(ctx, `INSERT INTO product_option_values (product_id, option_id, value)
				VALUES ($1, $2, $3) RETURNING id`, productID, optionID, v.optionValue).Scan(&valueID); err != nil {
				t.Fatal(err)
			}
			values[v.optionValue] = valueID
		}
	}
	for position, v := range p.variants {
		var variantID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO product_variants (product_id, sku, price_cents, is_active, position)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, productID,
			fmt.Sprintf("RANKING-%s-%d", strings.ToUpper(slug), position), v.price, v.active, position).Scan(&variantID); err != nil {
			t.Fatal(err)
		}
		if p.optionName != "" {
			if _, err := tx.Exec(ctx, `INSERT INTO variant_option_values (product_id, variant_id, option_id, option_value_id)
				VALUES ($1, $2, $3, $4)`, productID, variantID, optionID, values[v.optionValue]); err != nil {
				t.Fatal(err)
			}
		}
		if v.inStock {
			if _, err := tx.Exec(ctx, `SELECT record_inventory_movement($1, 1, 'receipt', $2, 'admin', NULL, NULL)`,
				variantID, "ranking-"+variantID.String()); err != nil {
				t.Fatal(err)
			}
		}
	}
	return productID
}
