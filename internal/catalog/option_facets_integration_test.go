//go:build integration

package catalog_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestOptionFacetsRequireOneVariantForEverySelectedValueStockAndPrice(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	category, slug := "option-facet-"+uuid.NewString(), "option-product-"+uuid.NewString()
	var productID uuid.UUID
	if err = tx.QueryRow(ctx, `WITH category AS (
  INSERT INTO categories(slug,name,position) SELECT $1,'Facet category',coalesce(max(position)+1,0) FROM categories WHERE parent_id IS NULL RETURNING id
 ), brand AS (
  INSERT INTO brands(slug,name) VALUES($2,'Facet brand') RETURNING id
 ) INSERT INTO products(category_id,brand_id,slug,name,status) SELECT category.id,brand.id,$3,'Facet product','draft' FROM category,brand RETURNING id`, category, "facet-brand-"+uuid.NewString(), slug).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	type axisChoice struct{ axis, value uuid.UUID }
	choices := map[string]axisChoice{}
	for position, axis := range []struct {
		name, label    string
		values, labels []string
	}{
		{"容量", "Capacity", []string{"128GB", "256GB"}, []string{"128 GB", "256 GB"}},
		{"顏色", "Colour", []string{"曜石黑", "白"}, []string{"Obsidian", "White"}},
	} {
		var axisID uuid.UUID
		if err = tx.QueryRow(ctx, `INSERT INTO product_options(product_id,name,name_en,position) VALUES($1,$2,$3,$4) RETURNING id`, productID, axis.name, axis.label, position).Scan(&axisID); err != nil {
			t.Fatal(err)
		}
		for i, value := range axis.values {
			var valueID uuid.UUID
			if err = tx.QueryRow(ctx, `INSERT INTO product_option_values(product_id,option_id,value,value_en,position) VALUES($1,$2,$3,$4,$5) RETURNING id`, productID, axisID, value, axis.labels[i], i).Scan(&valueID); err != nil {
				t.Fatal(err)
			}
			choices[axis.name+":"+value] = axisChoice{axis: axisID, value: valueID}
		}
	}
	var matching uuid.UUID
	for i, v := range []struct {
		price  int64
		stock  int
		values []string
	}{
		{90000, 0, []string{"容量:256GB", "顏色:曜石黑"}},
		{10000, 5, []string{"容量:128GB", "顏色:曜石黑"}},
		{10000, 5, []string{"容量:256GB", "顏色:白"}},
	} {
		var id uuid.UUID
		if err = tx.QueryRow(ctx, `INSERT INTO product_variants(product_id,sku,price_cents,stock_quantity,position) VALUES($1,$2,$3,$4,$5) RETURNING id`, productID, "FACET-"+strings.ToUpper(uuid.NewString()[:8])+"-"+strconv.Itoa(i), v.price, v.stock, i).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			matching = id
		}
		for _, value := range v.values {
			choice := choices[value]
			if _, err = tx.Exec(ctx, `INSERT INTO variant_option_values(product_id,variant_id,option_id,option_value_id) VALUES($1,$2,$3,$4)`, productID, id, choice.axis, choice.value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE products SET status='active',published_at=now() WHERE id=$1`, productID); err != nil {
		t.Fatal(err)
	}
	store := catalog.NewStore(tx)
	f := catalog.Filters{Page: 1, InStockOnly: true, MaxPrice: 50000, OptionValues: []catalog.OptionFilter{{Name: "容量", Value: "256GB"}, {Name: "顏色", Value: "曜石黑"}}}
	local := i18n.WithLocale(ctx, i18n.En)
	view, err := store.Listing(local, category, f)
	if err != nil {
		t.Fatal(err)
	}
	if view.Total != 0 || len(view.Products) != 0 {
		t.Fatal("different variants satisfied option, stock or price filters")
	}
	facetCounts := map[string]int64{}
	selected := 0
	for _, group := range view.Facets {
		if group.Kind != pages.FacetVariantOption {
			continue
		}
		if group.Label != "Capacity" && group.Label != "Colour" {
			t.Fatalf("untranslated axis %q", group.Label)
		}
		for _, option := range group.Options {
			facetCounts[option.Value] = option.Count
			if option.Selected {
				selected++
			}
			if option.Value == "顏色:曜石黑" && option.Label != "Obsidian" {
				t.Fatal("URL identity leaked into the displayed label")
			}
		}
	}
	if selected != 2 || facetCounts["容量:256GB"] != 0 || facetCounts["容量:128GB"] != 1 || facetCounts["顏色:白"] != 1 {
		t.Fatalf("facet counts/selection = %v/%d", facetCounts, selected)
	}
	if _, err = tx.Exec(ctx, `UPDATE product_variants SET price_cents=10000 WHERE id=$1`, matching); err != nil {
		t.Fatal(err)
	}
	view, err = store.Listing(local, category, f)
	if err != nil {
		t.Fatal(err)
	}
	if view.Total != 0 {
		t.Fatal("price and options borrowed stock from another SKU")
	}
	if _, err = tx.Exec(ctx, `SELECT record_inventory_movement($1,5,'adjustment',$2,'admin',NULL,NULL)`, matching, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	view, err = store.Listing(local, category, f)
	if err != nil {
		t.Fatal(err)
	}
	if view.Total != 1 || len(view.Products) != 1 || view.Products[0].Slug != slug || view.Products[0].PriceCents != 10000 || !view.Products[0].InStock || view.Products[0].PriceVaries {
		t.Fatalf("one matching SKU did not supply the listing offer: %#v", view.Products)
	}
	impossible := f
	impossible.OptionValues = []catalog.OptionFilter{{Name: "容量", Value: "256GB"}, {Name: "容量", Value: "128GB"}}
	view, err = store.Listing(local, category, impossible)
	if err != nil {
		t.Fatal(err)
	}
	if view.Total != 0 {
		t.Fatal("two values of one axis were satisfied by different variants")
	}
	unknown := f
	unknown.OptionValues = []catalog.OptionFilter{{Name: "容量", Value: "unknown"}}
	view, err = store.Listing(local, category, unknown)
	if err != nil {
		t.Fatal(err)
	}
	if view.Total != 0 {
		t.Fatal("unknown option silently produced an unfiltered listing")
	}
}
