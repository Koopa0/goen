package pages

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shipping"
)

func TestOrderEventLabelsCoverEveryKind(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for kind, want := range map[order.EventKind]string{
		order.EventPlaced:    "送出訂單",
		order.EventPaid:      "付款完成",
		order.EventPicking:   "開始備貨",
		order.EventShipped:   "出貨",
		order.EventInTransit: "運送中",
		order.EventDelivered: "送達",
		order.EventCompleted: "訂單完成",
		order.EventCancelled: "取消",
		order.EventRefunded:  "已退款",
	} {
		if got := i18n.T(ctx, OrderEvent{Kind: kind}.LabelKey()); got != want {
			t.Errorf("LabelKey(%s) reads %q, want %q", kind, got, want)
		}
	}
}

// The refund of units the shop could not send says so; a return's refund does not.
func TestARefundOfUnsentUnitsIsNamedAsSuch(t *testing.T) {
	t.Parallel()
	for locale, want := range map[i18n.Locale]string{i18n.ZhHant: "無法出貨，已退款", i18n.En: "Not sent — refunded"} {
		ctx := i18n.WithLocale(t.Context(), locale)
		if got := i18n.T(ctx, OrderEvent{Kind: order.EventRefunded, Unsent: true}.LabelKey()); got != want {
			t.Errorf("%s: the refund of unsent units reads %q, want %q", locale, got, want)
		}
		if got := i18n.T(ctx, OrderEvent{Kind: order.EventRefunded}.LabelKey()); got == want {
			t.Errorf("%s: a return's refund reads %q too", locale, got)
		}
	}
}

func TestTheAdminTimelineKeepsItsStatusWords(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	key, ok := OrderEvent{Kind: order.EventCancelled}.LookupLabelKey()
	if !ok || i18n.T(ctx, key) != "訂單取消" {
		t.Errorf("LookupLabelKey(cancelled) = %q, %v; want the status 訂單取消", i18n.T(ctx, key), ok)
	}
}

func TestKnownPickupChainChoicesMatchValidationAndReturnFreshStorage(t *testing.T) {
	want := []PickupChainChoice{
		{Value: "seven_eleven", Label: "7-ELEVEN"},
		{Value: "family_mart", Label: "全家 FamilyMart"},
		{Value: "hi_life", Label: "萊爾富 Hi-Life"},
		{Value: "ok_mart", Label: "OK mart"},
	}
	got := KnownPickupChainChoices()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("KnownPickupChainChoices() mismatch (-want +got):\n%s", diff)
	}
	for _, choice := range got {
		if !choice.Value.Known() {
			t.Errorf("KnownPickupChainChoices() offers %q, but KnownPickupChain rejects it", choice.Value)
		}
	}

	got[0].Value = "other_chain"
	got[0].Label = "Other"
	if diff := cmp.Diff(want, KnownPickupChainChoices()); diff != "" {
		t.Errorf("mutating KnownPickupChainChoices() changed the next result (-want +got):\n%s", diff)
	}
	if shipping.Chain("other_chain").Known() {
		t.Error("mutating KnownPickupChainChoices() changed KnownPickupChain")
	}
}

// TestCheckoutOffersTheChainsTheShopShipsTo holds the two sets apart: a shopper
// chooses between the chains whose store picker the shop will integrate, and the
// back office keeps every chain, because an order already placed at one of the
// others still has to be correctable.
func TestCheckoutOffersTheChainsTheShopShipsTo(t *testing.T) {
	t.Parallel()

	want := []PickupChainChoice{
		{Value: shipping.ChainSevenEleven, Label: "7-ELEVEN"},
		{Value: shipping.ChainFamilyMart, Label: "全家 FamilyMart"},
	}
	got := CheckoutPickupChainChoices()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("CheckoutPickupChainChoices() mismatch (-want +got):\n%s", diff)
	}
	for _, choice := range got {
		if !choice.Value.Known() {
			t.Errorf("checkout offers %q, which validation rejects", choice.Value)
		}
	}
	if len(KnownPickupChainChoices()) != len(shipping.Chains()) {
		t.Error("the back office no longer offers every chain the shop can accept")
	}
}

// TestAPickupOrderIsNamedByItsChain holds what the confirmation and the back
// office show while no store picker exists: the chain is the destination, and a
// store appears only when one is known.
func TestAPickupOrderIsNamedByItsChain(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		d      Delivery
		pickup bool
		want   string
	}{
		{
			name: "the chain alone", pickup: true,
			d:    Delivery{PickupChain: shipping.ChainSevenEleven},
			want: "7-ELEVEN",
		},
		{
			name: "a chain whose store is known", pickup: true,
			d: Delivery{
				PickupChain: shipping.ChainFamilyMart, PickupStoreCode: "012345",
				PickupStoreName: "台北車站門市",
			},
			want: "全家 FamilyMart 台北車站門市(012345)",
		},
		{
			name: "an address",
			d: Delivery{
				PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
			},
			want: "110 台北市信義區松高路 1 號",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.d.IsPickup(); got != tt.pickup {
				t.Errorf("IsPickup() = %v, want %v", got, tt.pickup)
			}
			if got := tt.d.Line(); got != tt.want {
				t.Errorf("Line() = %q, want %q", got, tt.want)
			}
		})
	}
}
