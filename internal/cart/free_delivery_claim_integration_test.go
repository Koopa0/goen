//go:build integration

package cart_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// The home and product pages claim free delivery from the same shipping methods
// the cart prices, so the claim must be the amount at which the cart's own rule
// says every method is free.
func TestTheStorefrontClaimsFreeDeliveryOnlyWhereTheCartDoes(t *testing.T) {
	ctx := t.Context()

	type method struct {
		pickup   bool
		feeCents int64
		freeOver *int64
	}
	over := func(cents int64) *int64 { return &cents }

	for _, tt := range []struct {
		name       string
		methods    []method
		withPickup bool
		want       int64
	}{
		{"every method has a threshold", []method{{false, 100, over(300000)}, {true, 60, over(200000)}}, true, 300000},
		{"one method never turns free", []method{{false, 100, over(300000)}, {true, 60, nil}}, true, 0},
		{"a method that costs nothing does not block", []method{{false, 100, over(300000)}, {true, 0, nil}}, true, 300000},
		{"a method that costs nothing names no threshold of its own", []method{{false, 100, over(300000)}, {true, 0, over(500000)}}, true, 300000},
		{"every method costs nothing", []method{{false, 0, nil}, {true, 0, over(100)}}, true, 0},
		{"pickup is left out where there is no store map", []method{{false, 100, over(200000)}, {true, 60, over(300000)}}, false, 200000},
		{"a pickup that never turns free is left out too", []method{{false, 100, over(200000)}, {true, 60, nil}}, false, 200000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var active []uuid.UUID
			rows, err := pool.Query(ctx, `SELECT id FROM shipping_methods WHERE is_active`)
			if err != nil {
				t.Fatalf("read active methods: %v", err)
			}
			for rows.Next() {
				var id uuid.UUID
				if scanErr := rows.Scan(&id); scanErr != nil {
					t.Fatalf("scan: %v", scanErr)
				}
				active = append(active, id)
			}
			rows.Close()
			if rowsErr := rows.Err(); rowsErr != nil {
				t.Fatalf("read active methods: %v", rowsErr)
			}
			t.Cleanup(func() {
				if _, restoreErr := pool.Exec(ctx, `UPDATE shipping_methods SET is_active = id = ANY($1)`, active); restoreErr != nil {
					t.Errorf("restore methods: %v", restoreErr)
				}
			})
			if _, withdrawErr := pool.Exec(ctx, `UPDATE shipping_methods SET is_active = false`); withdrawErr != nil {
				t.Fatalf("withdraw methods: %v", withdrawErr)
			}

			for _, m := range tt.methods {
				kind := "address"
				if m.pickup {
					kind = "pickup_point"
				}
				var id uuid.UUID
				if createErr := pool.QueryRow(ctx, `
					INSERT INTO shipping_methods (code, destination_kind)
					VALUES ('claim' || replace($1::text, '-', ''), $2) RETURNING id`,
					uuid.NewString(), kind).Scan(&id); createErr != nil {
					t.Fatalf("create method: %v", createErr)
				}
				if _, versionErr := pool.Exec(ctx, `
					INSERT INTO shipping_method_versions (method_id, name, fee_cents, free_over_cents)
					VALUES ($1, '測試運送', $2, $3)`, id, m.feeCents, m.freeOver); versionErr != nil {
					t.Fatalf("create version: %v", versionErr)
				}
			}

			got, err := db.New(pool).FreeDeliveryThreshold(ctx, tt.withPickup)
			if err != nil {
				t.Fatalf("FreeDeliveryThreshold: %v", err)
			}
			if got != tt.want {
				t.Errorf("FreeDeliveryThreshold(withPickup=%v) = %d, want %d", tt.withPickup, got, tt.want)
			}

			// The cart offers pickup whenever it is active, so parity is checked
			// where the storefront counts it too.
			if !tt.withPickup {
				return
			}
			s := cart.NewStore(pool)
			at := func(subtotal int64) pages.FreeDelivery {
				t.Helper()
				choices, choicesErr := s.ShippingChoices(ctx, uuid.New(), subtotal)
				if choicesErr != nil {
					t.Fatalf("ShippingChoices(%d): %v", subtotal, choicesErr)
				}
				return cart.FreeDeliveryFor(choices, subtotal)
			}
			if got == 0 {
				if c := at(0); c.Kind == pages.FreeDeliveryShort {
					t.Errorf("the page claims nothing but the cart says %+v at 0", c)
				}
				return
			}
			if c := at(got); c.Kind != pages.FreeDeliveryReached || c.ThresholdCents != got {
				t.Errorf("at the claimed %d the cart says %+v, want reached over %d", got, c, got)
			}
			if c := at(got - 1); c.Kind != pages.FreeDeliveryShort || c.ShortfallCents != 1 || c.ThresholdCents != got {
				t.Errorf("one cent under the claimed %d the cart says %+v, want 1 short of %d", got, c, got)
			}
		})
	}
}

func TestAMethodWithAZoneSurchargeNamesTheZoneInTheCartsChoices(t *testing.T) {
	ctx := t.Context()
	var zoneID, methodID, versionID uuid.UUID
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := pool.QueryRow(ctx, `
		INSERT INTO shipping_zones (code, name, name_en) VALUES ('z' || $1::text, '離島乙', 'Outlying islands B') RETURNING id`,
		suffix).Scan(&zoneID); err != nil {
		t.Fatalf("create zone: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO shipping_methods (code, destination_kind) VALUES ('zone' || $1::text, 'address') RETURNING id`,
		suffix).Scan(&methodID); err != nil {
		t.Fatalf("create method: %v", err)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(clean, `UPDATE shipping_methods SET is_active = false WHERE id = $1`, methodID); err != nil {
			t.Errorf("withdraw method: %v", err)
		}
	})
	if err := pool.QueryRow(ctx, `
		INSERT INTO shipping_method_versions (method_id, name, fee_cents, free_over_cents)
		VALUES ($1, '測試運送', 100, 300000) RETURNING id`, methodID).Scan(&versionID); err != nil {
		t.Fatalf("create version: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO shipping_version_zones (version_id, zone_id, surcharge_cents) VALUES ($1, $2, 20000)`,
		versionID, zoneID); err != nil {
		t.Fatalf("create surcharge: %v", err)
	}

	choices, err := cart.NewStore(pool).ShippingChoices(ctx, uuid.New(), 300000)
	if err != nil {
		t.Fatalf("ShippingChoices: %v", err)
	}
	for _, c := range choices {
		if c.VersionID != versionID.String() {
			continue
		}
		if want := []string{"離島乙"}; !slices.Equal(c.SurchargeZones, want) {
			t.Errorf("ShippingChoices: SurchargeZones = %v, want %v", c.SurchargeZones, want)
		}
		return
	}
	t.Errorf("ShippingChoices did not offer the new method: %v", choices)
}
