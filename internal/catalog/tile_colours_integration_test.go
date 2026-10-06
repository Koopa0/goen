//go:build integration

package catalog_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

type colourValue struct {
	name, swatch string
	inactive     bool // used only by a variant that is not for sale
}

type colourFixture struct {
	options [][]colourValue // one slice of values per option, in position order; swatch "" is none
	want    []string
}

func insertColourProduct(t *testing.T, tx pgx.Tx, category uuid.UUID, slug, name string, f colourFixture) {
	t.Helper()
	ctx := t.Context()
	var productID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO products(category_id,slug,name,status) VALUES($1,$2,$3,'draft') RETURNING id`, category, slug, name).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	type chosen struct{ option, value uuid.UUID }
	var variants [][]chosen
	for o, values := range f.options {
		var optionID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO product_options(product_id,name,position) VALUES($1,$2,$3) RETURNING id`, productID, "option"+string(rune('a'+o)), o).Scan(&optionID); err != nil {
			t.Fatal(err)
		}
		for i, v := range values {
			var valueID uuid.UUID
			if err := tx.QueryRow(ctx, `INSERT INTO product_option_values(product_id,option_id,value,swatch_hex,position) VALUES($1,$2,$3,nullif($4,''),$5) RETURNING id`, productID, optionID, v.name, v.swatch, i).Scan(&valueID); err != nil {
				t.Fatal(err)
			}
			for len(variants) <= i {
				variants = append(variants, nil)
			}
			variants[i] = append(variants[i], chosen{optionID, valueID})
		}
	}
	for i, picks := range variants {
		inactive := false
		for _, values := range f.options {
			if i < len(values) && values[i].inactive {
				inactive = true
			}
		}
		var variantID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO product_variants(product_id,sku,price_cents,stock_quantity,is_active,position) VALUES($1,$2,10000,5,$3,$4) RETURNING id`, productID, "TC-"+strings.ToUpper(uuid.NewString()[:8]), !inactive, i).Scan(&variantID); err != nil {
			t.Fatal(err)
		}
		for _, p := range picks {
			if _, err := tx.Exec(ctx, `INSERT INTO variant_option_values(product_id,variant_id,option_id,option_value_id) VALUES($1,$2,$3,$4)`, productID, variantID, p.option, p.value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE products SET status='active',published_at=now() WHERE id=$1`, productID); err != nil {
		t.Fatal(err)
	}
}

// Every tile query carries the same colours: those of the first option all of
// whose values are colours and are offered by an active variant, and nothing when
// the option has a value without one.
func TestTilesCarryTheColoursTheProductPageOffers(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	token := "tilecolours" + strings.ReplaceAll(uuid.NewString(), "-", "")
	categorySlug := "cat-" + token
	var category uuid.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO categories(slug,name,position) SELECT $1,'Tile colours',coalesce(max(position)+1,0) FROM categories WHERE parent_id IS NULL RETURNING id`, categorySlug).Scan(&category); err != nil {
		t.Fatal(err)
	}
	products := map[string]colourFixture{
		token + "-choice": {
			options: [][]colourValue{
				{{name: "128", swatch: ""}, {name: "256", swatch: ""}, {name: "512", swatch: ""}},
				{{name: "black", swatch: "#111111"}, {name: "white", swatch: "#222222"}, {name: "red", swatch: "#333333", inactive: true}},
			},
			want: []string{"#111111", "#222222"},
		},
		token + "-one": {
			options: [][]colourValue{{{name: "black", swatch: "#111111"}}},
			want:    []string{"#111111"}, // the card draws dots only from two
		},
		token + "-unnamed": {
			options: [][]colourValue{{{name: "black", swatch: "#111111"}, {name: "plain", swatch: ""}}},
		},
	}
	for slug, f := range products {
		insertColourProduct(t, tx, category, slug, token+" "+slug, f)
	}

	store := catalog.NewStore(tx)
	local := i18n.WithLocale(ctx, i18n.En)
	check := func(surface string, tiles []pages.ProductTile) {
		t.Helper()
		seen := 0
		for i := range tiles {
			f, ok := products[tiles[i].Slug]
			if !ok {
				continue
			}
			seen++
			if !slices.Equal(tiles[i].Colours, f.want) {
				t.Errorf("%s: %s colours = %v, want %v", surface, tiles[i].Slug, tiles[i].Colours, f.want)
			}
		}
		if seen != len(products) {
			t.Errorf("%s returned %d of the %d fixture products", surface, seen, len(products))
		}
	}

	listing, err := store.Listing(local, categorySlug, catalog.Filters{Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	check("Listing", listing.Products)

	search, err := store.Search(local, token, catalog.SortRelevance, 1)
	if err != nil {
		t.Fatal(err)
	}
	check("Search", search.Products)

	newest, err := store.NewestProducts(local, 500)
	if err != nil {
		t.Fatal(err)
	}
	check("NewestProducts", newest)
}
