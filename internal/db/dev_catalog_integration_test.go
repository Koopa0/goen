//go:build integration

package db_test

import (
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db/dbtest"
)

// TestDemoCatalogueRequiresNoProductsOrOrders: seed/dev_catalog.sql refuses a
// database that already holds products or orders.
func TestDemoCatalogueRequiresNoProductsOrOrders(t *testing.T) {
	shop := dbtest.Pool(t)
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatalf("read demo catalogue: %v", err)
	}
	for _, fixture := range []struct {
		name    string
		prepare string
		clean   string
	}{
		{
			name: "product without orders",
			prepare: `WITH category AS (
				INSERT INTO categories (slug, name) VALUES ('kept', 'Kept') RETURNING id
			) INSERT INTO products (category_id, slug, name)
				SELECT id, 'kept', 'Kept' FROM category`,
			clean: `DELETE FROM products WHERE slug = 'kept'`,
		},
		{
			name: "order without products",
			prepare: `INSERT INTO shipping_methods (code) VALUES ('kept');
				INSERT INTO shipping_method_versions (method_id, name, fee_cents)
				SELECT id, 'Kept', 0 FROM shipping_methods WHERE code = 'kept';
				WITH kept_order AS (
					INSERT INTO orders (shipping_version_id, shipping_method_code, shipping_method_name)
					SELECT v.id, sm.code, v.name FROM shipping_method_versions v
					JOIN shipping_methods sm ON sm.id = v.method_id WHERE sm.code = 'kept'
					RETURNING id
				), kept_line AS (
					INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
					SELECT id, 'KEPT-1', 'Historical purchase', 0, 1 FROM kept_order
				)
				INSERT INTO order_private_data (order_id, email, recipient_name, phone,
				                                postal_code, city, district, street)
				SELECT id, 'kept@example.com', 'Kept recipient', '0912345678',
				       '110', 'Taipei', 'Xinyi', '1 Kept Road' FROM kept_order`,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if _, err := shop.Exec(t.Context(), fixture.prepare); err != nil {
				t.Fatalf("prepare existing shop: %v", err)
			}
			before := tableDigests(t, shop)
			conn, err := shop.Acquire(t.Context())
			if err != nil {
				t.Fatalf("acquire seed connection: %v", err)
			}
			defer conn.Release()
			_, seedErr := conn.Exec(t.Context(), string(seed))
			if _, err := conn.Exec(t.Context(), "ROLLBACK"); err != nil {
				t.Fatalf("rollback refused seed: %v", err)
			}
			pgErr, ok := errors.AsType[*pgconn.PgError](seedErr)
			if !ok || pgErr.Code != "23514" || pgErr.ConstraintName != "dev_catalog_requires_empty_shop" {
				t.Fatalf("seed error = %v, want empty-shop constraint refusal", seedErr)
			}
			if changed := changedTables(before, tableDigests(t, shop)); len(changed) != 0 {
				t.Fatalf("refused seed changed tables: %v", changed)
			}
			if fixture.clean != "" {
				if _, err := shop.Exec(t.Context(), fixture.clean); err != nil {
					t.Fatalf("remove product fixture: %v", err)
				}
			}
		})
	}
}

// TestDemoCatalogueLoadsIntoSchemaOnlyShop: seed/dev_catalog.sql loads into a
// database holding only the schema.
func TestDemoCatalogueLoadsIntoSchemaOnlyShop(t *testing.T) {
	shop := dbtest.Pool(t)
	seedCatalogue(t, shop)
	var products int
	if err := shop.QueryRow(t.Context(), `SELECT count(*) FROM products`).Scan(&products); err != nil {
		t.Fatalf("count demo products: %v", err)
	}
	if products == 0 {
		t.Fatal("demo catalogue left the shop without products")
	}
}
