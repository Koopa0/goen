//go:build integration

package products_test

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/admin/products/productstest"
	"github.com/koopa0/goen/internal/admin/shipping"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/ui/uitest"
)

// TestHandler_AddVariantMistypedParcel: a mistyped parcel dimension does not become unmeasured.
func TestHandler_AddVariantMistypedParcel(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	h := productstest.NewHandler(pool, s)
	slug := productstest.Draft(t, ctx, pool, s)
	defer func() {
		if err := s.SetStatus(context.WithoutCancel(ctx), slug, "archived"); err != nil {
			t.Errorf("archive parcel fixture: %v", err)
		}
	}()

	tests := []struct {
		name  string
		field string
		id    string
		raw   string
	}{
		{name: "safety stock", field: "safety", id: "v-safety", raw: "1000001"},
		{name: "longest side", field: "parcel_longest", id: "v-longest", raw: "5001"},
		{name: "three-side sum", field: "parcel_sum", id: "v-sum", raw: "15001"},
		{name: "weight typo", field: "parcel_weight", id: "v-weight", raw: "10,000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sku := "PARSE-" + strings.ToUpper(uuid.NewString()[:8])
			form := url.Values{
				"sku":            {sku},
				"price":          {"1000"},
				"compare":        {""},
				"safety":         {"0"},
				"parcel_longest": {"450"},
				"parcel_sum":     {"1050"},
				"parcel_weight":  {"500"},
			}
			form.Set(tt.field, tt.raw)
			res := productstest.PostVariantForm(t, h, ctx, slug, form)
			if res.Code != http.StatusUnprocessableEntity {
				t.Errorf("AddVariant(%s=%q) status = %d, want 422", tt.field, tt.raw, res.Code)
			} else {
				body := res.Body.String()
				uitest.AssertRefusedInput(t, body, tt.id, tt.raw)
				for id, field := range map[string]string{
					"v-sku": "sku", "v-price": "price", "v-compare": "compare",
					"v-safety": "safety", "v-longest": "parcel_longest",
					"v-sum": "parcel_sum", "v-weight": "parcel_weight",
				} {
					if got := uitest.InputAttribute(t, uitest.InputElementByID(t, body, id), "value"); got != form.Get(field) {
						t.Errorf("input %q value = %q, want submitted %q", id, got, form.Get(field))
					}
				}
				for _, id := range []string{"v-safety", "v-longest", "v-sum", "v-weight"} {
					uitest.AssertTextNumberControl(t, body, id)
				}
			}
			var count int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM product_variants WHERE sku = $1`, sku).Scan(&count); err != nil {
				t.Fatalf("count refused variant: %v", err)
			}
			if count != 0 {
				t.Errorf("AddVariant(%s=%q) inserted %d rows", tt.field, tt.raw, count)
			}
		})
	}

	measuredSKU := "MEASURED-" + strings.ToUpper(uuid.NewString()[:8])
	measured := url.Values{
		"sku": {measuredSKU}, "price": {"1000"}, "safety": {"0"},
		"parcel_longest": {"450"}, "parcel_sum": {"1050"}, "parcel_weight": {"15000"},
	}
	if res := productstest.PostVariantForm(t, h, ctx, slug, measured); res.Code != http.StatusSeeOther {
		t.Fatalf("add measured variant status = %d, want 303", res.Code)
	}
	unmeasuredSKU := "UNMEASURED-" + strings.ToUpper(uuid.NewString()[:8])
	unmeasured := url.Values{
		"sku": {unmeasuredSKU}, "price": {"1000"}, "safety": {"0"},
		"parcel_longest": {""}, "parcel_sum": {""}, "parcel_weight": {""},
	}
	if res := productstest.PostVariantForm(t, h, ctx, slug, unmeasured); res.Code != http.StatusSeeOther {
		t.Fatalf("add unmeasured variant status = %d, want 303", res.Code)
	}

	variantIDs := make(map[string]uuid.UUID, 2)
	rows, err := pool.Query(ctx, `
		SELECT sku, id FROM product_variants WHERE sku = ANY($1::text[])
		ORDER BY sku`, []string{measuredSKU, unmeasuredSKU})
	if err != nil {
		t.Fatalf("read parcel variants: %v", err)
	}
	for rows.Next() {
		var sku string
		var id uuid.UUID
		if err := rows.Scan(&sku, &id); err != nil {
			t.Fatalf("scan parcel variant: %v", err)
		}
		variantIDs[sku] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate parcel variants: %v", err)
	}
	rows.Close()
	if len(variantIDs) != 2 {
		t.Fatalf("read %d parcel variants, want 2", len(variantIDs))
	}
	for _, sku := range []string{measuredSKU, unmeasuredSKU} {
		if err := stock.NewStore(pool).Receive(ctx, sku, 2, actor.String(), "parse-stock-"+uuid.NewString()); err != nil {
			t.Fatalf("stock %s: %v", sku, err)
		}
	}
	if err := s.SetStatus(ctx, slug, "active"); err != nil {
		t.Fatalf("publish parcel product: %v", err)
	}

	methodCode := "parse_pickup_" + uuid.NewString()[:8]
	if errs, err := shipping.NewStore(pool).CreateMethod(ctx, &shipping.NewMethod{
		Code: methodCode, Destination: "pickup_point", Name: "量測超取",
		Carrier: "測試承運人", MaxParcelWeightG: 10000,
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateMethod: %v %v", err, errs)
	}
	var methodID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_methods WHERE code = $1`, methodCode).Scan(&methodID); err != nil {
		t.Fatalf("read parcel method: %v", err)
	}
	defer func() {
		if err := shipping.NewStore(pool).SetMethodActive(context.WithoutCancel(ctx), methodID.String(), false); err != nil {
			t.Errorf("deactivate parcel method: %v", err)
		}
	}()

	basket := cart.NewStore(pool)
	for _, tt := range []struct {
		name    string
		sku     string
		offered bool
	}{
		{name: "measured oversized parcel", sku: measuredSKU, offered: false},
		{name: "unmeasured parcel", sku: unmeasuredSKU, offered: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cartID, err := basket.Create(ctx, uuid.NewString(), uuid.NullUUID{})
			if err != nil {
				t.Fatalf("create cart: %v", err)
			}
			if addErr := basket.Add(ctx, cartID, variantIDs[tt.sku], 1); addErr != nil {
				t.Fatalf("add %s to cart: %v", tt.sku, addErr)
			}
			choices, err := basket.ShippingChoices(ctx, cartID, 100000)
			if err != nil {
				t.Fatalf("ShippingChoices: %v", err)
			}
			codes := make([]string, 0, len(choices))
			for i := range choices {
				codes = append(codes, choices[i].Code)
			}
			if got := slices.Contains(codes, methodCode); got != tt.offered {
				t.Errorf("ShippingChoices(%s) offered %q = %v, want %v; choices=%v",
					tt.sku, methodCode, got, tt.offered, codes)
			}
		})
	}
}
