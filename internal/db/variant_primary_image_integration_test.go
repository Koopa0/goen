//go:build integration

package db_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
)

// A product photographed in silver (first), untagged (second) and black
// (third), sold in silver, black and gold. Gold has no photograph of its own.
const linePhotoScenario = `
INSERT INTO product_options (id, product_id, name) VALUES
    ('aaaa0091-0000-4000-8000-000000000000', '3333aaaa-3333-4333-8333-333333333333', '顏色');
INSERT INTO product_option_values (id, product_id, option_id, value) VALUES
    ('bbbb0091-0000-4000-8000-000000000000', '3333aaaa-3333-4333-8333-333333333333', 'aaaa0091-0000-4000-8000-000000000000', '銀'),
    ('bbbb0092-0000-4000-8000-000000000000', '3333aaaa-3333-4333-8333-333333333333', 'aaaa0091-0000-4000-8000-000000000000', '石墨黑'),
    ('bbbb0093-0000-4000-8000-000000000000', '3333aaaa-3333-4333-8333-333333333333', 'aaaa0091-0000-4000-8000-000000000000', '金');
INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES
    ('44440091-0000-4000-8000-000000000000', '3333aaaa-3333-4333-8333-333333333333', 'LINE-PHOTO-SILVER', 100, 0),
    ('44440092-0000-4000-8000-000000000000', '3333aaaa-3333-4333-8333-333333333333', 'LINE-PHOTO-BLACK', 100, 1),
    ('44440093-0000-4000-8000-000000000000', '3333aaaa-3333-4333-8333-333333333333', 'LINE-PHOTO-GOLD', 100, 2);
INSERT INTO variant_option_values (product_id, variant_id, option_id, option_value_id) VALUES
    ('3333aaaa-3333-4333-8333-333333333333', '44440091-0000-4000-8000-000000000000', 'aaaa0091-0000-4000-8000-000000000000', 'bbbb0091-0000-4000-8000-000000000000'),
    ('3333aaaa-3333-4333-8333-333333333333', '44440092-0000-4000-8000-000000000000', 'aaaa0091-0000-4000-8000-000000000000', 'bbbb0092-0000-4000-8000-000000000000'),
    ('3333aaaa-3333-4333-8333-333333333333', '44440093-0000-4000-8000-000000000000', 'aaaa0091-0000-4000-8000-000000000000', 'bbbb0093-0000-4000-8000-000000000000');
INSERT INTO product_images (product_id, bundled_key, alt_text, position, option_value_id) VALUES
    ('3333aaaa-3333-4333-8333-333333333333', 'line-silver.webp', '銀', 0, 'bbbb0091-0000-4000-8000-000000000000'),
    ('3333aaaa-3333-4333-8333-333333333333', 'line-product.webp', '商品照', 1, NULL),
    ('3333aaaa-3333-4333-8333-333333333333', 'line-black.webp', '石墨黑', 2, 'bbbb0092-0000-4000-8000-000000000000');
`

// TestVariantPrimaryImageFirst: the photograph variant_primary_image gives a
// cart or order line is the first the product page's gallery shows for that
// choice, including none.
//
// The line readers once ordered by "shows this variant" and then position, so a
// variant with no photograph of its own showed another colour's (#2141): here,
// gold would have shown the silver one.
func TestVariantPrimaryImageFirst(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, execErr := tx.Exec(ctx, fixtures+linePhotoScenario); execErr != nil {
		t.Fatalf("load fixtures: %v", execErr)
	}

	want := map[string]string{
		"44440091-0000-4000-8000-000000000000": "line-silver.webp",
		"44440092-0000-4000-8000-000000000000": "line-black.webp",
		"44440093-0000-4000-8000-000000000000": "line-product.webp",
		"":                                     "line-product.webp",
	}

	rows, err := tx.Query(ctx, `
		SELECT pv.product_id, pv.id FROM product_variants pv
		UNION ALL SELECT p.id, NULL FROM products p`)
	if err != nil {
		t.Fatalf("list choices: %v", err)
	}
	type choice struct {
		Product uuid.UUID
		Variant uuid.NullUUID
	}
	choices, err := pgx.CollectRows(rows, pgx.RowToStructByPos[choice])
	if err != nil {
		t.Fatalf("read choices: %v", err)
	}

	q := db.New(tx)
	compared := 0
	for _, c := range choices {
		gallery, galleryErr := q.ProductImages(ctx, db.ProductImagesParams{
			Locale: "zh-Hant", ProductID: c.Product, VariantID: c.Variant,
		})
		if galleryErr != nil {
			t.Fatalf("gallery of %s/%v: %v", c.Product, c.Variant, galleryErr)
		}
		var line string
		lineErr := tx.QueryRow(ctx, `
			SELECT coalesce(media_digest, bundled_key) FROM variant_primary_image
			WHERE product_id = $1 AND variant_id IS NOT DISTINCT FROM $2`,
			c.Product, c.Variant).Scan(&line)
		switch {
		case len(gallery) == 0 && errors.Is(lineErr, pgx.ErrNoRows):
			continue
		case lineErr != nil:
			t.Fatalf("line photograph of %s/%v: %v", c.Product, c.Variant, lineErr)
		case len(gallery) == 0:
			t.Errorf("%s/%v: the line shows %s, the gallery nothing", c.Product, c.Variant, line)
			continue
		}
		first := gallery[0].ImageBundledKey
		if gallery[0].ImageDigest != "" {
			first = gallery[0].ImageDigest
		}
		if line != first {
			t.Errorf("%s/%v: a line shows %s, the product page opens on %s", c.Product, c.Variant, line, first)
		}
		compared++
		key := ""
		if c.Variant.Valid {
			key = c.Variant.UUID.String()
		}
		if w, ok := want[key]; ok && c.Product.String() == "3333aaaa-3333-4333-8333-333333333333" && line != w {
			t.Errorf("variant %q shows %s, want %s", key, line, w)
		}
	}
	if compared != 7 {
		t.Errorf("compared %d choices, want 7: the scenario's three variants and none chosen, "+
			"the fixture product's two and none chosen", compared)
	}
}

// TestVariantPrimaryImageWidensNoRolesRead: variant_primary_image is
// security_invoker, so its grant widens no role's read: store, admin and
// reporting read it, and a role that cannot read a table beneath it, such as
// maintenance, cannot read it.
//
// The view is security_invoker, so
// its grant lets a role read nothing it could not already read from
// product_images, product_variants and variant_option_values. The readers'
// roles read it; a role that cannot read a table beneath it cannot read it.
func TestVariantPrimaryImageWidensNoRolesRead(t *testing.T) {
	const read = `SELECT bundled_key FROM variant_primary_image WHERE product_id = ` + fixtureProduct
	for _, role := range []string{"store", "admin", "reporting"} {
		expectAccepted(t, role+" reads variant_primary_image", `SET LOCAL ROLE `+role+`; `+read)
	}
	expectRefusal(t, "maintenance, which reads no catalogue", `SET LOCAL ROLE maintenance; `+read, "42501", "")
	expectRefusal(t, "store without product_images", `
		REVOKE SELECT ON product_images FROM store;
		SET LOCAL ROLE store; `+read, "42501", "")
	expectRefusal(t, "admin without variant_option_values", `
		REVOKE SELECT ON variant_option_values FROM admin;
		SET LOCAL ROLE admin; `+read, "42501", "")
}
