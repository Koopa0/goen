//go:build integration

package checkout_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/shipping"
)

// TestTheOrderNamesItsShippingMethodInTheReadersLanguage: the method's name is
// read from the version the order was priced from, so an English reader is not
// shown the Chinese name the shop typed, and a Chinese reader still is.
func TestTheOrderNamesItsShippingMethodInTheReadersLanguage(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)

	var methodID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO shipping_methods (code, destination_kind)
		VALUES ($1, 'pickup_point') RETURNING id`, "named"+uuid.NewString()[:6]).Scan(&methodID); err != nil {
		t.Fatalf("create method: %v", err)
	}
	var versionID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO shipping_method_versions (method_id, name, name_en, fee_cents)
		VALUES ($1, '測試超取', 'Test pickup', 6000) RETURNING id`, methodID).Scan(&versionID); err != nil {
		t.Fatalf("create version: %v", err)
	}

	id := newCart(t, s)
	if err := s.Carts().Add(ctx, id, variantOf(t, "pixelight-9-pro"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	addr := &delivery.Details{
		To:    shipping.DestinationPickupPoint,
		Email: "named@example.com", RecipientName: "林小美", Phone: "0955666777",
		PickupChain: "seven_eleven",
	}
	number, err := placeOrder(t, s, ctx, id, uuid.NullUUID{}, versionID, addr, "",
		"named-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	for _, tt := range []struct {
		locale i18n.Locale
		want   string
	}{{i18n.En, "Test pickup"}, {i18n.ZhHant, "測試超取"}} {
		view, err := buyer.NewStore(pool).Order(i18n.WithLocale(ctx, tt.locale), number)
		if err != nil {
			t.Fatalf("read the order in %s: %v", tt.locale.Tag(), err)
		}
		if view.ShippingName != tt.want {
			t.Errorf("shipping name in %s = %q, want %q", tt.locale.Tag(), view.ShippingName, tt.want)
		}
	}
}

// TestAMethodIsNotOfferedForAParcelItsCarrierRefuses. A method is offered only
// for a cart its carrier will physically take, PER ITEM and never over the cart
// total: two things that each fit are two parcels, but one item cannot be split.
func TestAMethodIsNotOfferedForAParcelItsCarrierRefuses(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)

	code := "cvs" + uuid.NewString()[:6]
	var methodID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO shipping_methods (code, destination_kind,
		                              max_parcel_longest_mm, max_parcel_sum_mm, max_parcel_weight_g)
		VALUES ($1, 'pickup_point', 450, 1050, 10000) RETURNING id`, code).Scan(&methodID); err != nil {
		t.Fatalf("create method: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO shipping_method_versions (method_id, name, fee_cents)
		VALUES ($1, '測試超取', 6000)`, methodID); err != nil {
		t.Fatalf("create version: %v", err)
	}

	small, big, _ := variantsOf(t, "parcel", 3)
	if _, err := pool.Exec(ctx, `
		UPDATE product_variants SET parcel_longest_mm = 180, parcel_sum_mm = 320, parcel_weight_g = 400
		WHERE id = $1`, small); err != nil {
		t.Fatalf("measure the small one: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE product_variants SET parcel_longest_mm = 700, parcel_sum_mm = 1400, parcel_weight_g = 7000
		WHERE id = $1`, big); err != nil {
		t.Fatalf("measure the big one: %v", err)
	}

	offered := func(t *testing.T, cartID uuid.UUID) bool {
		t.Helper()
		choices, err := s.Carts().ShippingChoices(ctx, cartID, 100000)
		if err != nil {
			t.Fatalf("ShippingChoices: %v", err)
		}
		for i := range choices {
			if choices[i].Code == code {
				return true
			}
		}
		return false
	}

	t.Run("a parcel that fits", func(t *testing.T) {
		id := newCart(t, s)
		if err := s.Carts().Add(ctx, id, small, 1); err != nil {
			t.Fatalf("add: %v", err)
		}
		if !offered(t, id) {
			t.Error("a box the carrier accepts is not being offered the method")
		}
	})

	t.Run("a parcel that does not", func(t *testing.T) {
		id := newCart(t, s)
		if err := s.Carts().Add(ctx, id, big, 1); err != nil {
			t.Fatalf("add: %v", err)
		}
		if offered(t, id) {
			t.Error("a 27-inch monitor is being offered 超商取貨; the customer pays " +
				"for it and the shop finds out at the counter")
		}
	})

	t.Run("one that fits beside one that does not", func(t *testing.T) {
		id := newCart(t, s)
		if err := s.Carts().Add(ctx, id, small, 1); err != nil {
			t.Fatalf("add small: %v", err)
		}
		if err := s.Carts().Add(ctx, id, big, 1); err != nil {
			t.Fatalf("add big: %v", err)
		}
		if offered(t, id) {
			t.Error("the method is offered because one item fits — the oversized one " +
				"still cannot be split, and it is the one that reaches the counter")
		}
	})

	t.Run("an unmeasured variant is refused by nothing", func(t *testing.T) {
		id := newCart(t, s)
		unmeasured := freshVariant(t, "parcel")
		if err := s.Carts().Add(ctx, id, unmeasured, 1); err != nil {
			t.Fatalf("add: %v", err)
		}
		if !offered(t, id) {
			t.Error("an unmeasured variant lost the method: NULL means UNKNOWN, and " +
				"hiding the channel 75.2% of shoppers prefer because nobody typed a " +
				"box size costs more than the counter refusal it prevents")
		}
	})
}

func TestTheCheckoutOffersDeliveryInTheVisitorsLanguage(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	// An empty cart: what a method is OFFERED for depends on what is in the basket.
	id := newCart(t, s)

	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
		absent string
	}{
		{
			name: "Chinese", locale: i18n.ZhHant,
			want: "宅配到府", absent: "Home delivery",
		},
		{
			name: "English", locale: i18n.En,
			want: "Home delivery", absent: "宅配到府",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			choices, err := s.Carts().ShippingChoices(i18n.WithLocale(ctx, tt.locale), id, 100000)
			if err != nil {
				t.Fatalf("ShippingChoices: %v", err)
			}
			names := make([]string, 0, len(choices))
			for i := range choices {
				names = append(names, choices[i].Name)
			}
			if !slices.Contains(names, tt.want) {
				t.Errorf("the chooser offers %v, want %q among them", names, tt.want)
			}
			if slices.Contains(names, tt.absent) {
				t.Errorf("the chooser offers %q, the other language: %v", tt.absent, names)
			}
		})
	}
}
